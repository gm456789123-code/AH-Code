package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLiveClaudeCodeCLI drives the real `claude` CLI through chatCLI exactly as the app would,
// including the Windows .cmd-shim / stdin path, which nothing else in the test suite exercises
// (Grok resolves to a real .exe; the hostile-prompt CLI test uses powershell.exe/findstr).
// Opt-in: SENTCODE_LIVE_CLAUDE=1 go test -run TestLiveClaudeCodeCLI -v
func TestLiveClaudeCodeCLI(t *testing.T) {
	if os.Getenv("SENTCODE_LIVE_CLAUDE") == "" {
		t.Skip("set SENTCODE_LIVE_CLAUDE=1 to run against the real claude CLI")
	}
	a := isolate(t)

	for _, id := range []string{"claude-code-low", "claude-code-medium", "claude-code-high", "claude-code-ultra"} {
		def, ok := a.findModel(id)
		if !ok {
			t.Fatalf("%s not registered", id)
		}
		if _, installed := resolveCLI(def.Command); !installed {
			t.Skip("claude not on PATH")
		}
		t.Run(id, func(t *testing.T) {
			out, err := a.chatCLI(context.Background(), def, ChatRequest{Message: `Reply with exactly: OK ` + id}, "")
			if err != nil {
				t.Fatalf("chatCLI: %v", err)
			}
			if !strings.Contains(out, "OK") {
				t.Errorf("unexpected output: %q", out)
			}
			t.Logf("args=%v -> %q", def.Args, out)
		})
	}

	// Full path through runRegistryModel, including token accounting and action list
	def, _ := a.findModel("claude-code-low")
	a.userTokens = 100
	resp := a.runRegistryModel(ChatRequest{Message: "Reply with exactly: PING"}, def, "")
	if resp.Status != "ok" || !strings.Contains(resp.Reply, "PING") {
		t.Fatalf("runRegistryModel: %+v", resp)
	}
	t.Logf("full response: %+v", resp)
}
