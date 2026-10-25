package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLiveClaudeCodeRemembersAcrossTurns reproduces the exact scenario reported: ask Claude Code
// something, then ask a follow-up that only makes sense if it remembers the first turn — driven
// through chatCLI exactly as the app calls it, not a shortcut.
// Opt-in: SENTCODE_LIVE_CLAUDE=1 go test -run TestLiveClaudeCodeRemembersAcrossTurns -v -timeout 2m
func TestLiveClaudeCodeRemembersAcrossTurns(t *testing.T) {
	if os.Getenv("SENTCODE_LIVE_CLAUDE") == "" {
		t.Skip("set SENTCODE_LIVE_CLAUDE=1 to run against the real claude CLI")
	}
	a := isolate(t)
	def, ok := a.findModel("claude-code-low")
	if !ok {
		t.Fatal("claude-code-low missing from registry")
	}
	if _, installed := resolveCLI(def.Command); !installed {
		t.Skip("claude not on PATH")
	}

	dir := t.TempDir()
	logID := "live_test_session"

	reply1, err := a.chatCLI(context.Background(), def,
		ChatRequest{Message: "Remember this exact phrase: the secret handshake is purple giraffe. Just say OK.", LogSessionID: logID}, dir)
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	t.Logf("turn 1: %s", reply1)

	reply2, err := a.chatCLI(context.Background(), def,
		ChatRequest{Message: "What was the secret handshake I told you? Answer with just the phrase.", LogSessionID: logID}, dir)
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	t.Logf("turn 2: %s", reply2)

	if !strings.Contains(strings.ToLower(reply2), "purple giraffe") {
		t.Fatalf("Claude Code must remember the earlier turn via its own resumed session, got: %s", reply2)
	}

	// A different conversation (different LogSessionID) in the same project must NOT see it
	reply3, err := a.chatCLI(context.Background(), def,
		ChatRequest{Message: "What was the secret handshake I told you? Answer with just the phrase, or say you don't know.", LogSessionID: "a_different_conversation"}, dir)
	if err != nil {
		t.Fatalf("turn 3: %v", err)
	}
	t.Logf("turn 3 (different conversation): %s", reply3)
	if strings.Contains(strings.ToLower(reply3), "purple giraffe") {
		t.Fatal("a different Sent-Code conversation must not leak another conversation's memory")
	}
}
