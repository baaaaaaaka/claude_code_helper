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

type sessionEnvelopeMeta struct {
	sessionEnvelope
	Cwd       string `json:"cwd"`
	SessionID string `json:"sessionId"`
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
	var fallback []Message
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			if msg, ok := parseLineMessage(line); ok {
				appendMessage(&ring, msg, maxMessages)
			} else {
				for _, fb := range parseLineFallbackMessages(line) {
					appendMessage(&fallback, fb, maxMessages)
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	if len(ring) == 0 && len(fallback) > 0 {
		return fallback, nil
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
		case "thinking":
			role = "Thinking"
		case "tool":
			role = "Tool"
		case "tool_result":
			role = "Tool Result"
		case "meta":
			role = "Meta"
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

func appendMessage(ring *[]Message, msg Message, maxMessages int) {
	if maxMessages > 0 && len(*ring) >= maxMessages {
		*ring = append((*ring)[1:], msg)
		return
	}
	*ring = append(*ring, msg)
}

func parseLineMessage(line []byte) (Message, bool) {
	var env sessionEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Message{}, false
	}
	return parseEnvelopeMessage(env)
}

func parseLineFallbackMessages(line []byte) []Message {
	var env sessionEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil
	}
	ts := parseTime(env.Timestamp)
	if env.IsMeta || env.Type == "file-history-snapshot" {
		return []Message{{
			Role:      "meta",
			Content:   string(line),
			Timestamp: ts,
		}}
	}
	if len(env.Message) == 0 {
		return nil
	}
	var msg sessionMessage
	if err := json.Unmarshal(env.Message, &msg); err != nil {
		return nil
	}
	return fallbackMessagesFromContent(msg.Content, ts)
}

func fallbackMessagesFromContent(raw json.RawMessage, timestamp time.Time) []Message {
	if len(raw) == 0 {
		return nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	var out []Message
	switch content := payload.(type) {
	case []any:
		for _, item := range content {
			out = append(out, fallbackMessageFromItem(item, timestamp)...)
		}
	case map[string]any:
		out = append(out, fallbackMessageFromItem(content, timestamp)...)
	}
	return out
}

func fallbackMessageFromItem(item any, timestamp time.Time) []Message {
	entry, ok := item.(map[string]any)
	if !ok {
		return nil
	}
	typ, _ := entry["type"].(string)
	switch typ {
	case "thinking":
		if text, ok := entry["thinking"].(string); ok && strings.TrimSpace(text) != "" {
			return []Message{{Role: "thinking", Content: text, Timestamp: timestamp}}
		}
		if text, ok := entry["text"].(string); ok && strings.TrimSpace(text) != "" {
			return []Message{{Role: "thinking", Content: text, Timestamp: timestamp}}
		}
	case "tool_use":
		name, _ := entry["name"].(string)
		id, _ := entry["id"].(string)
		label := "Tool use"
		if name != "" {
			label = label + " " + name
		}
		if id != "" {
			label = label + " (" + id + ")"
		}
		content := label
		if input, ok := entry["input"]; ok {
			if formatted := formatJSONBlock(input); formatted != "" {
				content = content + "\n" + formatted
			}
		}
		return []Message{{Role: "tool", Content: content, Timestamp: timestamp}}
	case "tool_result":
		id, _ := entry["tool_use_id"].(string)
		label := "Tool result"
		if isErr, ok := entry["is_error"].(bool); ok && isErr {
			label = "Tool result error"
		}
		if id != "" {
			label = label + " (" + id + ")"
		}
		content := label
		if text := extractToolResultText(entry["content"]); text != "" {
			content = content + "\n" + text
		} else if formatted := formatJSONBlock(entry["content"]); formatted != "" {
			content = content + "\n" + formatted
		}
		return []Message{{Role: "tool_result", Content: content, Timestamp: timestamp}}
	}
	return nil
}

func extractToolResultText(content any) string {
	switch raw := content.(type) {
	case string:
		return raw
	case []any:
		parts := []string{}
		for _, item := range raw {
			switch v := item.(type) {
			case string:
				parts = append(parts, v)
			case map[string]any:
				if typ, _ := v["type"].(string); typ == "text" {
					if txt, ok := v["text"].(string); ok {
						parts = append(parts, txt)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func formatJSONBlock(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func parseEnvelopeMessage(env sessionEnvelope) (Message, bool) {
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
