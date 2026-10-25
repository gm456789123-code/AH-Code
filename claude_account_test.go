package main

import (
	"os"
	"testing"
)

func TestParseClaudeAuthStatus(t *testing.T) {
	base := ClaudeAccountInfo{IsInstalled: true}

	loggedIn := parseClaudeAuthStatus([]byte(`{
		"loggedIn": true, "authMethod": "claude.ai", "email": "hack3rwin@gmail.com",
		"orgId": "60af5eab-586d-43ba-9182-bef49ddd3f13", "subscriptionType": "pro"
	}`), base)
	if !loggedIn.IsInstalled || !loggedIn.IsLoggedIn || loggedIn.Email != "hack3rwin@gmail.com" || loggedIn.SubscriptionType != "pro" {
		t.Errorf("%+v", loggedIn)
	}

	loggedOut := parseClaudeAuthStatus([]byte(`{"loggedIn": false}`), base)
	if !loggedOut.IsInstalled || loggedOut.IsLoggedIn || loggedOut.Email != "" || loggedOut.SubscriptionType != "" {
		t.Errorf("logged-out state must not leak stale account info: %+v", loggedOut)
	}

	// Malformed/unexpected output degrades to "unknown" rather than erroring or crashing —
	// this call backs a UI badge, not a security check.
	for _, junk := range [][]byte{nil, []byte(""), []byte("not json"), []byte("null")} {
		got := parseClaudeAuthStatus(junk, base)
		if got.IsInstalled != base.IsInstalled || got.IsLoggedIn {
			t.Errorf("junk %q must degrade quietly, got %+v", junk, got)
		}
	}
}

func TestGetClaudeAccountInfoWithoutCLI(t *testing.T) {
	a := isolate(t)
	t.Setenv("PATH", t.TempDir()) // hide any real `claude` on this machine's PATH
	info := a.GetClaudeAccountInfo()
	if info.IsInstalled || info.IsLoggedIn {
		t.Errorf("%+v", info)
	}
}

// TestLiveClaudeAccountInfo checks the real, installed `claude` CLI's own reported status.
// Opt-in: SENTCODE_LIVE_CLAUDE=1 go test -run TestLiveClaudeAccountInfo -v
func TestLiveClaudeAccountInfo(t *testing.T) {
	if os.Getenv("SENTCODE_LIVE_CLAUDE") == "" {
		t.Skip("set SENTCODE_LIVE_CLAUDE=1 to run against the real claude CLI")
	}
	a := isolate(t)
	if _, ok := resolveCLI("claude"); !ok {
		t.Skip("claude not on PATH")
	}
	info := a.GetClaudeAccountInfo()
	t.Logf("%+v", info)
	if !info.IsInstalled {
		t.Fatal("claude is on PATH, IsInstalled must be true")
	}
	if !info.IsLoggedIn || info.SubscriptionType == "" {
		t.Fatalf("expected a logged-in account with a real plan on this machine, got %+v", info)
	}
}
