package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendStoredTurnPersistsAcrossLoads(t *testing.T) {
	isolate(t) // redirects LOCALAPPDATA to a temp dir

	appendStoredTurn("sc_1", `C:\proj`, "user", "", "ทำ dark mode ให้หน่อย", 2)
	appendStoredTurn("sc_1", `C:\proj`, "assistant", "qwen-coder-7b", "  ปรับให้แล้วครับ  ", 0)

	s, ok := loadStoredSession(`C:\proj`, "sc_1")
	if !ok {
		t.Fatal("session must be readable back from disk")
	}
	if s.ProjectPath != `C:\proj` {
		t.Errorf("project path = %q", s.ProjectPath)
	}
	if s.Title != "ทำ dark mode ให้หน่อย" {
		t.Errorf("title should come from the first user turn, got %q", s.Title)
	}
	if len(s.Turns) != 2 {
		t.Fatalf("want 2 turns, got %d: %+v", len(s.Turns), s.Turns)
	}
	if s.Turns[0].Role != "user" || s.Turns[0].Images != 2 || s.Turns[0].Lang != "th" {
		t.Errorf("turn 0 = %+v", s.Turns[0])
	}
	if s.Turns[1].Role != "assistant" || s.Turns[1].Model != "qwen-coder-7b" || s.Turns[1].Content != "ปรับให้แล้วครับ" {
		t.Errorf("turn 1 = %+v (content must be trimmed)", s.Turns[1])
	}
	if s.CreatedAt == "" || s.UpdatedAt == "" {
		t.Error("timestamps must be set")
	}

	// A second, unrelated session in the same project must not collide with the first
	appendStoredTurn("sc_2", `C:\proj`, "user", "", "hello", 0)
	if s2, _ := loadStoredSession(`C:\proj`, "sc_2"); len(s2.Turns) != 1 {
		t.Errorf("sc_2 should be independent, got %+v", s2)
	}
	if s1, _ := loadStoredSession(`C:\proj`, "sc_1"); len(s1.Turns) != 2 {
		t.Error("writing sc_2 must not disturb sc_1")
	}
}

func TestAppendStoredTurnIgnoresJunk(t *testing.T) {
	isolate(t)
	appendStoredTurn("", "", "user", "", "no session id", 0)
	appendStoredTurn("sc_3", "", "user", "", "   ", 0) // blank content
	if _, ok := loadStoredSession("", "sc_3"); ok {
		t.Error("a session must not be created for blank content")
	}
	if _, ok := loadStoredSession("", ""); ok {
		t.Error("empty session id must never produce a file")
	}
}

func TestAppendStoredTurnCapsHistoryLength(t *testing.T) {
	isolate(t)
	for i := 0; i < storeMaxTurns+20; i++ {
		appendStoredTurn("sc_long", "", "user", "", "turn", 0)
	}
	s, ok := loadStoredSession("", "sc_long")
	if !ok || len(s.Turns) != storeMaxTurns {
		t.Fatalf("want %d turns kept, got %d", storeMaxTurns, len(s.Turns))
	}
}

func TestSessionIDIsSanitizedForFilenames(t *testing.T) {
	isolate(t)
	evil := `../../evil`
	appendStoredTurn(evil, "", "user", "", "hi", 0)

	// Whatever the sanitized name looks like, it must resolve to a single file directly inside
	// the project's own sessions directory — never a parent-directory escape via path separators.
	if dir := filepath.Dir(sessionLogPath("", evil)); dir != sessionsDir("") {
		t.Fatalf("path escaped the sessions directory: %s (want parent %s)", sessionLogPath("", evil), sessionsDir(""))
	}
	if _, ok := loadStoredSession("", evil); !ok {
		t.Error("a sanitized id must still round-trip through save/load")
	}
}

func TestStoredHistoryFallback(t *testing.T) {
	isolate(t)
	if h := storedHistoryFallback("", "nope"); h != nil {
		t.Errorf("unknown session must yield nil, got %+v", h)
	}

	appendStoredTurn("sc_4", `C:\proj`, "user", "", "first", 0)
	appendStoredTurn("sc_4", `C:\proj`, "assistant", "grok-auto", "second", 0)
	h := storedHistoryFallback(`C:\proj`, "sc_4")
	if len(h) != 2 || h[0].Role != "user" || h[1].Model != "grok-auto" {
		t.Errorf("%+v", h)
	}

	for i := 0; i < maxHistoryTurns+10; i++ {
		appendStoredTurn("sc_4", `C:\proj`, "user", "", "turn", 0)
	}
	if h := storedHistoryFallback(`C:\proj`, "sc_4"); len(h) != maxHistoryTurns {
		t.Errorf("fallback must cap at maxHistoryTurns, got %d", len(h))
	}
}

