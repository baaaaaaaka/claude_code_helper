package claudehistory

import "testing"

func TestFormatMessagesTruncatesByRunes(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: "游戏开始测试"},
	}
	out := FormatMessages(msgs, 4)
	if out != "Assistant:\n游戏开始…" {
		t.Fatalf("unexpected output: %q", out)
	}
}
