package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// TelegramExport is the top-level structure of a Telegram Desktop JSON export file.
type TelegramExport struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"` // "personal_chat", "saved_messages", "group", "supergroup", "channel"
	ID       int64             `json:"id"`
	Messages []TelegramMessage `json:"messages"`
}

// TelegramMessage represents a single message entry in a Telegram export.
type TelegramMessage struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"` // "message" or "service"
	Date      string          `json:"date"`
	DateUnix  string          `json:"date_unixtime"`
	From      string          `json:"from"`
	FromID    string          `json:"from_id"` // e.g. "user123456"
	Text      json.RawMessage `json:"text"`
	Photo     string          `json:"photo"`
	MediaType string          `json:"media_type"` // "photo", "video", "voice_message", "sticker", "animation", "document"
	File      string          `json:"file"`
}

// extractTelegramText reads the "text" field which may be a plain string
// or an array of strings/text objects (for messages with entities like links).
func extractTelegramText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return ""
	}
	var parts []string
	for _, item := range arr {
		var plain string
		if err := json.Unmarshal(item, &plain); err == nil {
			parts = append(parts, plain)
			continue
		}
		var obj struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(item, &obj); err == nil && obj.Text != "" {
			parts = append(parts, obj.Text)
		}
	}
	return strings.Join(parts, "")
}

// ParseTelegramExport parses a Telegram Desktop JSON export (result.json) and inserts
// all messages into userDB. Returns counts of inserted and skipped (duplicate) messages.
func ParseTelegramExport(userDB *sql.DB, r io.Reader) (inserted int, skipped int, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read file: %w", err)
	}

	var exp TelegramExport
	if err := json.Unmarshal(data, &exp); err != nil {
		return 0, 0, fmt.Errorf("failed to parse JSON: %w", err)
	}

	if len(exp.Messages) == 0 {
		return 0, 0, fmt.Errorf("no messages found — verify this is a Telegram Desktop JSON export")
	}

	chatName := exp.Name
	chatIDStr := strconv.FormatInt(exp.ID, 10)
	threadID := int(exp.ID & 0x7FFFFFFF) // low bits of chat ID as int thread_id

	for _, tm := range exp.Messages {
		if tm.Type != "message" {
			continue // skip service entries (join/leave/pin events)
		}

		// Parse timestamp
		var msgDate time.Time
		if tm.DateUnix != "" {
			if unixSec, e := strconv.ParseInt(tm.DateUnix, 10, 64); e == nil {
				msgDate = time.Unix(unixSec, 0)
			}
		}
		if msgDate.IsZero() && tm.Date != "" {
			if t, e := time.Parse("2006-01-02T15:04:05", tm.Date); e == nil {
				msgDate = t
			}
		}
		if msgDate.IsZero() {
			slog.Warn("Telegram: skipping message with unparseable date", "id", tm.ID)
			continue
		}

		body := extractTelegramText(tm.Text)
		messageIDStr := strconv.FormatInt(tm.ID, 10)

		// Address is the chat identifier; for personal chats it's the contact's ID.
		var address, contactName string
		switch exp.Type {
		case "saved_messages":
			address = "saved_messages"
			contactName = "Saved Messages"
		default: // personal_chat, group, supergroup, channel
			address = chatIDStr
			contactName = chatName
		}

		// contentType marks the record as MMS-equivalent when media is present
		var contentType string
		if tm.MediaType != "" || tm.Photo != "" || tm.File != "" {
			switch tm.MediaType {
			case "photo":
				contentType = "image/jpeg"
			case "video", "video_message":
				contentType = "video/mp4"
			case "voice_message":
				contentType = "audio/ogg"
			case "sticker":
				contentType = "image/webp"
			case "animation":
				contentType = "image/gif"
			default:
				contentType = "application/octet-stream"
			}
			if tm.Photo != "" && contentType == "" {
				contentType = "image/jpeg"
			}
		}

		msg := &Message{
			Address:     address,
			Body:        body,
			Type:        1, // direction unknown without own user ID — mark as received
			Date:        msgDate,
			Read:        true,
			ThreadID:    threadID,
			Subject:     chatName,
			ContactName: contactName,
			Sender:      tm.FromID,
			MessageID:   messageIDStr,
			ContentType: contentType,
		}

		if insertErr := InsertMessage(userDB, msg); insertErr != nil {
			slog.Debug("Telegram: error inserting message", "id", tm.ID, "error", insertErr)
			skipped++
			continue
		}
		if msg.ID > 0 {
			inserted++
		} else {
			skipped++ // ON CONFLICT DO NOTHING
		}
	}

	return inserted, skipped, nil
}