func TestSessionsAreNestedPerProjectNotFlat(t *testing.T) {
	isolate(t)
	appendStoredTurn("same_id", `C:\proj-a`, "user", "", "hello from A", 0)
	appendStoredTurn("same_id", `C:\proj-b`, "user", "", "hello from B", 0)

	a, ok := loadStoredSession(`C:\proj-a`, "same_id")
	if !ok || a.Turns[0].Content != "hello from A" {
		t.Errorf("project A: %+v", a)
	}
	b, ok := loadStoredSession(`C:\proj-b`, "same_id")
	if !ok || b.Turns[0].Content != "hello from B" {
		t.Errorf("project B: %+v", b)
	}
	if sessionsDir(`C:\proj-a`) == sessionsDir(`C:\proj-b`) {
		t.Error("two different projects must not share a sessions directory")
	}
	// The session file must actually live under the project's own data directory, not flat
	if !strings.HasPrefix(sessionLogPath(`C:\proj-a`, "same_id"), projectDataDir(`C:\proj-a`)) {
		t.Errorf("session file not nested under its project: %s", sessionLogPath(`C:\proj-a`, "same_id"))
	}
}

func TestProjectSlugIsStableAndCollisionResistant(t *testing.T) {
	if projectSlug(`C:\Users\me\proj`) != projectSlug(`C:\Users\me\proj`) {
		t.Error("slug must be deterministic")
	}
	if projectSlug(`C:\a\frontend`) == projectSlug(`C:\b\frontend`) {
		t.Error("same folder name in different locations must not collide")
	}
	if projectSlug("") != unassignedProjectSlug {
		t.Errorf("no project selected should bucket under %q, got %q", unassignedProjectSlug, projectSlug(""))
	}
	// Windows paths are case-insensitive; the same project under different casing must match
	if projectSlug(`C:\Proj`) != projectSlug(`c:\proj`) {
		t.Error("slug must be case-insensitive to match Windows path semantics")
	}
}

func TestSummaryLogIsSeparateFromChatMemory(t *testing.T) {
	isolate(t)
	appendStoredTurn("sc_5", `C:\proj`, "user", "", "ทำ dark mode ให้หน่อย", 1)
	appendStoredTurn("sc_5", `C:\proj`, "assistant", "qwen-coder-7b", "ปรับให้แล้วครับ นี่คือรายละเอียด...", 0)

	// Two distinct files: the full transcript (chat memory) and the running summary
	chatMemoryPath := sessionLogPath(`C:\proj`, "sc_5")
	logPath := summaryLogPath(`C:\proj`)
	if chatMemoryPath == logPath {
		t.Fatal("chat memory and summary log must be different files")
	}
	if _, err := os.Stat(chatMemoryPath); err != nil {
		t.Fatal("chat memory file missing")
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal("summary log file missing")
	}
	text := string(raw)

	if !strings.Contains(text, "ทำ dark mode ให้หน่อย") {
		t.Error("summary log must keep the user's own words verbatim")
	}
	if !strings.Contains(text, "qwen-coder-7b") {
		t.Error("summary log must credit which model answered")
	}
	if !strings.Contains(text, "[1 image attached]") {
		t.Errorf("summary log must note the attachment: %q", text)
	}
	if !strings.Contains(text, "lang=th") {
		t.Error("summary log must tag the detected language")
	}
	// The label wrapper is English even for a Thai conversation; only the quoted content is Thai
	if !strings.Contains(text, "User |") || !strings.Contains(text, "Assistant (qwen-coder-7b) |") {
		t.Errorf("summary log labels must be in English: %q", text)
	}

	// A long turn is truncated in the summary (unlike chat memory, which keeps much more)
	long := strings.Repeat("x", summaryLinePreview+50)
	appendStoredTurn("sc_5", `C:\proj`, "user", "", long, 0)
	raw, _ = os.ReadFile(logPath)
	if strings.Contains(string(raw), long) {
		t.Error("summary log lines must be capped, not the full turn")
	}
}

func TestSendChatMessageLogsBothSidesAndBackfillsHistory(t *testing.T) {
	a := isolate(t)
	engineCalls := coderEngine(t, a)
	a.userTokens = 100

	req := ChatRequest{Message: "hi there", Model: "qwen-coder-7b", ProjectPath: `C:\proj`, LogSessionID: "sc_6"}
	resp := a.SendChatMessage(req)
	if resp.Status != "ok" {
		t.Fatalf("resp: %+v", resp)
	}

	s, ok := loadStoredSession(`C:\proj`, "sc_6")
	if !ok || len(s.Turns) != 2 || s.Turns[0].Content != "hi there" || s.Turns[1].Content != "here is the code" {
		t.Fatalf("expected user+assistant logged, got %+v", s)
	}
	if s.Turns[1].Model != "qwen-coder-7b" {
		t.Errorf("assistant turn must record which model answered: %+v", s.Turns[1])
	}

	// A second call with no frontend-supplied history must recover it from the log we just wrote
	*engineCalls = nil
	a.SendChatMessage(ChatRequest{Message: "again", Model: "qwen-coder-7b", ProjectPath: `C:\proj`, LogSessionID: "sc_6"})
	if len(*engineCalls) != 1 {
		t.Fatal("expected one more coder call")
	}
	if !strings.Contains((*engineCalls)[0].Prompt, "hi there") {
		t.Errorf("history recovered from disk must reach the model, prompt: %q", (*engineCalls)[0].Prompt)
	}
}

