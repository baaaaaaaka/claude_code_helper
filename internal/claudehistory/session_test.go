package claudehistory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormatMessagesTruncatesByRunes(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: "游戏开始测试"},
	}
	out := FormatMessages(msgs, 4)
	if out != "Assistant:\n游戏开始…" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestSessionParsingHelpers(t *testing.T) {
	t.Run("ReadSessionMessages handles invalid lines and ring buffer", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.jsonl")
		content := strings.Join([]string{
			`{"type":"user","message":{"role":"user","content":"hi"},"timestamp":"2026-01-01T00:00:00Z"}`,
			`not-json`,
			`{"type":"user","isMeta":true,"message":{"role":"user","content":"ignore"},"timestamp":"2026-01-01T00:00:01Z"}`,
			`{"type":"assistant","message":{"role":"assistant","content":"there"},"timestamp":"2026-01-01T00:00:02Z"}`,
			``,
		}, "\n")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write session file: %v", err)
		}

		all, err := ReadSessionMessages(path, 0)
		if err != nil {
			t.Fatalf("ReadSessionMessages error: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(all))
		}
		if all[0].Role != "user" || all[1].Role != "assistant" {
			t.Fatalf("unexpected roles: %#v", all)
		}

		last, err := ReadSessionMessages(path, 1)
		if err != nil {
			t.Fatalf("ReadSessionMessages error: %v", err)
		}
		if len(last) != 1 || last[0].Content != "there" {
			t.Fatalf("expected ring buffer to keep last message, got %#v", last)
		}
	})

	t.Run("ReadSessionMessages errors on missing file", func(t *testing.T) {
		_, err := ReadSessionMessages(filepath.Join(t.TempDir(), "missing.jsonl"), 0)
		if err == nil {
			t.Fatalf("expected error for missing file")
		}
	})

	t.Run("FormatMessages respects roles and maxChars", func(t *testing.T) {
		msgs := []Message{
			{Role: "user", Content: " hello "},
			{Role: "assistant", Content: "world"},
		}
		out := FormatMessages(msgs, 0)
		want := "User:\nhello\n\nAssistant:\nworld"
		if out != want {
			t.Fatalf("expected %q, got %q", want, out)
		}

		out = FormatMessages(msgs, -1)
		if out != want {
			t.Fatalf("expected %q for negative max, got %q", want, out)
		}
	})

	t.Run("truncateRunes handles boundaries", func(t *testing.T) {
		if got := truncateRunes("abc", 0); got != "" {
			t.Fatalf("expected empty for max=0, got %q", got)
		}
		if got := truncateRunes("你好", 2); got != "你好" {
			t.Fatalf("expected full string, got %q", got)
		}
		if got := truncateRunes("你好", 1); got != "你…" {
			t.Fatalf("expected truncation, got %q", got)
		}
	})

	t.Run("parseLineMessage handles invalid and valid lines", func(t *testing.T) {
		if _, ok := parseLineMessage([]byte("not-json")); ok {
			t.Fatalf("expected invalid JSON to be skipped")
		}
		if _, ok := parseLineMessage([]byte("")); ok {
			t.Fatalf("expected empty line to be skipped")
		}
		line := []byte(`{"type":"user","message":{"role":"user","content":"hey"},"timestamp":"2026-01-01T00:00:00Z"}`)
		msg, ok := parseLineMessage(line)
		if !ok || msg.Content != "hey" {
			t.Fatalf("expected valid message, got %#v ok=%v", msg, ok)
		}
	})

	t.Run("parseEnvelopeMessage filters non-text and invalid roles", func(t *testing.T) {
		if _, ok := parseEnvelopeMessage(sessionEnvelope{IsMeta: true}); ok {
			t.Fatalf("expected meta envelope to be skipped")
		}
		if _, ok := parseEnvelopeMessage(sessionEnvelope{Type: "file-history-snapshot"}); ok {
			t.Fatalf("expected snapshot envelope to be skipped")
		}
		if _, ok := parseEnvelopeMessage(sessionEnvelope{Message: []byte(`{}`)}); ok {
			t.Fatalf("expected missing role to be skipped")
		}
		env := sessionEnvelope{
			Message:   []byte(`{"role":"system","content":"nope"}`),
			Timestamp: "2026-01-01T00:00:00Z",
		}
		if _, ok := parseEnvelopeMessage(env); ok {
			t.Fatalf("expected invalid role to be skipped")
		}
		env = sessionEnvelope{
			Message:   []byte(`{"role":"user","content":"<LOCAL-COMMAND-FOO>"}`),
			Timestamp: "2026-01-01T00:00:00Z",
		}
		if _, ok := parseEnvelopeMessage(env); ok {
			t.Fatalf("expected command content to be skipped")
		}
		env = sessionEnvelope{
			Message:   []byte(`{"role":"assistant","content":"ok"}`),
			Timestamp: "2026-01-01T00:00:00Z",
		}
		msg, ok := parseEnvelopeMessage(env)
		if !ok || msg.Role != "assistant" || msg.Content != "ok" {
			t.Fatalf("expected valid message, got %#v ok=%v", msg, ok)
		}
	})

	t.Run("extractText handles strings, arrays, objects", func(t *testing.T) {
		if got := extractText([]byte(`"plain"`)); got != "plain" {
			t.Fatalf("expected string content, got %q", got)
		}
		array := `[{"type":"thinking","text":"skip"},{"type":"text","text":"keep"},{"type":"input_text","text":"also"}]`
		if got := extractText([]byte(array)); got != "keep\nalso" {
			t.Fatalf("expected array content, got %q", got)
		}
		if got := extractText([]byte(`{"text":"hi"}`)); got != "hi" {
			t.Fatalf("expected object text, got %q", got)
		}
		if got := extractText([]byte(`{"content":"hey"}`)); got != "hey" {
			t.Fatalf("expected object content, got %q", got)
		}
		if got := extractText(nil); got != "" {
			t.Fatalf("expected empty for nil raw, got %q", got)
		}
	})

	t.Run("shouldSkipContent is case insensitive", func(t *testing.T) {
		if !shouldSkipContent("<LOCAL-COMMAND-FOO>") {
			t.Fatalf("expected local command marker to be skipped")
		}
		if shouldSkipContent("regular text") {
			t.Fatalf("expected regular text not to be skipped")
		}
	})

	t.Run("FormatSession renders metadata and messages", func(t *testing.T) {
		session := Session{
			SessionID:   "sess-1",
			ProjectPath: "/tmp/project",
			Summary:     "summary",
			CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			ModifiedAt:  time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
		}
		messages := []Message{{Role: "user", Content: "hi"}}
		out := FormatSession(session, messages)
		if !strings.Contains(out, "Session: sess-1") {
			t.Fatalf("expected session header, got %q", out)
		}
		if !strings.Contains(out, "Project: /tmp/project") || !strings.Contains(out, "Summary: summary") {
			t.Fatalf("expected metadata lines, got %q", out)
		}
		if !strings.Contains(out, "User:\nhi") {
			t.Fatalf("expected formatted messages, got %q", out)
		}
	})
}
