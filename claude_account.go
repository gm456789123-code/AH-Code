package main

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

// ClaudeAccountInfo reports the real Claude Code login and subscription plan, read straight from
// the CLI itself (`claude auth status --json`) rather than guessed at — mirrors how
// GetGrokAccountInfo reports Grok's real tier.
type ClaudeAccountInfo struct {
	IsInstalled      bool   `json:"isInstalled"`
	IsLoggedIn       bool   `json:"isLoggedIn"`
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"` // e.g. "pro", "free", "max"; "" if unknown
}

// GetClaudeAccountInfo asks the installed `claude` CLI for its own auth/plan status
func (a *App) GetClaudeAccountInfo() ClaudeAccountInfo {
	info := ClaudeAccountInfo{}
	claudeBin, ok := resolveCLI("claude")
	info.IsInstalled = ok
	if !ok {
		return info
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, claudeBin, "auth", "status", "--json")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return info
	}
	return parseClaudeAuthStatus(out, info)
}

// parseClaudeAuthStatus fills in the parts of `base` that `claude auth status --json` reports.
// base.IsInstalled is preserved as passed in; malformed output leaves the rest at zero values
// (logged out) rather than erroring, since a live account check should degrade quietly.
func parseClaudeAuthStatus(out []byte, base ClaudeAccountInfo) ClaudeAccountInfo {
	var status struct {
		LoggedIn         bool   `json:"loggedIn"`
		Email            string `json:"email"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if json.Unmarshal(out, &status) != nil {
		return base
	}
	base.IsLoggedIn = status.LoggedIn
	base.Email = status.Email
	base.SubscriptionType = status.SubscriptionType
	return base
}

// --- Session continuity ---
//
// Unlike Grok (already resumed via its own --resume) and unlike Qwen (given a durable history by
// chatOllama), a plain `claude -p` call starts a brand-new, memoryless conversation every single
// time — confirmed live: asked "do you see earlier history?", Claude Code correctly reported no.
// Claude Code does have its own real session persistence (--session-id to start one,
// --resume to continue it, verified against the installed CLI), so chatCLI gives it a
// deterministic session id derived from this Sent-Code conversation's own id, and resumes it on
// every turn after the first.

// claudeUUIDNamespace is an arbitrary fixed namespace for deriving Claude Code session ids; it
// only needs to be constant across runs, not registered anywhere.
var claudeUUIDNamespace = [16]byte{0x53, 0x65, 0x6e, 0x74, 0x2d, 0x43, 0x6f, 0x64, 0x65, 0x2d, 0x43, 0x6c, 0x61, 0x75, 0x64, 0x65}

// deterministicUUID derives a stable RFC 4122 v5-style UUID from name, so the same Sent-Code
// conversation always maps to the same Claude Code session id across turns and app restarts,
// without needing to persist a separate id anywhere ourselves.
func deterministicUUID(name string) string {
	h := sha1.New()
	h.Write(claudeUUIDNamespace[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// claudeCodePathEncodeRe mirrors Claude Code's own project-folder encoding for its session
// storage (~/.claude/projects/<encoded path>/<session-id>.jsonl): every character that isn't a
// plain ASCII letter or digit becomes '-' (verified against the installed CLI's real output).
var claudeCodePathEncodeRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// claudeCodeSessionExists reports whether Claude Code already has a session with this id for
// this project, so chatCLI knows whether to resume it or start it fresh with that exact id.
func claudeCodeSessionExists(projectPath, sessionID string) bool {
	if projectPath == "" || sessionID == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	encoded := claudeCodePathEncodeRe.ReplaceAllString(projectPath, "-")
	path := filepath.Join(home, ".claude", "projects", encoded, sessionID+".jsonl")
	_, err = os.Stat(path)
	return err == nil
}

// claudeCodeSessionArgs returns the extra flags that give this Claude Code call real memory of
// this Sent-Code conversation: nil when there is nothing to key a session by (no conversation id
// or no project — Claude Code's own session storage is keyed by project folder).
func claudeCodeSessionArgs(logSessionID, projectPath string) []string {
	if logSessionID == "" || projectPath == "" {
		return nil
	}
	sid := deterministicUUID("sent-code-conversation:" + logSessionID)
	if claudeCodeSessionExists(projectPath, sid) {
		return []string{"--resume", sid}
	}
	return []string{"--session-id", sid}
}