func TestSendChatMessageLogsUserTurnButNotAFailedReply(t *testing.T) {
	a := isolate(t)
	a.isOnline = false
	a.SendChatMessage(ChatRequest{Message: "hi", Model: "qwen-coder-7b", LogSessionID: "sc_7"})

	s, ok := loadStoredSession("", "sc_7")
	if !ok || len(s.Turns) != 1 || s.Turns[0].Role != "user" {
		t.Fatalf("the user's own words should still be kept even if nothing answered: %+v", s)
	}
}

func TestRecentProjectMemory(t *testing.T) {
	isolate(t)
	if m := recentProjectMemory(`C:\proj`, 15); m != "" {
		t.Errorf("no history yet must yield nothing, got %q", m)
	}

	appendStoredTurn("sc_8", `C:\proj`, "user", "", "first ever message", 0)
	appendStoredTurn("sc_8", `C:\proj`, "assistant", "qwen-coder-7b", "sure, done", 0)
	mem := recentProjectMemory(`C:\proj`, 15)
	if !strings.Contains(mem, "first ever message") || !strings.Contains(mem, "qwen-coder-7b") {
		t.Errorf("memory must contain earlier turns: %q", mem)
	}

	// A later, unrelated session in the SAME project must see the earlier one's memory
	mem2 := recentProjectMemory(`C:\proj`, 15)
	if mem2 != mem {
		t.Errorf("memory is per-project, not per-session: %q vs %q", mem, mem2)
	}

	// A different project must not see this project's memory
	if m := recentProjectMemory(`C:\other-proj`, 15); strings.Contains(m, "first ever message") {
		t.Errorf("memory leaked across projects: %q", m)
	}

	// maxLines caps how far back it looks
	for i := 0; i < 30; i++ {
		appendStoredTurn("sc_8", `C:\proj`, "user", "", "filler", 0)
	}
	if got := strings.Count(recentProjectMemory(`C:\proj`, 5), "\n"); got > 4 {
		t.Errorf("expected at most 5 lines (4 newlines), got %d newlines", got)
	}
}

func TestLocalSystemPromptIncludesProjectMemory(t *testing.T) {
	isolate(t)
	appendStoredTurn("sc_9", `C:\proj`, "user", "", "we agreed to use Postgres", 0)
	appendStoredTurn("sc_9", `C:\proj`, "assistant", "qwen-coder-7b", "noted, using Postgres", 0)

	prompt := localSystemPrompt(`C:\proj`)
	if !strings.Contains(prompt, "we agreed to use Postgres") {
		t.Errorf("system prompt must include memory from earlier sessions: %q", prompt)
	}

	// No project selected: no memory section, and no false claim that commands can be run
	if p := localSystemPrompt(""); strings.Contains(p, "Postgres") {
		t.Errorf("no project selected must not include any project's memory: %q", p)
	}
}

func TestSidebarListsAndDeletesNonGrokSessions(t *testing.T) {
	isolate(t)
	home := t.TempDir() // no ~/.grok here, so only our own stored sessions can appear
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	appendStoredTurn("session_1", `C:\proj`, "user", "", "สร้างหน้า login", 0)
	appendStoredTurn("session_1", `C:\proj`, "assistant", "qwen-coder-7b", "เสร็จแล้ว", 0)
	appendStoredTurn("session_2", `C:\other`, "user", "", "another project", 0)

	a := &App{}
	got := a.GetGrokSessions(`C:\proj`)
	ids := map[string]string{}
	for _, s := range got {
		ids[s.ID] = s.Title
	}
	if ids["session_1"] != "สร้างหน้า login" {
		t.Errorf("a Qwen-only chat must appear in the sidebar with its title, got %+v", got)
	}
	if _, ok := ids["session_2"]; !ok {
		t.Errorf("chats from other projects are currently listed too, got %+v", got)
	}

	if !a.DeleteGrokSession(`C:\proj`, "session_1") {
		t.Error("deleting a stored session must report success")
	}
	if _, ok := loadStoredSession(`C:\proj`, "session_1"); ok {
		t.Error("deleted session must be gone from disk")
	}
	if _, ok := loadStoredSession(`C:\other`, "session_2"); !ok {
		t.Error("deleting one session must not touch another")
	}
}
