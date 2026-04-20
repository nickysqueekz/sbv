package internal

import (
"crypto/rand"
"database/sql"
"encoding/hex"
"fmt"
"time"

"github.com/google/uuid"
"golang.org/x/crypto/bcrypt"
)

var authDB *sql.DB

// InitAuthDB initializes the authentication database.
// In PostgreSQL mode the filepath argument is ignored.
func InitAuthDB(filepath string) error {
var err error

if pgMode {
authDB, err = openPGConn("public")
if err != nil {
return fmt.Errorf("postgres: open auth: %w", err)
}
_, err = authDB.Exec(pgAuthDDL)
return err
}

authDB, err = sql.Open("sqlite3", filepath)
if err != nil {
return err
}
if err = authDB.Ping(); err != nil {
return err
}
_, err = authDB.Exec("PRAGMA busy_timeout=5000;")
if err != nil {
return fmt.Errorf("failed to set busy timeout: %w", err)
}
if UseWALMode {
_, err = authDB.Exec("PRAGMA journal_mode=WAL;")
if err != nil {
return fmt.Errorf("failed to enable WAL mode: %w", err)
}
}
_, err = authDB.Exec(`
CREATE TABLE IF NOT EXISTS users (
id            TEXT PRIMARY KEY,
username      TEXT NOT NULL UNIQUE,
password_hash TEXT NOT NULL,
created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
id         TEXT PRIMARY KEY,
user_id    TEXT NOT NULL,
created_at INTEGER NOT NULL,
expires_at INTEGER NOT NULL,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS settings (
user_id       TEXT PRIMARY KEY,
settings_json TEXT NOT NULL DEFAULT '{}',
updated_at    INTEGER NOT NULL,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id    ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS gdrive_tokens (
user_id       TEXT    PRIMARY KEY,
access_token  TEXT    NOT NULL,
refresh_token TEXT    NOT NULL DEFAULT '',
token_type    TEXT    NOT NULL DEFAULT 'Bearer',
expiry        INTEGER NOT NULL DEFAULT 0,
FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
`)
return err
}

// ── Google Drive token storage ────────────────────────────────────────────────

// GDriveToken holds an OAuth2 access/refresh token pair for Google Drive.
type GDriveToken struct {
AccessToken  string
RefreshToken string
TokenType    string
Expiry       int64 // Unix timestamp; 0 = no expiry
}

// SaveGDriveToken upserts an OAuth2 token for a user.
func SaveGDriveToken(userID string, tok *GDriveToken) error {
_, err := execDB(authDB, `
INSERT INTO gdrive_tokens (user_id, access_token, refresh_token, token_type, expiry)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET
access_token  = excluded.access_token,
refresh_token = excluded.refresh_token,
token_type    = excluded.token_type,
expiry        = excluded.expiry
`, userID, tok.AccessToken, tok.RefreshToken, tok.TokenType, tok.Expiry)
return err
}

// GetGDriveToken retrieves the stored OAuth2 token for a user.
func GetGDriveToken(userID string) (*GDriveToken, error) {
var tok GDriveToken
err := queryRowDB(authDB,
"SELECT access_token, refresh_token, token_type, expiry FROM gdrive_tokens WHERE user_id = ?",
userID,
).Scan(&tok.AccessToken, &tok.RefreshToken, &tok.TokenType, &tok.Expiry)
if err == sql.ErrNoRows {
return nil, fmt.Errorf("no Google Drive token stored")
}
return &tok, err
}

// HasGDriveToken returns true if the user has a stored Google Drive token.
func HasGDriveToken(userID string) (bool, error) {
_, err := GetGDriveToken(userID)
if err != nil && err.Error() == "no Google Drive token stored" {
return false, nil
}
return err == nil, err
}

// DeleteGDriveToken removes the stored Google Drive token for a user.
func DeleteGDriveToken(userID string) error {
_, err := execDB(authDB, "DELETE FROM gdrive_tokens WHERE user_id = ?", userID)
return err
}

// ── User management ───────────────────────────────────────────────────────────

// CreateUser creates a new user with a hashed password.
func CreateUser(username, password string) (*User, error) {
userID := uuid.New().String()
hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
if err != nil {
return nil, fmt.Errorf("failed to hash password: %w", err)
}
createdAt := time.Now().Unix()
_, err = execDB(authDB,
"INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)",
userID, username, string(hashedPassword), createdAt,
)
if err != nil {
return nil, fmt.Errorf("failed to create user: %w", err)
}
return &User{
ID:           userID,
Username:     username,
PasswordHash: string(hashedPassword),
CreatedAt:    time.Unix(createdAt, 0),
}, nil
}

