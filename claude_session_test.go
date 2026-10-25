package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestDeterministicUUID(t *testing.T) {
	a := deterministicUUID("session_1")
	b := deterministicUUID("session_1")
	c := deterministicUUID("session_2")
	if a != b {
		t.Error("same input must always produce the same id")
	}
	if a == c {
		t.Error("different inputs must produce different ids")
	}
	if !uuidRe.MatchString(a) {
		t.Errorf("not a well-formed UUID: %q", a)
	}
	if a[14] != '5' {
		t.Errorf("must be tagged version 5, got %q", a)
	}
	if v := a[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Errorf("must be tagged RFC 4122 variant, got %q", a)
	}
}

// TestClaudeCodePathEncodingMatchesRealCLI hard-codes the exact input/output pair observed from
// the real, installed `claude` CLI (see the chat transcript this was verified in), so a future
// change to our regex can't silently drift from what Claude Code itself actually does.
func TestClaudeCodePathEncodingMatchesRealCLI(t *testing.T) {
	in := `C:\Users\Saint\AppData\Local\Temp\cc_test2\sub dir_with-chars.here`
	want := "C--Users-Saint-AppData-Local-Temp-cc-test2-sub-dir-with-chars-here"
	if got := claudeCodePathEncodeRe.ReplaceAllString(in, "-"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClaudeCodeSessionExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // os.UserHomeDir() on Windows
	t.Setenv("HOME", home)        // and elsewhere

	proj := `C:\proj`
	sid := deterministicUUID("x")
	if claudeCodeSessionExists(proj, sid) {
		t.Error("must be false before the session file exists")
	}
	if claudeCodeSessionExists("", sid) || claudeCodeSessionExists(proj, "") {
		t.Error("must be false for a blank project or session id, never stat a nonsense path")
	}

	encoded := claudeCodePathEncodeRe.ReplaceAllString(proj, "-")
	dir := filepath.Join(home, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if !claudeCodeSessionExists(proj, sid) {
		t.Error("must be true once the session file exists")
	}
}

func TestClaudeCodeSessionArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	if args := claudeCodeSessionArgs("", `C:\proj`); args != nil {
		t.Errorf("no conversation id must mean no session flags: %v", args)
	}
	if args := claudeCodeSessionArgs("sc_1", ""); args != nil {
		t.Errorf("no project must mean no session flags: %v", args)
	}

	args := claudeCodeSessionArgs("sc_1", `C:\proj`)
	if len(args) != 2 || args[0] != "--session-id" {
		t.Errorf("first turn must start a session by id: %v", args)
	}
	sid := args[1]

	// same conversation id + project must reuse the same session id every time
	again := claudeCodeSessionArgs("sc_1", `C:\proj`)
	if len(again) != 2 || again[0] != "--session-id" || again[1] != sid {
		t.Errorf("must be deterministic before the session actually exists on disk: %v", again)
	}

	// once Claude Code has actually created that session, later turns must resume it
	encoded := claudeCodePathEncodeRe.ReplaceAllString(`C:\proj`, "-")
	dir := filepath.Join(home, ".claude", "projects", encoded)
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte("{}"), 0644)

	resumed := claudeCodeSessionArgs("sc_1", `C:\proj`)
	if len(resumed) != 2 || resumed[0] != "--resume" || resumed[1] != sid {
		t.Errorf("must resume the existing session: %v", resumed)
	}

	// a different conversation in the same project must get its own, different session id
	other := claudeCodeSessionArgs("sc_2", `C:\proj`)
	if other[1] == sid {
		t.Error("different conversations must not share a Claude Code session id")
	}
}
