package internal

import (
"context"
"crypto/rand"
"encoding/hex"
"encoding/json"
"fmt"
"io"
"log/slog"
"net/http"
"net/url"
"os"
"path/filepath"
"sync"
"time"

"github.com/labstack/echo/v4"
"golang.org/x/oauth2"
"golang.org/x/oauth2/google"
)

// oauthState is a pending OAuth2 state nonce tied to a user and an expiry.
type oauthState struct {
userID  string
expires time.Time
}

var (
gdriveStateMu sync.Mutex
gdriveStates  = make(map[string]oauthState)
)

// getOAuthConfig builds an oauth2.Config from environment variables.
// Required: GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, APP_BASE_URL
func getOAuthConfig() (*oauth2.Config, error) {
clientID := os.Getenv("GOOGLE_CLIENT_ID")
clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
baseURL := os.Getenv("APP_BASE_URL")
if clientID == "" || clientSecret == "" || baseURL == "" {
return nil, fmt.Errorf("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and APP_BASE_URL must be set")
}
return &oauth2.Config{
ClientID:     clientID,
ClientSecret: clientSecret,
RedirectURL:  baseURL + "/api/gdrive/callback",
Scopes:       []string{"https://www.googleapis.com/auth/drive.readonly"},
Endpoint:     google.Endpoint,
}, nil
}

// generateState creates a random hex state nonce.
func generateState() (string, error) {
b := make([]byte, 16)
if _, err := rand.Read(b); err != nil {
return "", err
}
return hex.EncodeToString(b), nil
}

// purgeExpiredStates removes state nonces that have passed their TTL.
func purgeExpiredStates() {
now := time.Now()
gdriveStateMu.Lock()
defer gdriveStateMu.Unlock()
for k, v := range gdriveStates {
if now.After(v.expires) {
delete(gdriveStates, k)
}
}
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

// HandleGDriveStatus returns {"configured": bool, "connected": bool}.
func HandleGDriveStatus(c echo.Context) error {
sess := c.Get("session").(*Session)
_, err := getOAuthConfig()
configured := err == nil
connected := false
if configured {
connected, _ = HasGDriveToken(sess.UserID)
}
return c.JSON(http.StatusOK, map[string]bool{
"configured": configured,
"connected":  connected,
})
}

// HandleGDriveAuth redirects the user to the Google OAuth2 consent screen.
func HandleGDriveAuth(c echo.Context) error {
sess := c.Get("session").(*Session)
cfg, err := getOAuthConfig()
if err != nil {
return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
}
state, err := generateState()
if err != nil {
return c.JSON(http.StatusInternalServerError, map[string]string{"error": "state generation failed"})
}
purgeExpiredStates()
gdriveStateMu.Lock()
gdriveStates[state] = oauthState{userID: sess.UserID, expires: time.Now().Add(10 * time.Minute)}
gdriveStateMu.Unlock()
authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
return c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// HandleGDriveCallback is the public OAuth2 redirect target.
func HandleGDriveCallback(c echo.Context) error {
state := c.QueryParam("state")
code := c.QueryParam("code")
errParam := c.QueryParam("error")
if errParam != "" {
return c.Redirect(http.StatusTemporaryRedirect, "/?gdrive_error="+errParam)
}
purgeExpiredStates()
gdriveStateMu.Lock()
st, ok := gdriveStates[state]
if ok {
delete(gdriveStates, state)
}
gdriveStateMu.Unlock()
if !ok || time.Now().After(st.expires) {
return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid or expired state"})
}
cfg, err := getOAuthConfig()
if err != nil {
return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
tok, err := cfg.Exchange(context.Background(), code)
if err != nil {
slog.Error("gdrive: token exchange failed", "error", err)
return c.Redirect(http.StatusTemporaryRedirect, "/?gdrive_error=exchange_failed")
}
dbTok := &GDriveToken{
AccessToken:  tok.AccessToken,
RefreshToken: tok.RefreshToken,
TokenType:    tok.TokenType,
Expiry:       tok.Expiry.Unix(),
}
if err := SaveGDriveToken(st.userID, dbTok); err != nil {
slog.Error("gdrive: failed to save token", "error", err)
return c.Redirect(http.StatusTemporaryRedirect, "/?gdrive_error=save_failed")
}
return c.Redirect(http.StatusTemporaryRedirect, "/")
}

// gdriveFile represents a Drive file entry returned to the frontend.
type gdriveFile struct {
ID           string `json:"id"`
Name         string `json:"name"`
MimeType     string `json:"mimeType"`
Size         string `json:"size"`
ModifiedTime string `json:"modifiedTime"`
}

// getAccessToken returns a valid access token, refreshing it if expired.
func getAccessToken(userID string) (string, error) {
dbTok, err := GetGDriveToken(userID)
if err != nil {
return "", err
}
cfg, err := getOAuthConfig()
if err != nil {
return "", err
}
tok := &oauth2.Token{
AccessToken:  dbTok.AccessToken,
RefreshToken: dbTok.RefreshToken,
TokenType:    dbTok.TokenType,
Expiry:       time.Unix(dbTok.Expiry, 0),
}
ts := cfg.TokenSource(context.Background(), tok)
newTok, err := ts.Token()
if err != nil {
return "", fmt.Errorf("gdrive: token refresh failed: %w", err)
}
if newTok.AccessToken != tok.AccessToken {
updated := &GDriveToken{
AccessToken:  newTok.AccessToken,
RefreshToken: newTok.RefreshToken,
TokenType:    newTok.TokenType,
Expiry:       newTok.Expiry.Unix(),
}
if saveErr := SaveGDriveToken(userID, updated); saveErr != nil {
slog.Warn("gdrive: failed to persist refreshed token", "error", saveErr)
}
}
return newTok.AccessToken, nil
}

// HandleGDriveFiles lists Drive files matching the optional ?q= search term.
// Only .xml, .json, and .zip files are returned.
func HandleGDriveFiles(c echo.Context) error {
sess := c.Get("session").(*Session)
search := c.QueryParam("q")

accessToken, err := getAccessToken(sess.UserID)
if err != nil {
return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not connected to Google Drive"})
}

// Build Drive API query
driveQ := "(mimeType='application/zip' or name contains '.xml' or name contains '.json') and trashed=false"
if search != "" {
driveQ = fmt.Sprintf("name contains '%s' and trashed=false", sanitizeSearchTerm(search))
}

apiURL := "https://www.googleapis.com/drive/v3/files?q=" +
url.QueryEscape(driveQ) +
"&fields=files(id,name,mimeType,size,modifiedTime)&pageSize=100"

req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, apiURL, nil)
if err != nil {
return c.JSON(http.StatusInternalServerError, map[string]string{"error": "request build failed"})
}
req.Header.Set("Authorization", "Bearer "+accessToken)

resp, err := http.DefaultClient.Do(req)
if err != nil {
return c.JSON(http.StatusBadGateway, map[string]string{"error": "Drive API unreachable"})
}
defer resp.Body.Close()

if resp.StatusCode != http.StatusOK {
return c.JSON(http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("Drive API returned %d", resp.StatusCode)})
}

var result struct {
Files []gdriveFile `json:"files"`
}
if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to parse Drive response"})
}
if result.Files == nil {
result.Files = []gdriveFile{}
}
return c.JSON(http.StatusOK, result.Files)
}

