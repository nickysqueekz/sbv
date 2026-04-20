package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// GoogleChatExport is the structure of a Google Chat Takeout messages.json file.
type GoogleChatExport struct {
	Messages []GoogleChatMessage `json:"messages"`
}

// GoogleChatMessage represents a single message in a Google Chat export.
type GoogleChatMessage struct {
	Creator     GoogleChatCreator `json:"creator"`
	CreatedDate string            `json:"created_date"`
	Text        string            `json:"text"`
	MessageID   string            `json:"message_id"`
	TopicID     string            `json:"topic_id"`
	Annotations []json.RawMessage `json:"annotations"` // attachments, reactions — presently ignored
}

// GoogleChatCreator identifies the author of a Google Chat message.
type GoogleChatCreator struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// googleChatDateFormats are tried in order when parsing Google Chat created_date strings.
var googleChatDateFormats = []string{
	"Monday, January 2, 2006 at 3:04:05 PM MST",
	"Monday, January 2, 2006 at 3:04:05 PM UTC",
	"Monday, January 02, 2006 at 3:04:05 PM MST",
	"Mon, Jan 2, 2006 at 3:04:05 PM MST",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05-07:00",
}

func parseGoogleChatDate(s string) (time.Time, error) {
	// Normalize "at 3:04:05 PM UTC" (some exports use "UTC" as zone)
	s = strings.TrimSpace(s)
	for _, layout := range googleChatDateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format: %q", s)
}

// ParseGoogleChatExport parses a Google Chat Takeout messages.json file and inserts
// all messages into userDB. Returns counts of inserted and skipped (duplicate) messages.
func ParseGoogleChatExport(userDB *sql.DB, r io.Reader) (inserted int, skipped int, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read file: %w", err)
	}

	var exp GoogleChatExport
	if err := json.Unmarshal(data, &exp); err != nil {
		return 0, 0, fmt.Errorf("failed to parse JSON: %w", err)
	}

	if len(exp.Messages) == 0 {
		return 0, 0, fmt.Errorf("no messages found — verify this is a Google Chat messages.json export")
	}

	// Build conversation address from unique participant emails (sorted for stability)
	emailSet := make(map[string]bool)
	for _, m := range exp.Messages {
		if m.Creator.Email != "" {
			emailSet[strings.ToLower(m.Creator.Email)] = true
		}
	}
	var emails []string
	for e := range emailSet {
		emails = append(emails, e)
	}
	sort.Strings(emails)
	address := strings.Join(emails, ",")
	if address == "" {
		address = "google-chat-unknown"
	}

	// Use the chat participant names as the conversation subject
	nameSet := make(map[string]bool)
	for _, m := range exp.Messages {
		if m.Creator.Name != "" {
			nameSet[m.Creator.Name] = true
		}
	}
	var names []string
	for n := range nameSet {
		names = append(names, n)
	}
	sort.Strings(names)
	chatName := strings.Join(names, ", ")

	// Stable thread ID from address hash
	threadID := stableIntHash(address)

	for _, gm := range exp.Messages {
		if gm.Text == "" && len(gm.Annotations) == 0 {
			continue // skip empty messages
		}

		msgDate, parseErr := parseGoogleChatDate(gm.CreatedDate)
		if parseErr != nil {
			slog.Warn("GoogleChat: skipping message with unparseable date",
				"date", gm.CreatedDate, "error", parseErr)
			continue
		}

		msg := &Message{
			Address:     address,
			Body:        gm.Text,
			Type:        1, // direction unknown — mark as received
			Date:        msgDate,
			Read:        true,
			ThreadID:    threadID,
			Subject:     chatName,
			ContactName: gm.Creator.Name,
			Sender:      gm.Creator.Email,
			MessageID:   gm.MessageID,
		}

		if insertErr := InsertMessage(userDB, msg); insertErr != nil {
			slog.Debug("GoogleChat: error inserting message", "id", gm.MessageID, "error", insertErr)
			skipped++
			continue
		}
		if msg.ID > 0 {
			inserted++
		} else {
			skipped++
		}
	}

	return inserted, skipped, nil
}

// stableIntHash returns a stable positive int32-range hash of a string, suitable for use as thread_id.
func stableIntHash(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h & 0x7FFFFFFF
}
