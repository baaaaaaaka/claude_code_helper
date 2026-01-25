package claudehistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

type Message struct {
	Role      string
	Content   string
	Timestamp time.Time
}

type sessionEnvelope struct {
	Type      string          `json:"type"`
	IsMeta    bool            `json:"isMeta"`
	Message   json.RawMessage `json:"message"`
	Timestamp string          `json:"timestamp"`
}

type sessionMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func ReadSessionMessages(filePath string, maxMessages int) ([]Message, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var ring []Message
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			if msg, ok := parseLineMessage(line); ok {
				if maxMessages > 0 && len(ring) >= maxMessages {
					ring = append(ring[1:], msg)
				} else {
					ring = append(ring, msg)
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	return ring, nil
}

func FormatMessages(messages []Message, maxCharsPerMessage int) string {
	var b strings.Builder
	for i, msg := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		role := "Message"
		switch msg.Role {
		case "user":
			role = "User"
		case "assistant":
			role = "Assistant"
		}
		b.WriteString(role)
		b.WriteString(":")
		b.WriteString("\n")
		text := strings.TrimSpace(msg.Content)
		if maxCharsPerMessage > 0 {
			text = truncateRunes(text, maxCharsPerMessage)
		}
		b.WriteString(text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func FormatSession(session Session, messages []Message) string {
	var b strings.Builder
	b.WriteString("Session: ")
	b.WriteString(session.SessionID)
	b.WriteString("\n")
	if session.ProjectPath != "" {
		b.WriteString("Project: ")
		b.WriteString(session.ProjectPath)
		b.WriteString("\n")
	}
	if session.Summary != "" {
		b.WriteString("Summary: ")
		b.WriteString(session.Summary)
		b.WriteString("\n")
	}
	if session.CreatedAt.IsZero() == false {
		b.WriteString("Created: ")
		b.WriteString(session.CreatedAt.Format(time.RFC3339))
		b.WriteString("\n")
	}
	if session.ModifiedAt.IsZero() == false {
		b.WriteString("Modified: ")
		b.WriteString(session.ModifiedAt.Format(time.RFC3339))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(FormatMessages(messages, 0))
	b.WriteString("\n")
	return b.String()
}

func parseLineMessage(line []byte) (Message, bool) {
	var env sessionEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Message{}, false
	}
	if env.IsMeta || env.Type == "file-history-snapshot" {
		return Message{}, false
	}
	if len(env.Message) == 0 {
		return Message{}, false
	}
	var msg sessionMessage
	if err := json.Unmarshal(env.Message, &msg); err != nil {
		return Message{}, false
	}
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	if role != "user" && role != "assistant" {
		return Message{}, false
	}
	text := extractText(msg.Content)
	text = strings.TrimSpace(text)
	if text == "" || shouldSkipContent(text) {
		return Message{}, false
	}
	return Message{
		Role:      role,
		Content:   text,
		Timestamp: parseTime(env.Timestamp),
	}, true
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}

	var asArray []map[string]any
	if err := json.Unmarshal(raw, &asArray); err == nil {
		var parts []string
		for _, item := range asArray {
			typ, _ := item["type"].(string)
			switch typ {
			case "thinking", "tool_result", "tool_use":
				continue
			case "text", "input_text":
				if txt, ok := item["text"].(string); ok {
					parts = append(parts, txt)
				}
			default:
				if txt, ok := item["text"].(string); ok {
					parts = append(parts, txt)
				}
			}
		}
		return strings.Join(parts, "\n")
	}

	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err == nil {
		if txt, ok := asObject["text"].(string); ok {
			return txt
		}
		if txt, ok := asObject["content"].(string); ok {
			return txt
		}
	}

	return ""
}

func shouldSkipContent(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "<local-command-") {
		return true
	}
	if strings.Contains(lower, "<command-name>") || strings.Contains(lower, "<command-message>") {
		return true
	}
	if strings.Contains(lower, "<local-command-caveat>") {
		return true
	}
	return false
}