// GetUserByUsername retrieves a user by username.
func GetUserByUsername(username string) (*User, error) {
var user User
var createdAt int64
err := queryRowDB(authDB,
"SELECT id, username, password_hash, created_at FROM users WHERE username = ?",
username,
).Scan(&user.ID, &user.Username, &user.PasswordHash, &createdAt)
if err != nil {
if err == sql.ErrNoRows {
return nil, fmt.Errorf("user not found")
}
return nil, err
}
user.CreatedAt = time.Unix(createdAt, 0)
return &user, nil
}

// VerifyPassword checks if the provided password matches the user's hash.
func VerifyPassword(user *User, password string) bool {
return bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil
}

// GetUsernameByID retrieves a username by user ID.
func GetUsernameByID(userID string) (string, error) {
var username string
err := queryRowDB(authDB, "SELECT username FROM users WHERE id = ?", userID).Scan(&username)
if err != nil {
if err == sql.ErrNoRows {
return "", fmt.Errorf("user not found")
}
return "", err
}
return username, nil
}

// ── Session management ────────────────────────────────────────────────────────

// GenerateSessionID generates a cryptographically random session ID.
func GenerateSessionID() (string, error) {
b := make([]byte, 32)
if _, err := rand.Read(b); err != nil {
return "", err
}
return hex.EncodeToString(b), nil
}

// CreateSession creates a new 30-day session for a user.
func CreateSession(userID string, username string) (*Session, error) {
sessionID, err := GenerateSessionID()
if err != nil {
return nil, fmt.Errorf("failed to generate session ID: %w", err)
}
createdAt := time.Now()
expiresAt := createdAt.Add(30 * 24 * time.Hour)
_, err = execDB(authDB,
"INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)",
sessionID, userID, createdAt.Unix(), expiresAt.Unix(),
)
if err != nil {
return nil, fmt.Errorf("failed to create session: %w", err)
}
return &Session{
ID:        sessionID,
UserID:    userID,
Username:  username,
CreatedAt: createdAt,
ExpiresAt: expiresAt,
}, nil
}

// GetSession retrieves and validates a session by ID.
func GetSession(sessionID string) (*Session, error) {
var session Session
var createdAt, expiresAt int64
err := queryRowDB(authDB, `
SELECT s.id, s.user_id, u.username, s.created_at, s.expires_at
FROM sessions s
JOIN users u ON s.user_id = u.id
WHERE s.id = ?`,
sessionID,
).Scan(&session.ID, &session.UserID, &session.Username, &createdAt, &expiresAt)
if err != nil {
if err == sql.ErrNoRows {
return nil, fmt.Errorf("session not found")
}
return nil, err
}
session.CreatedAt = time.Unix(createdAt, 0)
session.ExpiresAt = time.Unix(expiresAt, 0)
if time.Now().After(session.ExpiresAt) {
DeleteSession(sessionID) //nolint:errcheck
return nil, fmt.Errorf("session expired")
}
return &session, nil
}

// DeleteSession deletes a session by ID.
func DeleteSession(sessionID string) error {
_, err := execDB(authDB, "DELETE FROM sessions WHERE id = ?", sessionID)
return err
}

// CleanExpiredSessions removes all expired sessions.
func CleanExpiredSessions() error {
_, err := execDB(authDB, "DELETE FROM sessions WHERE expires_at < ?", time.Now().Unix())
return err
}

// UpdatePassword updates a user's password.
func UpdatePassword(userID string, newPassword string) error {
hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
if err != nil {
return fmt.Errorf("failed to hash password: %w", err)
}
_, err = execDB(authDB,
"UPDATE users SET password_hash = ? WHERE id = ?",
string(hashedPassword), userID,
)
return err
}

// ListUsers returns all users ordered by username.
func ListUsers() ([]User, error) {
rows, err := queryDB(authDB, "SELECT id, username, password_hash, created_at FROM users ORDER BY username")
if err != nil {
return nil, fmt.Errorf("failed to query users: %w", err)
}
defer rows.Close()
var users []User
for rows.Next() {
var user User
var createdAt int64
if err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &createdAt); err != nil {
return nil, fmt.Errorf("failed to scan user: %w", err)
}
user.CreatedAt = time.Unix(createdAt, 0)
users = append(users, user)
}
return users, rows.Err()
}