// HandleGDriveImport downloads a Drive file into the user''s ingest directory.
func HandleGDriveImport(dataDir string) echo.HandlerFunc {
return func(c echo.Context) error {
sess := c.Get("session").(*Session)
var body struct {
FileID   string `json:"file_id"`
Filename string `json:"filename"`
}
if err := c.Bind(&body); err != nil || body.FileID == "" || body.Filename == "" {
return c.JSON(http.StatusBadRequest, map[string]string{"error": "file_id and filename required"})
}
// Sanitize filename to prevent path traversal
body.Filename = filepath.Base(body.Filename)

accessToken, err := getAccessToken(sess.UserID)
if err != nil {
return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not connected to Google Drive"})
}

apiURL := "https://www.googleapis.com/drive/v3/files/" +
url.PathEscape(body.FileID) + "?alt=media"

req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, apiURL, nil)
if err != nil {
return c.JSON(http.StatusInternalServerError, map[string]string{"error": "request build failed"})
}
req.Header.Set("Authorization", "Bearer "+accessToken)

resp, err := http.DefaultClient.Do(req)
if err != nil {
return c.JSON(http.StatusBadGateway, map[string]string{"error": "Drive API unreachable"})
}
defer resp.Body.Close()

if resp.StatusCode != http.StatusOK {
return c.JSON(http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("Drive API returned %d", resp.StatusCode)})
}

// Write to ingest directory
ingestDir := filepath.Join(dataDir, sess.UserID, "ingest")
if err := os.MkdirAll(ingestDir, 0o700); err != nil {
return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create ingest dir"})
}
destPath := filepath.Join(ingestDir, body.Filename)

f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
if err != nil {
return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create file"})
}
defer f.Close()

if _, err := io.Copy(f, resp.Body); err != nil {
os.Remove(destPath)
return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to write file"})
}

return c.JSON(http.StatusOK, map[string]string{"filename": body.Filename, "status": "imported"})
}
}

// HandleGDriveDisconnect removes the stored token for the current user.
func HandleGDriveDisconnect(c echo.Context) error {
sess := c.Get("session").(*Session)
if err := DeleteGDriveToken(sess.UserID); err != nil {
return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
return c.JSON(http.StatusOK, map[string]string{"status": "disconnected"})
}

// ── helpers ───────────────────────────────────────────────────────────────────

// sanitizeSearchTerm strips single quotes to prevent injection in Drive API queries.
func sanitizeSearchTerm(s string) string {
out := make([]byte, 0, len(s))
for i := 0; i < len(s); i++ {
if s[i] != '\'' {
out = append(out, s[i])
}
}
return string(out)
}
