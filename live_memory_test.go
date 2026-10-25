package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLiveMemoryReproduction reproduces the exact multi-turn scenario the user reported (can't
// answer "what did I say above") against the real, already-installed Qwen model, printing the
// full prompt sent to Ollama so we can see whether history is actually reaching it.
// Opt-in: SENTCODE_LIVE=1 go test -run TestLiveMemoryReproduction -v -timeout 5m
func TestLiveMemoryReproduction(t *testing.T) {
	if os.Getenv("SENTCODE_LIVE") == "" {
		t.Skip("set SENTCODE_LIVE=1 to run against the real engine")
	}
	a := NewApp() // NOT isolated: use the real, already-installed engine/model on this machine
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.engine.Ensure(ctx); err != nil {
		t.Fatalf("engine start: %v", err)
	}

	def, ok := a.findModel("qwen-coder-7b")
	if !ok || !isInstalled(def) {
		t.Skip("qwen2.5-coder:7b not installed on this machine")
	}

	turn1 := "นายเห็นข้อความด้านบนมั้ย"
	reply1, err := a.chatOllama(context.Background(), def, ChatRequest{Message: turn1}, "")
	if err != nil {
		t.Fatalf("turn1: %v", err)
	}
	t.Logf("turn1 reply: %s", reply1)

	history := []ChatTurn{
		{Role: "user", Content: turn1},
		{Role: "assistant", Content: reply1, Model: "qwen-coder-7b"},
	}
	turn2 := "ตอบคำถามด้านบนที่นายยังไม่ได้ตอบให้ที"
	reply2, err := a.chatOllama(context.Background(), def, ChatRequest{Message: turn2, History: history}, "")
	if err != nil {
		t.Fatalf("turn2: %v", err)
	}
	fmt.Println("=== TURN 2 (with history) ===")
	fmt.Println("reply:", reply2)
}
