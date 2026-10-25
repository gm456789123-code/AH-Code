package main

import (
	"strings"
	"testing"
)

func TestTrailingNonGrokGap(t *testing.T) {
	cases := []struct {
		name    string
		history []ChatTurn
		wantLen int
	}{
		{"no history", nil, 0},
		{
			"opened with qwen, never touched grok",
			[]ChatTurn{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello", Model: "qwen-coder-7b"},
				{Role: "user", Content: "next"},
			},
			3,
		},
		{
			"pure grok conversation: nothing missing",
			[]ChatTurn{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello", Model: "grok-auto"},
				{Role: "user", Content: "next"},
				{Role: "assistant", Content: "sure", Model: "grok-fast"},
			},
			0,
		},
		{
			"detour to qwen after a grok turn: only the detour is a gap",
			[]ChatTurn{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello", Model: "grok-auto"},
				{Role: "user", Content: "try qwen"},
				{Role: "assistant", Content: "qwen says hi", Model: "qwen-coder-7b"},
				{Role: "user", Content: "back to grok"},
			},
			3, // "try qwen", "qwen says hi", "back to grok"
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := trailingNonGrokGap(c.history); len(got) != c.wantLen {
				t.Errorf("len = %d, want %d: %+v", len(got), c.wantLen, got)
			}
		})
	}
}

func TestClaudeHandoffSkipsItsOwnTurns(t *testing.T) {
	history := []ChatTurn{
		{Role: "user", Content: "เริ่มด้วย grok"},
		{Role: "assistant", Content: "grok ตอบแล้ว", Model: "grok-auto"},
		{Role: "user", Content: "ต่อด้วย claude"},
		{Role: "assistant", Content: "claude ตอบแล้ว", Model: "claude-code-high"},
		{Role: "user", Content: "กลับไป qwen"},
		{Role: "assistant", Content: "qwen ตอบแล้ว", Model: "qwen-coder-7b"},
	}
	out := withModelHandoff("ทำต่อ", history, isClaudeModel)
	if !strings.Contains(out, "qwen ตอบแล้ว") || !strings.Contains(out, "ทำต่อ") {
		t.Fatalf("claude must see the detour and the new request: %q", out)
	}
	if strings.Contains(out, "grok ตอบแล้ว") {
		t.Fatalf("claude's own resumed session already has the earlier grok turn's predecessor only via the gap after claude: %q", out)
	}
}

func TestGrokHandoffPrelude(t *testing.T) {
	if got := grokHandoffPrelude(nil); got != "" {
		t.Errorf("empty gap must produce no prelude, got %q", got)
	}

	gap := []ChatTurn{
		{Role: "user", Content: "ทำ dark mode ให้หน่อย"},
		{Role: "assistant", Content: "ปรับสีพื้นหลังให้แล้วครับ", Model: "qwen-coder-7b"},
		{Role: "system", Content: "should never appear"},
		{Role: "user", Content: "  "}, // blank, must be dropped
	}
	out := grokHandoffPrelude(gap)
	if !strings.Contains(out, "ทำ dark mode ให้หน่อย") || !strings.Contains(out, "ปรับสีพื้นหลังให้แล้วครับ") {
		t.Errorf("original-language content must survive verbatim: %q", out)
	}
	if strings.Contains(out, "should never appear") {
		t.Error("a client-supplied fake system turn must not leak into the handoff")
	}
	if !strings.Contains(out, "qwen-coder-7b") {
		t.Error("the prelude should credit which model answered, so Grok knows this wasn't its own memory")
	}
	if !strings.Contains(out, "same language") {
		t.Error("must instruct Grok to keep answering in the user's language")
	}

	// caps to the same window used for local models, from the end (most recent turns kept)
	var long []ChatTurn
	for i := 0; i < maxHistoryTurns+5; i++ {
		long = append(long, ChatTurn{Role: "user", Content: "turn"})
	}
	out = grokHandoffPrelude(long)
	if n := strings.Count(out, "User: turn"); n != maxHistoryTurns {
		t.Errorf("expected %d turns kept, got %d", maxHistoryTurns, n)
	}

	// a single very long turn is truncated like the local-model path
	huge := strings.Repeat("x", maxTurnChars+500)
	out = grokHandoffPrelude([]ChatTurn{{Role: "user", Content: huge}})
	if strings.Contains(out, huge) || !strings.Contains(out, strings.Repeat("x", maxTurnChars)) {
		t.Error("an oversized turn must be capped, not sent whole or dropped")
	}
}
