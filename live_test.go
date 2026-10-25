package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveEngineSmoke installs the real engine into the real data dir and chats with a tiny model.
// Opt-in only: SENTCODE_LIVE=1 go test -run TestLiveEngineSmoke -v -timeout 30m
func TestLiveEngineSmoke(t *testing.T) {
	if os.Getenv("SENTCODE_LIVE") == "" {
		t.Skip("set SENTCODE_LIVE=1 to run (downloads ~1.4 GB)")
	}
	a := NewApp()
	t.Cleanup(a.engine.Stop)

	if _, err := os.Stat(ollamaBinary()); err != nil {
		start := time.Now()
		if err := a.installEngine(context.Background()); err != nil {
			t.Fatalf("engine install: %v", err)
		}
		t.Logf("engine installed in %s", time.Since(start).Round(time.Second))
	} else {
		t.Log("engine already installed, reusing")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.engine.Ensure(ctx); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	t.Logf("engine %s on %s", a.engine.version(ctx), a.engine.baseURL())

	tiny := ModelDef{ID: "smoke", Name: "Smoke", Kind: KindOllama, OllamaTag: "qwen2.5-coder:0.5b"}
	if err := a.pullModel(context.Background(), tiny); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if !isInstalled(tiny) {
		t.Fatal("manifest not found on disk after pull; ollamaManifestPath does not match the real store layout")
	}

	reply, err := a.chatOllama(ctx, tiny, ChatRequest{Message: "Write a Go function that adds two ints. Code only."}, "")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	t.Logf("reply:\n%s", reply)
	if len(reply) < 10 {
		t.Fatal("reply too short")
	}
}
