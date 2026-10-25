package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveModelUsesSearchTool drives the real, already-installed Qwen model with a question it
// cannot know the answer to without searching, to confirm it actually reaches for SEARCH: and
// uses the real result end-to-end.
// Opt-in: SENTCODE_LIVE=1 go test -run TestLiveModelUsesSearchTool -v -timeout 3m
func TestLiveModelUsesSearchTool(t *testing.T) {
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
	reply, actions, err := a.chatOllamaWithTools(context.Background(), def,
		ChatRequest{Message: "Search the web for the official Go module path of Anthropic's Go SDK on pkg.go.dev, and tell me the exact import path."},
		dir)
	if err != nil {
		t.Fatalf("chatOllamaWithTools: %v", err)
	}
	t.Logf("actions: %+v", actions)
	t.Logf("reply: %s", reply)

	found := false
	for _, act := range actions {
		if act.Type == "search" {
			found = true
		}
	}
	if !found {
		t.Fatal("the model never used the SEARCH tool for a question it could not otherwise answer")
	}
}
