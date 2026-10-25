package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// StoredTurn is one persisted message in a durable, backend-owned conversation log. It is kept
// independently of any single engine's own storage (e.g. Grok's native session files), so
// history survives switching models mid-conversation and surviving an app restart, even for
// engines (Qwen, CLI agents) that never write anything to disk themselves.
type StoredTurn struct {
	Role      string `json:"role"`            // "user" | "assistant"
	Model     string `json:"model,omitempty"` // which engine answered; empty for user turns
	Content   string `json:"content"`
	Images    int    `json:"images,omitempty"`
	Lang      string `json:"lang,omitempty"` // best-effort tag (e.g. "th", "en"), label only
	Timestamp string `json:"timestamp"`      // RFC3339
}

// StoredSession is the durable record for one conversation ("chat memory": the full, replayable
// turn-by-turn transcript). One JSON file per session, nested under its project — see
// projectDataDir. Kept deliberately separate from the project's summary.md (see
// appendSummaryLine): this file is what a model reads back in for context; summary.md is what a
// human (or another AI, at a glance) skims to see what happened in a project over time.
type StoredSession struct {
	ID          string       `json:"id"`
	ProjectPath string       `json:"projectPath"`
	Title       string       `json:"title"`
	CreatedAt   string       `json:"createdAt"`
	UpdatedAt   string       `json:"updatedAt"`
	Turns       []StoredTurn `json:"turns"`
}

const (
	storeMaxTurns      = 200  // per session; oldest turns are dropped once exceeded
	storeTurnPreview   = 4000 // characters kept per turn in chat memory (sessions/<id>.json)
	summaryLinePreview = 160  // characters kept per line in the summary log (summary.md)
)

// unassignedProjectSlug buckets sessions started before any project folder was selected
const unassignedProjectSlug = "_unassigned"

// projectSlug turns a project path into a stable, filesystem-safe directory name: the folder's
// own name (for readability) plus a short hash of the full path (so two projects that happen to
// share a folder name, e.g. two different "frontend" checkouts, never collide).
func projectSlug(projectPath string) string {
	p := strings.TrimSpace(projectPath)
	if p == "" {
		return unassignedProjectSlug
	}
	clean := filepath.Clean(p)
	base := sessionIDSafe.ReplaceAllString(strings.ToLower(filepath.Base(clean)), "_")
	if base == "" || base == "." {
		base = "project"
	}
	sum := sha1.Sum([]byte(strings.ToLower(clean))) // Windows paths are case-insensitive
	return base + "-" + hex.EncodeToString(sum[:])[:8]
}

// projectDataDir is where everything Sent-Code durably remembers about one project lives —
// nested behind the project itself, not in one flat global bucket: session transcripts under
// sessions/, and the project's own running summary.md beside them.
func projectDataDir(projectPath string) string {
	d := filepath.Join(sentDataDir(), "projects", projectSlug(projectPath))
	_ = os.MkdirAll(d, 0755)
	return d
}

func sessionsDir(projectPath string) string {
	d := filepath.Join(projectDataDir(projectPath), "sessions")
	_ = os.MkdirAll(d, 0755)
	return d
}

// sessionIDSafe keeps session ids (and project folder names) safe to use as filenames. Grok's
// ids and our own "session_..." ids are already filename-safe; this defends against anything
// unexpected reaching here, and keeps the result to a single path segment (no "/", "\", "..").
var sessionIDSafe = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func sessionLogPath(projectPath, id string) string {
	return filepath.Join(sessionsDir(projectPath), sessionIDSafe.ReplaceAllString(id, "_")+".json")
}

func summaryLogPath(projectPath string) string {
	return filepath.Join(projectDataDir(projectPath), "summary.md")
}

// sessionFileMu serializes all reads/writes to session log and summary files. Turns arrive one
// HTTP/IPC call at a time in practice, so a single global lock is simple and cheap enough.
var sessionFileMu sync.Mutex

func loadStoredSession(projectPath, id string) (StoredSession, bool) {
	b, err := os.ReadFile(sessionLogPath(projectPath, id))
	if err != nil {
		return StoredSession{}, false
	}
	var s StoredSession
	if json.Unmarshal(b, &s) != nil {
		return StoredSession{}, false
	}
	return s, true
}

