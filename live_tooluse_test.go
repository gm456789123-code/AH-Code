package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveToolUse drives the real, already-installed Qwen model with a task that requires it to
// actually run a command to answer correctly (a fact only discoverable by reading a file).
// Opt-in: SENTCODE_LIVE=1 go test -run TestLiveToolUse -v -timeout 3m
func TestLiveToolUse(t *testing.T) {
	if os.Getenv("SENTCODE_LIVE") == "" {
		t.Skip("set SENTCODE_LIVE=1 to run against the real engine")
	}
	a := NewApp()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.engine.Ensure(ctx); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	def, ok := a.findModel("qwen-coder-7b")
	if !ok || !isInstalled(def) {
		t.Skip("qwen2.5-coder:7b not installed on this machine")
	}

	dir := t.TempDir()
	secret := "kumquat-77219"
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("the magic word is "+secret), 0644); err != nil {
		t.Fatal(err)
	}

	reply, actions, err := a.chatOllamaWithTools(context.Background(), def,
		ChatRequest{Message: "Read secret.txt in this project and tell me exactly what the magic word is. Nothing else."}, dir)
	if err != nil {
		t.Fatalf("chatOllamaWithTools: %v", err)
	}
	t.Logf("actions: %+v", actions)
	t.Logf("reply: %s", reply)

	if len(actions) == 0 {
		t.Fatal("the model never ran a command; it could not have known the answer without one")
	}
	if !strings.Contains(reply, secret) {
		t.Fatalf("reply must contain the real secret read from disk (%q), got: %s", secret, reply)
	}
}