// saveStoredSession writes via a temp file + rename so a crash mid-write never corrupts the log.
func saveStoredSession(s StoredSession) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := sessionLogPath(s.ProjectPath, s.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// detectLang is a coarse, dependency-free script guess used only to label the durable log for
// quick skimming; it never affects what is actually sent to a model.
func detectLang(s string) string {
	for _, r := range s {
		switch {
		case r >= 0x0E00 && r <= 0x0E7F:
			return "th"
		case unicode.Is(unicode.Han, r):
			return "zh"
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			return "ja"
		case unicode.Is(unicode.Hangul, r):
			return "ko"
		}
	}
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return "en"
}

// appendStoredTurn durably logs one message: it appends to that project's chat memory
// (sessions/<id>.json, the full transcript a model reads back for context) and, separately, one
// line to that project's summary.md (a short, skimmable, English-labeled running log — never fed
// back into a model, just for a human or another AI to see at a glance what happened and when).
// It is best-effort: a failure to persist never fails or slows down the chat turn itself, only
// logging a warning to stderr.
func appendStoredTurn(sessionID, projectPath, role, model, content string, images int) {
	content = strings.TrimSpace(content)
	if sessionID == "" || content == "" {
		return
	}
	sessionFileMu.Lock()
	defer sessionFileMu.Unlock()

	s, ok := loadStoredSession(projectPath, sessionID)
	now := time.Now().UTC().Format(time.RFC3339)
	if !ok {
		s = StoredSession{ID: sessionID, ProjectPath: projectPath, CreatedAt: now}
	}
	if projectPath != "" {
		s.ProjectPath = projectPath
	}

	c := content
	if r := []rune(c); len(r) > storeTurnPreview {
		c = string(r[:storeTurnPreview])
	}
	turn := StoredTurn{Role: role, Model: model, Content: c, Images: images, Lang: detectLang(c), Timestamp: now}
	s.Turns = append(s.Turns, turn)
	if len(s.Turns) > storeMaxTurns {
		s.Turns = s.Turns[len(s.Turns)-storeMaxTurns:]
	}
	if s.Title == "" && role == "user" {
		s.Title = titleFrom(c)
	}
	s.UpdatedAt = now

	if err := saveStoredSession(s); err != nil {
		fmt.Fprintln(os.Stderr, "session log write failed:", err)
	}
	appendSummaryLine(s.ProjectPath, turn)
}

// appendSummaryLine appends one line to the project's summary.md — a separate, append-only,
// human-and-AI-skimmable log distinct from the full chat memory in sessions/*.json. Labels
// (role/model/language tag) are in English regardless of the conversation's own language, so the
// log itself reads consistently; the quoted preview keeps the user's original words verbatim.
func appendSummaryLine(projectPath string, t StoredTurn) {
	who := "User"
	if t.Role == "assistant" {
		who = "Assistant"
		if t.Model != "" {
			who += " (" + t.Model + ")"
		}
	}
	preview := strings.Join(strings.Fields(t.Content), " ") // collapse newlines for a single log line
	if r := []rune(preview); len(r) > summaryLinePreview {
		preview = string(r[:summaryLinePreview]) + "..."
	}
	imgNote := ""
	if t.Images > 0 {
		imgNote = fmt.Sprintf(" [%d image%s attached]", t.Images, pluralInt(t.Images))
	}
	lang := t.Lang
	if lang == "" {
		lang = "?"
	}
	line := fmt.Sprintf("- %s | %s | lang=%s%s: %s\n", t.Timestamp, who, lang, imgNote, preview)

	path := summaryLogPath(projectPath)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "summary log write failed:", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		fmt.Fprintln(os.Stderr, "summary log write failed:", err)
	}
}

func titleFrom(s string) string {
	t := strings.TrimSpace(s)
	if r := []rune(t); len(r) > 60 {
		t = string(r[:60]) + "..."
	}
	return t
}

// storedHistoryFallback rebuilds ChatTurn history from a project's chat memory for when the
// caller sent none — e.g. right after an app restart, before the frontend's own in-memory state
// exists again.
func storedHistoryFallback(projectPath, sessionID string) []ChatTurn {
	sessionFileMu.Lock()
	s, ok := loadStoredSession(projectPath, sessionID)
	sessionFileMu.Unlock()
	if !ok {
		return nil
	}
	turns := s.Turns
	if len(turns) > maxHistoryTurns {
		turns = turns[len(turns)-maxHistoryTurns:]
	}
	out := make([]ChatTurn, 0, len(turns))
	for _, t := range turns {
		out = append(out, ChatTurn{Role: t.Role, Content: t.Content, Model: t.Model})
	}
	return out
}

// recentProjectMemory returns the tail of a project's summary.md — its running log across every
// session on that project, not just the current one — so a model gets real memory of what
// happened here before, beyond the current conversation's own (much shorter) history window.
func recentProjectMemory(projectPath string, maxLines int) string {
	sessionFileMu.Lock()
	data, err := os.ReadFile(summaryLogPath(projectPath))
	sessionFileMu.Unlock()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := strings.Join(lines, "\n")
	const maxChars = 2000
	if r := []rune(out); len(r) > maxChars {
		out = string(r[len(r)-maxChars:])
	}
	return strings.TrimSpace(out)
}
