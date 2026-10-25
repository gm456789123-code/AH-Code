package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Model kinds
const (
	KindOllama = "ollama" // weights pulled from the Ollama registry and served by our sidecar
	KindCLI    = "cli"    // any external terminal AI (Gemini CLI, Aider, ...) driven as a subprocess
)

// ModelDef is one selectable model/engine in the registry
type ModelDef struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Kind           string   `json:"kind"`
	OllamaTag      string   `json:"ollamaTag,omitempty"` // kind=ollama, e.g. "qwen2.5-coder:7b"
	SizeHint       string   `json:"sizeHint,omitempty"`  // shown before download, e.g. "~4.7 GB"
	SupportsVision bool     `json:"supportsVision"`      // whether attached images can be forwarded
	Command        string   `json:"command,omitempty"`   // kind=cli: executable name or absolute path
	Args           []string `json:"args,omitempty"`      // kind=cli: arguments, "{prompt}" is replaced by the message
	Builtin        bool     `json:"builtin"`
}

// ModelStatus is a ModelDef plus live install state
type ModelStatus struct {
	ModelDef
	Installed  bool `json:"installed"`
	Installing bool `json:"installing"`
}

// ModelProgress is emitted as "model-install-progress"
type ModelProgress struct {
	ID        string  `json:"id"`
	Stage     string  `json:"stage"` // "pulling", "verifying", "done", "error", "cancelled"
	Message   string  `json:"message"`
	Percent   float64 `json:"percent"`
	Completed int64   `json:"completed"`
	Total     int64   `json:"total"`
}

// builtinModels ship with the app and cannot be removed from the registry
var builtinModels = []ModelDef{
	{
		ID:          "qwen-coder-7b",
		Name:        "Qwen-Coder 7B",
		Description: "Qwen2.5-Coder 7B. Balanced & accurate for complex code.",
		Kind:        KindOllama,
		OllamaTag:   "qwen2.5-coder:7b",
		SizeHint:    "~4.4 GB",
		Builtin:     true,
	},
	{
		ID:          "qwen-coder-14b",
		Name:        "Qwen-Coder 14B",
		Description: "Qwen2.5-Coder 14B. High reasoning intelligence for large tasks.",
		Kind:        KindOllama,
		OllamaTag:   "qwen2.5-coder:14b",
		SizeHint:    "~9.0 GB",
		Builtin:     true,
	},
	{
		ID:          "qwen-coder-32b",
		Name:        "Qwen-Coder 32B",
		Description: "Qwen2.5-Coder 32B. Expert heavyweight coding intelligence.",
		Kind:        KindOllama,
		OllamaTag:   "qwen2.5-coder:32b",
		SizeHint:    "~20 GB",
		Builtin:     true,
	},
	// Claude Code, as four reasoning-effort tiers (verified against a real `claude` 2.1.284
	// install: `claude --help` documents --effort as accepting low/medium/high/xhigh/max; xhigh
	// is skipped here to land on exactly four tiers matching the Low/Medium/High/Ultra naming
	// used elsewhere in this UI). Print mode (-p) replies once and exits instead of opening the
	// interactive REPL; on Windows this resolves to a .cmd shim, which chatCLI already knows to
	// drive over stdin instead of argv — and Claude Code's own -p flag reads the prompt from
	// stdin in that case too (both confirmed working end-to-end, see live_claude_test.go).
	//
	// --permission-prompts none: there is no TTY in this headless context to answer a permission
	// prompt, so anything that would need approval is declined automatically instead of hanging
	// forever waiting for an answer that can never come. This is deliberately NOT the same as
	// --dangerously-skip-permissions / --permission-mode bypassPermissions: those let Claude Code
	// perform the action anyway with nobody watching. These are builtin entries active by default
	// for every user, so we don't make that call on their behalf; a user who wants that trade-off
	// can opt in themselves via Add Model.
	{
		ID:          "claude-code-low",
		Name:        "Claude Code — Low",
		Description: "Fastest, lightest reasoning effort. Good for quick edits and simple questions.",
		Kind:        KindCLI,
		Command:     "claude",
		Args:        []string{"-p", "{prompt}", "--effort", "low", "--permission-prompts", "none"},
		Builtin:     true,
	},
	{
		ID:          "claude-code-medium",
		Name:        "Claude Code — Medium",
		Description: "Balanced effort for everyday coding.",
		Kind:        KindCLI,
		Command:     "claude",
		Args:        []string{"-p", "{prompt}", "--effort", "medium", "--permission-prompts", "none"},
		Builtin:     true,
	},
	{
		ID:          "claude-code-high",
		Name:        "Claude Code — High",
		Description: "Deeper reasoning for harder problems.",
		Kind:        KindCLI,
		Command:     "claude",
		Args:        []string{"-p", "{prompt}", "--effort", "high", "--permission-prompts", "none"},
		Builtin:     true,
	},
	{
		ID:          "claude-code-ultra",
		Name:        "Claude Code — Ultra",
		Description: "Maximum reasoning effort for the hardest, most architectural problems.",
		Kind:        KindCLI,
		Command:     "claude",
		Args:        []string{"-p", "{prompt}", "--effort", "max", "--permission-prompts", "none"},
		Builtin:     true,
	},
}

var (
	ollamaTagRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\-/:]{0,199}$`)
	slugRe      = regexp.MustCompile(`[^a-z0-9]+`)
)

type modelRegistry struct {
	mu      sync.Mutex
	custom  []ModelDef
	loaded  bool
	pulling map[string]context.CancelFunc
}

func registryPath() string { return filepath.Join(sentDataDir(), "models.json") }

func (r *modelRegistry) loadLocked() {
	if r.loaded {
		return
	}
	r.loaded = true
	if r.pulling == nil {
		r.pulling = map[string]context.CancelFunc{}
	}
	if b, err := os.ReadFile(registryPath()); err == nil {
		_ = json.Unmarshal(b, &r.custom)
	}
	// Custom entries must never claim to be built-in
	for i := range r.custom {
		r.custom[i].Builtin = false
	}
}

func (r *modelRegistry) saveLocked() error {
	b, err := json.MarshalIndent(r.custom, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(registryPath(), b, 0644)
}

// find returns the definition for id (built-in or custom)
func (a *App) findModel(id string) (ModelDef, bool) {
	if id == "qwen-coder" || id == "sent-code-3b" || id == "qwen-coder-3b" {
		id = "qwen-coder-7b"
	}
	for _, m := range builtinModels {
		if m.ID == id {
			return m, true
		}
	}
	a.models.mu.Lock()
	defer a.models.mu.Unlock()
	a.models.loadLocked()
	for _, m := range a.models.custom {
		if m.ID == id {
			return m, true
		}
	}
	return ModelDef{}, false
}

// ollamaManifestPaths returns candidate paths for an Ollama model's manifest on disk
func ollamaManifestPaths(tag string) []string {
	name, ver := tag, "latest"
	if i := strings.LastIndex(tag, ":"); i > strings.LastIndex(tag, "/") {
		name, ver = tag[:i], tag[i+1:]
	}
	parts := strings.Split(name, "/")
	host, ns := "registry.ollama.ai", "library"
	switch len(parts) {
	case 1:
		name = parts[0]
	case 2:
		ns, name = parts[0], parts[1]
	default:
		host, ns, name = parts[0], parts[1], strings.Join(parts[2:], "/")
	}
	rel := filepath.Join("manifests", host, ns, name, ver)
	var paths []string
	paths = append(paths, filepath.Join(modelsDir(), rel))

	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".ollama", "models", rel))
	}
	if env := os.Getenv("OLLAMA_MODELS"); env != "" {
		paths = append(paths, filepath.Join(env, rel))
	}
	return paths
}

// ollamaManifestPath returns the primary manifest path in Sent-Code store
func ollamaManifestPath(tag string) string {
	paths := ollamaManifestPaths(tag)
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

// resolveCLI finds the executable for a CLI model
func resolveCLI(command string) (string, bool) {
	if command == "" {
		return "", false
	}
	if filepath.IsAbs(command) {
		if st, err := os.Stat(command); err == nil && !st.IsDir() {
			return command, true
		}
		return "", false
	}
	if p, err := exec.LookPath(command); err == nil {
		return p, true
	}
	return "", false
}

// isInstalled reports whether the model can be used right now (weights on disk / command resolvable / remote server)
func isInstalled(m ModelDef) bool {
	switch m.Kind {
	case KindOllama:
		if getActiveRemoteOllamaURL() != "" {
			return true
		}
		for _, p := range ollamaManifestPaths(m.OllamaTag) {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
		return false
	case KindCLI:
		_, ok := resolveCLI(m.Command)
		return ok
	}
	return false
}

func (a *App) statusOf(m ModelDef) ModelStatus {
	st := ModelStatus{ModelDef: m, Installed: isInstalled(m)}
	a.models.mu.Lock()
	_, st.Installing = a.models.pulling[m.ID]
	a.models.mu.Unlock()
	return st
}

// ListModels returns every registered model with its install state
func (a *App) ListModels() []ModelStatus {
	a.models.mu.Lock()
	a.models.loadLocked()
	all := append([]ModelDef{}, builtinModels...)
	all = append(all, a.models.custom...)
	a.models.mu.Unlock()

	out := make([]ModelStatus, 0, len(all))
	for _, m := range all {
		out = append(out, a.statusOf(m))
	}
	return out
}

// AddModel registers a user-supplied model. Kind "ollama" needs OllamaTag; kind "cli" needs Command.
func (a *App) AddModel(def ModelDef) (ModelStatus, error) {
	def.Name = strings.TrimSpace(def.Name)
	def.Description = strings.TrimSpace(def.Description)
	def.Builtin = false
	if def.Name == "" || len([]rune(def.Name)) > 60 {
		return ModelStatus{}, fmt.Errorf("give the model a name (max 60 characters)")
	}

	switch def.Kind {
	case KindOllama:
		def.OllamaTag = strings.TrimSpace(def.OllamaTag)
		if !ollamaTagRe.MatchString(def.OllamaTag) {
			return ModelStatus{}, fmt.Errorf("model tag looks invalid (example: qwen2.5-coder:14b)")
		}
		def.Command, def.Args = "", nil
	case KindCLI:
		def.Command = strings.TrimSpace(def.Command)
		if def.Command == "" {
			return ModelStatus{}, fmt.Errorf("enter the command that starts the AI (example: gemini)")
		}
		def.OllamaTag = ""
		def.SupportsVision = false
		var args []string
		for _, s := range def.Args {
			if strings.TrimSpace(s) != "" {
				args = append(args, s)
			}
		}
		def.Args = args
	default:
		return ModelStatus{}, fmt.Errorf("unknown model type")
	}

	a.models.mu.Lock()
	defer a.models.mu.Unlock()
	a.models.loadLocked()

	base := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(def.Name), "-"), "-")
	if base == "" {
		base = "model"
	}
	id := "custom-" + base
	taken := func(candidate string) bool {
		for _, m := range builtinModels {
			if m.ID == candidate {
				return true
			}
		}
		for _, m := range a.models.custom {
			if m.ID == candidate {
				return true
			}
		}
		return false
	}
	for n := 2; taken(id); n++ {
		id = fmt.Sprintf("custom-%s-%d", base, n)
	}
	def.ID = id

	a.models.custom = append(a.models.custom, def)
	if err := a.models.saveLocked(); err != nil {
		a.models.custom = a.models.custom[:len(a.models.custom)-1]
		return ModelStatus{}, err
	}
	return ModelStatus{ModelDef: def, Installed: isInstalled(def)}, nil
}

// RemoveModel unregisters a custom model (built-ins cannot be removed). Downloaded weights are kept;
// use UninstallModel first to free disk space.
func (a *App) RemoveModel(id string) error {
	a.models.mu.Lock()
	defer a.models.mu.Unlock()
	a.models.loadLocked()
	for i, m := range a.models.custom {
		if m.ID == id {
			if _, busy := a.models.pulling[id]; busy {
				return fmt.Errorf("model is downloading; cancel the download first")
			}
			a.models.custom = append(a.models.custom[:i], a.models.custom[i+1:]...)
			return a.models.saveLocked()
		}
	}
	return fmt.Errorf("model not found")
}

func (a *App) emitModel(p ModelProgress) {
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "model-install-progress", p)
	}
}

// InstallModel downloads an Ollama-kind model in the background. Progress arrives as "model-install-progress".
// An interrupted download resumes from where it stopped the next time.
func (a *App) InstallModel(id string) error {
	def, ok := a.findModel(id)
	if !ok || def.Kind != KindOllama {
		return fmt.Errorf("this model does not need a download")
	}
	if _, err := os.Stat(ollamaBinary()); err != nil {
		return fmt.Errorf("install the local engine first")
	}

	a.models.mu.Lock()
	a.models.loadLocked()
	if _, busy := a.models.pulling[id]; busy {
		a.models.mu.Unlock()
		return fmt.Errorf("already downloading")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.models.pulling[id] = cancel
	a.models.mu.Unlock()

	go func() {
		err := a.pullModel(ctx, def)
		a.models.mu.Lock()
		delete(a.models.pulling, id)
		a.models.mu.Unlock()
		cancel()

		switch {
		case err == nil:
			a.emitModel(ModelProgress{ID: id, Stage: "done", Percent: 100, Message: "Installed"})
		case ctx.Err() != nil:
			a.emitModel(ModelProgress{ID: id, Stage: "cancelled", Message: "Download paused"})
		default:
			fmt.Fprintln(os.Stderr, "model install failed:", err)
			a.emitModel(ModelProgress{ID: id, Stage: "error", Message: maskError(err)})
		}
	}()
	return nil
}

// CancelModelInstall stops a running download (partial data is kept for resume)
func (a *App) CancelModelInstall(id string) bool {
	a.models.mu.Lock()
	defer a.models.mu.Unlock()
	if c, ok := a.models.pulling[id]; ok {
		c()
		return true
	}
	return false
}

// A download that shows no sign of life for this long is treated as stalled and retried.
// Already-downloaded parts are kept by the engine, so a retry continues instead of restarting.
var (
	pullIdleTimeout = 120 * time.Second
	pullRetryDelay  = 2 * time.Second
)

const pullAttempts = 4

var errPullStalled = fmt.Errorf("download stalled")

// fatalPullError reports errors a retry cannot fix (bad tag, no disk space, ...)
func fatalPullError(err error) bool {
	s := strings.ToLower(err.Error())
	for _, k := range []string{"does not exist", "not found", "unauthorized", "denied", "invalid model", "no space", "not enough space"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func (a *App) pullModel(ctx context.Context, def ModelDef) error {
	a.emitModel(ModelProgress{ID: def.ID, Stage: "pulling", Message: "Starting engine..."})
	if err := a.engine.Ensure(ctx); err != nil {
		return err
	}
	var last ModelProgress
	var err error
	for attempt := 1; attempt <= pullAttempts; attempt++ {
		err = a.pullOnce(ctx, def, pullIdleTimeout, &last)
		if err == nil || ctx.Err() != nil || fatalPullError(err) {
			return err
		}
		if attempt < pullAttempts {
			fmt.Fprintf(os.Stderr, "pull attempt %d failed: %v\n", attempt, err)
			retry := last
			retry.Stage, retry.Message = "pulling", fmt.Sprintf("Connection interrupted. Retrying (%d/%d)...", attempt, pullAttempts-1)
			a.emitModel(retry)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pullRetryDelay):
			}
		}
	}
	return err
}

// pullOnce runs a single /api/pull request. last is updated with the latest progress so a retry can keep the bar where it was.
func (a *App) pullOnce(ctx context.Context, def ModelDef, idle time.Duration, last *ModelProgress) error {
	pctx, cancelPull := context.WithCancel(ctx)
	defer cancelPull()
	var stalled atomic.Bool
	watchdog := time.AfterFunc(idle, func() {
		stalled.Store(true)
		cancelPull()
	})
	defer watchdog.Stop()
	fail := func(err error) error {
		if stalled.Load() && ctx.Err() == nil {
			return errPullStalled
		}
		return err
	}

	body, _ := json.Marshal(map[string]any{"model": def.OllamaTag, "stream": true})
	req, _ := http.NewRequestWithContext(pctx, "POST", a.engine.baseURL()+"/api/pull", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("pull returned %d", resp.StatusCode)
	}

	type layer struct{ total, completed int64 }
	layers := map[string]*layer{}
	lastEmit := time.Time{}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		watchdog.Reset(idle)
		var ev struct {
			Status    string `json:"status"`
			Digest    string `json:"digest"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			return fmt.Errorf("%s", ev.Error)
		}
		if ev.Digest != "" && ev.Total > 0 {
			l := layers[ev.Digest]
			if l == nil {
				l = &layer{}
				layers[ev.Digest] = l
			}
			l.total, l.completed = ev.Total, ev.Completed
		}
		var total, completed int64
		for _, l := range layers {
			total += l.total
			completed += l.completed
		}
		pct := 0.0
		if total > 0 {
			pct = float64(completed) * 100 / float64(total)
			if pct > 99 {
				pct = 99 // last percent is reserved for verify + manifest write
			}
		}
		stage := "pulling"
		msg := "Downloading..."
		if strings.HasPrefix(ev.Status, "verifying") || strings.HasPrefix(ev.Status, "writing") {
			stage, msg = "verifying", "Verifying..."
		}
		if ev.Status == "success" {
			return nil
		}
		if stage != "pulling" || time.Since(lastEmit) > 150*time.Millisecond {
			lastEmit = time.Now()
			*last = ModelProgress{ID: def.ID, Stage: stage, Message: msg, Percent: pct, Completed: completed, Total: total}
			a.emitModel(*last)
		}
	}
	if err := sc.Err(); err != nil {
		return fail(err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fail(fmt.Errorf("download ended before completion"))
}

// UninstallModel deletes downloaded weights for an Ollama-kind model
func (a *App) UninstallModel(id string) error {
	def, ok := a.findModel(id)
	if !ok || def.Kind != KindOllama {
		return fmt.Errorf("nothing to uninstall")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.engine.Ensure(ctx); err != nil {
		return fmt.Errorf("%s", maskError(err))
	}
	body, _ := json.Marshal(map[string]any{"model": def.OllamaTag})
	req, _ := http.NewRequestWithContext(ctx, "DELETE", a.engine.baseURL()+"/api/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s", maskError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		return fmt.Errorf("%s", maskError(fmt.Errorf("delete returned %d", resp.StatusCode)))
	}
	return nil
}

// ---- Running a chat turn on a registry model ----

// ChatTurn is one earlier message, sent by the frontend so local models get conversation context
type ChatTurn struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
	// Model is which engine produced an assistant turn (e.g. "grok-auto", "qwen-coder"), so a
	// handoff to a different engine knows what context it is still missing. Empty for user turns
	// and for older history the frontend has not tagged yet.
	Model string `json:"model,omitempty"`
}

const (
	maxHistoryTurns = 12
	maxTurnChars    = 6000
)

func localSystemPrompt(projectDir string) string {
	s := "You are AH-Code AI, an autonomous and intelligent coding partner. Reply in natural, fluent Thai whenever the user writes in Thai. " +
		"Never output Chinese characters (such as 例如, 吗, 的, 比如); always use natural Thai words like 'เช่น' instead of 'เช่น'. " +
		"If the user greets you (e.g. 'ไง', 'สวัสดี', 'hello'), greet them back warmly and politely in Thai, and ask what code or project you can help with. " +
		"Be concise, practical, and action-oriented. You can run terminal commands, inspect project files, and help write and edit code for the user using your tools. " +
		"Before answering or using tools, you may enclose your thought process inside <think>...</think> tags to reason step by step. " +
		"Provide clean, working code in fenced blocks with clear explanations."
	if projectDir != "" {
		s += " The user's project folder is: " + projectDir + "."
		if tree := projectContextSummary(projectDir); tree != "" {
			s += " Here is what is actually in it (top level):\n" + tree
		}
		if mem := recentProjectMemory(projectDir, 15); mem != "" {
			s += "\n\nRecent activity on this project from earlier sessions (background context; " +
				"the current conversation's own turns are given separately below):\n" + mem
		}
		s += localToolInstructions
	} else {
		s += " No project folder is selected, so you cannot run commands right now."
	}
	return s
}

// projectContextSummary lists a project's top-level entries so a model that has no real shell of
// its own (unlike Grok/Claude Code, which run with this folder as their actual working
// directory) still gets real visibility into the project instead of just a path string.
func projectContextSummary(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	count := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" || name == "target" || name == "build" {
			continue
		}
		if count >= 40 {
			fmt.Fprintf(&b, "- ...and more\n")
			break
		}
		if e.IsDir() {
			fmt.Fprintf(&b, "- %s/\n", name)
		} else {
			fmt.Fprintf(&b, "- %s\n", name)
		}
		count++
	}
	return b.String()
}

// saveChatAttachments saves base64 image data into the project's .sentcode/attachments directory
// and returns their absolute paths. If projectDir is empty, the current working directory or temp is used.
func saveChatAttachments(projectDir string, images []string) []string {
	if len(images) == 0 {
		return nil
	}
	baseDir := projectDir
	if baseDir == "" {
		if wd, err := os.Getwd(); err == nil {
			baseDir = wd
		} else {
			baseDir = filepath.Join(os.TempDir(), "sentcode")
		}
	}
	attachDir := filepath.Join(baseDir, ".sentcode", "attachments")
	if err := os.MkdirAll(attachDir, 0755); err != nil {
		return nil
	}

	var saved []string
	now := time.Now().Format("20060102_150405")
	for i, imgURI := range images {
		raw := imgURI
		ext := ".png"
		if commaIdx := strings.Index(imgURI, ","); commaIdx >= 0 {
			header := strings.ToLower(imgURI[:commaIdx])
			if strings.Contains(header, "image/jpeg") || strings.Contains(header, "image/jpg") {
				ext = ".jpg"
			} else if strings.Contains(header, "image/webp") {
				ext = ".webp"
			} else if strings.Contains(header, "image/gif") {
				ext = ".gif"
			}
			raw = imgURI[commaIdx+1:]
		}
		raw = strings.TrimSpace(raw)
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			continue
		}
		filename := fmt.Sprintf("attachment_%s_%d%s", now, i+1, ext)
		fullPath := filepath.Join(attachDir, filename)
		if err := os.WriteFile(fullPath, data, 0644); err == nil {
			saved = append(saved, fullPath)
		}
	}
	return saved
}

// formatAttachmentNote creates a prompt note telling the AI the exact file paths of the attached images.
func formatAttachmentNote(projectDir string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n---\n[Attached Image(s)]:\n")
	for i, p := range paths {
		rel := p
		if projectDir != "" {
			if r, err := filepath.Rel(projectDir, p); err == nil && !strings.HasPrefix(r, "..") {
				rel = filepath.ToSlash(r)
			}
		}
		sb.WriteString(fmt.Sprintf("- Image %d: `%s` (Full path: `%s`)\n", i+1, rel, p))
	}
	sb.WriteString("Note for AI: Please inspect the attached image file(s) above using your file reading tool to understand what the user is showing (UI layout, visual errors, code screenshot, design elements, etc.) and address their request.\n")
	return sb.String()
}

// runRegistryModel executes one chat turn on a non-Grok model and returns the reply.
func (a *App) runRegistryModel(req ChatRequest, def ModelDef, projectDir string) ChatResponse {
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	a.activeChatCmdMu.Lock()
	a.activeChatCancelFn = cancel
	a.activeChatCancel = false
	a.activeChatCmdMu.Unlock()
	defer func() {
		cancel()
		a.activeChatCmdMu.Lock()
		a.activeChatCancelFn = nil
		a.activeChatCmdMu.Unlock()
	}()

	imageCount := len(req.Images)
	if strings.TrimSpace(req.Message) == "" && imageCount > 0 {
		req.Message = "Please inspect and analyze what the attached image shows."
	}

	eyes := ""
	var savedAttachmentPaths []string
	if imageCount > 0 {
		savedAttachmentPaths = saveChatAttachments(projectDir, req.Images)
	}

	var reply string
	var err error

	if imageCount > 0 {
		if def.Kind == KindCLI {
			// CLI agents (such as Claude Code) can natively inspect workspace files using their built-in tools.
			if spec, via, verr := a.seeImages(ctx, req); verr == nil {
				req.Message = composeWithSpec(req.Message, spec)
				eyes = via
			} else {
				eyes = def.Name
			}
			req.Message += formatAttachmentNote(projectDir, savedAttachmentPaths)
			req.Images = nil
		} else if !def.SupportsVision {
			// Non-multimodal local models: convert image to spec via vision pipeline (online Groq or local vision helper)
			if spec, via, verr := a.seeImages(ctx, req); verr == nil {
				req.Message = composeWithSpec(req.Message, spec)
				eyes = via
			} else if ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "vision stage failed:", verr)
			}
			req.Message += formatAttachmentNote(projectDir, savedAttachmentPaths)
			req.Images = nil
		} else {
			// Multimodal model (e.g. Qwen2.5-VL, Moondream)
			eyes = def.Name
			req.Message += formatAttachmentNote(projectDir, savedAttachmentPaths)
		}
	}

	a.emitLiveStep("step-model", "brain", "Reasoning with "+def.Name, "", "running")

	var toolActions []AgentAction
	switch {
	case def.Kind == KindOllama:
		reply, toolActions, err = a.chatOllamaWithTools(ctx, def, req, projectDir)
	case def.Kind == KindCLI:
		reply, err = a.chatCLI(ctx, def, req, projectDir)
	default:
		err = fmt.Errorf("unknown model type")
	}

	a.emitLiveStep("step-model", "brain", "Reasoning with "+def.Name, "", "done")

	a.activeChatCmdMu.Lock()
	stopped := a.activeChatCancel
	a.activeChatCmdMu.Unlock()

	thought := fmt.Sprintf("Thought for %s", time.Since(start).Round(time.Millisecond))

	thinkRe := regexp.MustCompile(`(?s)<(?:think|thought)>(.*?)</(?:think|thought)>`)
	if m := thinkRe.FindStringSubmatch(reply); len(m) > 1 {
		rawThought := strings.TrimSpace(m[1])
		reply = strings.TrimSpace(thinkRe.ReplaceAllString(reply, ""))
		if rawThought != "" {
			a.emitLiveThought(rawThought)
			thought = fmt.Sprintf("%s\n\n%s", thought, rawThought)
		}
	}

	var actions []AgentAction
	if imageCount > 0 && (def.SupportsVision || eyes != "") {
		detail := fmt.Sprintf("(%d attachment)", imageCount)
		if eyes != "" {
			detail = fmt.Sprintf("%s via %s", detail, eyes)
		}
		actions = append(actions, AgentAction{Type: "view_image", Label: "Viewed image", Detail: detail, Icon: "🖼️"})
	}
	actions = append(actions, toolActions...)

	if stopped || ctx.Err() != nil {
		if def.Kind == KindOllama {
			go a.UnloadOllamaModel(def.OllamaTag)
		}
		return ChatResponse{
			Status: "cancelled", Thought: thought, TokensLeft: a.userTokens,
			Actions: []AgentAction{{Type: "status", Label: "Stopped", Icon: "⏹"}},
			Reply:   "(Generation stopped by user)",
		}
	}
	if err != nil {
		if def.Kind == KindOllama && getActiveRemoteOllamaURL() == "" {
			go a.UnloadOllamaModel(def.OllamaTag)
		}
		fmt.Fprintln(os.Stderr, "model run failed:", err)
		a.userTokens++ // nothing was delivered, do not charge
		replyMsg := maskError(err)
		if getActiveRemoteOllamaURL() != "" {
			replyMsg = fmt.Sprintf("⚠️ เกิดข้อผิดพลาดจาก Cloud GPU: %s\n(คำแนะนำ: โปรดตรวจดูว่าหน้า Google Colab ยังคงรันอยู่และไม่ได้หลุดการเชื่อมต่อครับ)", err.Error())
		}
		return ChatResponse{
			Status:       "gateway_error",
			Reply:        replyMsg,
			ErrorMessage: err.Error(),
			TokensLeft:   a.userTokens,
		}
	}

	if imageCount > 0 && !def.SupportsVision && eyes == "" {
		reply = "⚠️ **AI Vision ('ดวงตาเอไอ') ยังไม่พร้อมทำงาน:**\n" +
			"> คุณได้แนบภาพเข้ามา แต่ยังไม่ได้ติดตั้งโมเดลสายตาในเครื่อง หรือยังไม่ได้ระบุ Cloud Vision Key ใน Settings\n\n" +
			"**วิธีเปิดใช้งานสายตาให้เอไอ (เลือกวิธีใดก็ได้):**\n" +
			"1. **แบบออฟไลน์ (ในเครื่อง):** ไปที่เมนูโมเดล -> กดติดตั้ง **Moondream2 (~800MB)** หรือ **Qwen2.5-VL 3B (~1.9GB)**\n" +
			"2. **แบบออนไลน์เร็วสูง (ฟรี):** ไปที่ปุ่ม **Settings** ด้านซ้ายล่าง -> ใส่ฟรี **Groq API Key** (รับฟรีทันทีได้ที่ [console.groq.com](https://console.groq.com/keys))\n" +
			"3. หรือสลับไปใช้โหมด **Grok (Auto / Expert / Heavy)** ซึ่งมีดวงตาระดับ Cloud ประมวลผลภาพในตัวครับ\n\n" +
			"---\n\n" + reply
	}
	actions = append(actions, AgentAction{Type: "status", Label: "Finished turn", Icon: "✓"})
	resp := ChatResponse{Status: "ok", Thought: thought, Actions: actions, Reply: reply, TokensLeft: a.userTokens}

	hasCodeModifications := false
	for _, act := range actions {
		if (act.Type == "edit_file" && act.Icon == "edit") || strings.Contains(strings.ToLower(act.Label), "edit") || strings.Contains(strings.ToLower(act.Label), "saved") {
			hasCodeModifications = true
			break
		}
	}

	if projectDir != "" && hasCodeModifications {
		resp.DiffStats = computeWorkspaceDiff(projectDir)
	}
	if def.Kind == KindOllama {
		go a.UnloadOllamaModel(def.OllamaTag)
	}
	return resp
}

// UnloadOllamaModel sends a keep_alive=0 request to immediately evict the model from VRAM/GPU
func (a *App) UnloadOllamaModel(tag string) {
	if tag == "" {
		tag = "qwen2.5-coder:7b"
	}
	body, _ := json.Marshal(map[string]any{
		"model":      tag,
		"keep_alive": 0,
	})
	req, err := http.NewRequest("POST", a.engine.baseURL()+"/api/generate", bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 3 * time.Second}
		resp, rerr := client.Do(req)
		if rerr == nil {
			resp.Body.Close()
		}
	}
}

// seeImages returns a technical spec for the attached images and who produced it.
// Order: web gateway (Groq) first, then an installed local vision model as the offline fallback.
func (a *App) seeImages(ctx context.Context, req ChatRequest) (spec, via string, err error) {
	if a.isOnline {
		if spec, err = a.gatewayVision(ctx, req); err == nil {
			return spec, "Vision Gateway", nil
		}
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
	} else {
		err = fmt.Errorf("offline")
	}
	if vd, ok := a.findVisionHelper(); ok {
		if spec, lerr := a.describeImages(ctx, vd, req); lerr == nil {
			return spec, vd.Name, nil
		}
	}
	return "", "", err
}

// findVisionHelper returns an installed vision-capable Ollama model the user added themselves.
// It is only the offline fallback: the web gateway is the primary "eyes".
func (a *App) findVisionHelper() (ModelDef, bool) {
	for _, m := range a.ListModels() {
		if m.Kind == KindOllama && m.SupportsVision && m.Installed {
			return m.ModelDef, true
		}
	}
	return ModelDef{}, false
}

const visionSystemPrompt = "You are the eyes of a coding assistant. You never write the final code yourself. " +
	"Study the attached image(s) and produce a precise technical specification another model can implement from, in plain text:\n" +
	"1. What the image is (UI screenshot, design mockup, error message, terminal output, diagram, code).\n" +
	"2. Layout: regions and their arrangement, alignment, spacing, sizes where visible.\n" +
	"3. Components: every button, input, list, card, icon, with their exact visible text.\n" +
	"4. Style: dominant colors as approximate hex values, fonts, borders, radius, shadows, light/dark theme.\n" +
	"5. Any error, warning or log text, transcribed verbatim, with file names and line numbers.\n" +
	"6. Anything in the image that looks wrong or relevant to the user's request.\n" +
	"Be factual: describe only what is visible and do not invent details. Reply in English."

// describeImages runs the vision model over the attached images and returns a structured spec.
// The model is unloaded straight afterwards so a small GPU has room to load the coder next.
func (a *App) describeImages(ctx context.Context, vd ModelDef, req ChatRequest) (string, error) {
	if err := a.engine.Ensure(ctx); err != nil {
		return "", err
	}
	ask := strings.TrimSpace(req.Message)
	if ask == "" {
		ask = "Describe the image(s) for a developer."
	}
	msgs := []ollamaMsg{
		{Role: "system", Content: visionSystemPrompt},
		{Role: "user", Content: "The user's request about this image: " + ask, Images: rawImages(req.Images)},
	}
	spec, err := a.ollamaChat(ctx, vd.OllamaTag, msgs, 0)
	if err != nil {
		return "", err
	}
	if spec == "" {
		return "", fmt.Errorf("vision model returned nothing")
	}
	return spec, nil
}

// composeWithSpec merges the user's words with the vision model's spec into one prompt for the coder
func composeWithSpec(userMsg, spec string) string {
	userMsg = strings.TrimSpace(userMsg)
	if userMsg == "" {
		userMsg = "Implement or fix what the attached image shows."
	}
	return userMsg + "\n\n---\nThe user attached image(s). You cannot see them; a vision model analyzed them and produced this specification. Treat it as ground truth about the image:\n\n" + spec
}

const (
	maxToolIterations = 4      // how many tool calls a single turn may make before we stop and just answer
	runCommandPrefix  = "RUN:" // the exact marker a model uses to ask for a command
	searchPrefix      = "SEARCH:"
	readFilePrefix    = "READ:"
	writeFilePrefix   = "WRITE:"
)

// toolCommandTimeout caps how long one model-requested command may run; a var (not a const) so
// tests can shrink it instead of waiting out the real 20s.
var toolCommandTimeout = 20 * time.Second

// localToolInstructions gives Ollama-kind models (which, unlike Grok/Claude Code, have no shell
// or browsing of their own) proactive tools to inspect and modify code.
const localToolInstructions = "\n\nYou have active tools to help the user. Use them proactively to inspect files, edit code, or run commands:\n" +
	"- READ: <file_path> — reads the content of a file in the project folder.\n" +
	"- WRITE: <file_path>\n<content> — writes new content to a file in the project folder.\n" +
	"- REPLACE: <file_path>\n<<<<<<< SEARCH\nexact old text\n=======\nnew text\n>>>>>>> REPLACE — edits part of an existing file (prefer over WRITE for small changes; old text must match exactly once).\n" +
	"- RUN: <command> — executes a terminal command directly in the project folder.\n" +
	"- SEARCH: <query> — searches the live web for official documentation or solutions.\n" +
	"\nCRITICAL RULES:\n" +
	"1. You HAVE FULL ACCESS and permission to read and inspect all files in the user's project directory.\n" +
	"2. NEVER say 'ฉันไม่มีความสามารถในการอ่านไฟล์' or 'ฉันไม่สามารถเข้าถึงระบบของคุณได้' (Never claim you cannot access or read files).\n" +
	"3. When the user asks to see code or inspect the project, call `READ: <filename>` immediately or use `RUN: dir` or `RUN: ls` to see what files exist, then answer with the real code!\n" +
	"When you need to use a tool, reply with ONLY the tool call line(s) and nothing else. You will then receive the real output and can provide the final answer.\n" +
	"Example:\nUser: อ่านไฟล์ app.go หน่อย\nAssistant: READ: app.go\n"

// toolKind identifies which tool a model asked for
type toolKind int

const (
	toolNone toolKind = iota
	toolRun
	toolSearch
	toolRead
	toolWrite
	toolReplace
)

// parseToolCall reports whether reply is a tool-call
func parseToolCall(reply string) (kind toolKind, arg string, ok bool) {
	t := strings.TrimSpace(reply)
	for prefix, k := range map[string]toolKind{
		runCommandPrefix:  toolRun,
		searchPrefix:      toolSearch,
		readFilePrefix:    toolRead,
		writeFilePrefix:   toolWrite,
		replaceFilePrefix: toolReplace,
	} {
		if len(t) <= len(prefix) || !strings.EqualFold(t[:len(prefix)], prefix) {
			continue
		}
		rest := t[len(prefix):]
		if k != toolWrite && k != toolReplace {
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				rest = rest[:i]
			}
		}
		a := strings.TrimSpace(rest)
		if a == "" {
			return toolNone, "", false
		}
		return k, a, true
	}
	return toolNone, "", false
}

// dangerousCommandSubstrings catches recognizably catastrophic commands (not a sandbox — a
// last-resort refusal for the obvious cases) before they're ever run unsupervised.
var dangerousCommandSubstrings = []string{
	"rm -rf /", "rm -rf ~", "rm -rf $home", "rm -rf .. ", "rm -rf ../",
	"mkfs.", ":(){ :|:& };:", "shutdown", "stop-computer", "restart-computer",
}

var driveRootTargetRe = regexp.MustCompile(`\b[a-z]:[\\/]?\s*(["']|$)`)

func isDangerousCommand(cmd string) bool {
	c := strings.ToLower(strings.TrimSpace(cmd))
	for _, s := range dangerousCommandSubstrings {
		if strings.Contains(c, s) {
			return true
		}
	}
	if strings.Contains(c, "dd ") && strings.Contains(c, "of=/dev/") {
		return true
	}
	// deleting/formatting an entire drive root, not a subfolder
	if driveRootTargetRe.MatchString(c) {
		for _, verb := range []string{"del ", "erase ", "remove-item", "rd ", "rmdir", "format "} {
			if strings.Contains(c, verb) {
				return true
			}
		}
	}
	return false
}

// runToolCommand runs one model-requested command in the project folder, capped by a timeout.
func runToolCommand(dir, commandStr string) CommandResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), toolCommandTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", commandStr)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", commandStr)
	}
	hideWindow(cmd)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	duration := time.Since(start).Round(time.Millisecond).String()

	exitCode := 0
	success := err == nil
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		out = append(out, []byte("\n(command timed out after "+toolCommandTimeout.String()+" and was stopped)")...)
	}
	return CommandResult{Command: commandStr, Stdout: string(out), ExitCode: exitCode, Success: success, Duration: duration}
}

// formatToolOutput turns a command's result into the next message handed back to the model.
func formatToolOutput(res CommandResult) string {
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		out = "(no output)"
	}
	if r := []rune(out); len(r) > 4000 {
		out = string(r[:4000]) + "\n...(truncated)"
	}
	status := "succeeded"
	if !res.Success {
		status = fmt.Sprintf("failed (exit %d)", res.ExitCode)
	}
	return fmt.Sprintf("[Terminal] `%s` %s:\n```\n%s\n```\nContinue, or answer now if you have enough to finish.", res.Command, status, out)
}

// chatOllamaWithTools drives chatOllama in a small ReAct-style loop: if the model asks to run a
// command (see localToolInstructions), it is actually run in the project folder and its real
// output is fed back so the model can use it before giving a final answer. Grok and Claude Code
// don't need this — they already run as real agents with their own shell in that directory. This
// is scoped to when a project folder is selected, capped in iterations, and refuses recognizably
// destructive commands (isDangerousCommand) instead of running them.
func (a *App) chatOllamaWithTools(ctx context.Context, def ModelDef, req ChatRequest, projectDir string) (string, []AgentAction, error) {
	if projectDir == "" {
		reply, err := a.chatOllama(ctx, def, req, projectDir)
		return reply, nil, err
	}

	history := append([]ChatTurn(nil), req.History...)
	message := req.Message
	images := req.Images
	var actions []AgentAction

	for i := 0; ; i++ {
		reply, err := a.chatOllama(ctx, def, ChatRequest{Message: message, History: history, Images: images}, projectDir)
		if err != nil {
			return "", actions, err
		}
		images = nil // only the user's own first turn carries an attachment

		kind, arg, isTool := parseToolCall(reply)
		if !isTool || i >= maxToolIterations {
			return reply, actions, nil
		}

		history = append(history,
			ChatTurn{Role: "user", Content: message},
			ChatTurn{Role: "assistant", Content: reply, Model: def.ID},
		)

		switch kind {
		case toolRun:
			if isDangerousCommand(arg) {
				actions = append(actions, AgentAction{Type: "command", Label: "Refused command", Detail: arg, Icon: "🚫"})
				message = "[Terminal] Refused: this command looks destructive and was not run. " +
					"Explain that to the user instead of retrying it, or suggest a safer alternative."
				continue
			}
			a.emitLiveStep("step-tool-"+arg, "terminal", "Executing terminal command", arg, "running")
			result := runToolCommand(projectDir, arg)
			a.emitLiveStep("step-tool-"+arg, "terminal", "Executed command: "+arg, "", "done")
			actions = append(actions, AgentAction{Type: "command", Label: "Ran command", Detail: arg, Icon: "terminal"})
			message = formatToolOutput(result)

		case toolSearch:
			a.emitLiveStep("step-search-"+arg, "search", "Searching documentation & solutions", arg, "running")
			results, serr := webSearch(ctx, arg)
			if serr != nil {
				a.emitLiveStep("step-search-"+arg, "search", "Search offline", arg, "done")
				actions = append(actions, AgentAction{Type: "search", Label: "Search failed", Detail: arg, Icon: "search"})
				message = "[Search] Could not search the web right now (offline or the search service is unavailable). " +
					"Answer using what you already know, and say you could not verify this online."
			} else {
				a.emitLiveStep("step-search-"+arg, "search", "Searched web: "+arg, "", "done")
				actions = append(actions, AgentAction{Type: "search", Label: "Searched the web", Detail: arg, Icon: "search"})
				message = formatSearchOutput(arg, results)
			}
		case toolRead:
			cleanPath, targetFile, pok := resolveProjectFile(projectDir, arg)
			if !pok {
				actions = append(actions, AgentAction{Type: "read_file", Label: "Refused path", Detail: arg, Icon: "🚫"})
				message = "[File Reader] Refused: `" + arg + "` is outside the project folder or a protected location."
				continue
			}
			a.emitLiveStep("step-read-"+cleanPath, "file", "Reading "+cleanPath, cleanPath, "running")
			contentBytes, rerr := os.ReadFile(targetFile)
			if rerr != nil {
				a.emitLiveStep("step-read-"+cleanPath, "file", "Failed reading "+cleanPath, cleanPath, "done")
				actions = append(actions, AgentAction{Type: "read_file", Label: "Reading " + cleanPath, Detail: cleanPath, Icon: "file"})
				message = fmt.Sprintf("[File Reader] Could not read `%s`: %v", cleanPath, rerr)
			} else {
				a.emitLiveStep("step-read-"+cleanPath, "file", "Reading "+cleanPath, cleanPath, "done")
				actions = append(actions, AgentAction{Type: "read_file", Label: "Reading " + cleanPath, Detail: cleanPath, Icon: "file"})
				preview := string(contentBytes)
				if len([]rune(preview)) > 4000 {
					preview = string([]rune(preview)[:4000]) + "\n...(truncated)"
				}
				message = fmt.Sprintf("[File Content of `%s`]:\n```\n%s\n```\nContinue, or answer now.", cleanPath, preview)
			}

		case toolReplace:
			parts := strings.SplitN(arg, "\n", 2)
			filePath := strings.TrimSpace(parts[0])
			body := ""
			if len(parts) > 1 {
				body = parts[1]
			}
			a.emitLiveStep("step-edit-"+filePath, "edit", "Editing "+filePath, filePath, "running")
			cleanPath, msg, rerr := applyReplace(projectDir, filePath, body)
			if cleanPath == "" {
				cleanPath = filePath
			}
			a.emitLiveStep("step-edit-"+filePath, "edit", "Editing "+cleanPath, cleanPath, "done")
			actions = append(actions, AgentAction{Type: "edit_file", Label: "Editing " + cleanPath, Detail: cleanPath, Icon: "edit"})
			if rerr != nil {
				message = fmt.Sprintf("[File Editor] Could not edit `%s`: %v", cleanPath, rerr)
			} else {
				message = msg
			}

		case toolWrite:
			parts := strings.SplitN(arg, "\n", 2)
			filePath := strings.TrimSpace(parts[0])
			content := ""
			if len(parts) > 1 {
				content = parts[1]
			}
			cleanPath, targetFile, pok := resolveProjectFile(projectDir, filePath)
			if !pok {
				actions = append(actions, AgentAction{Type: "edit_file", Label: "Refused path", Detail: filePath, Icon: "🚫"})
				message = "[File Editor] Refused: `" + filePath + "` is outside the project folder or a protected location."
				continue
			}
			a.emitLiveStep("step-edit-"+cleanPath, "edit", "Editing "+cleanPath, cleanPath, "running")
			_ = os.MkdirAll(filepath.Dir(targetFile), 0755)
			werr := os.WriteFile(targetFile, []byte(content), 0644)
			if werr != nil {
				a.emitLiveStep("step-edit-"+cleanPath, "edit", "Failed editing "+cleanPath, cleanPath, "done")
				actions = append(actions, AgentAction{Type: "edit_file", Label: "Editing " + cleanPath, Detail: cleanPath, Icon: "edit"})
				message = fmt.Sprintf("[File Editor] Could not write `%s`: %v", cleanPath, werr)
			} else {
				a.emitLiveStep("step-edit-"+cleanPath, "edit", "Saved "+cleanPath, cleanPath, "done")
				actions = append(actions, AgentAction{Type: "edit_file", Label: "Editing " + cleanPath, Detail: cleanPath, Icon: "edit"})
				message = fmt.Sprintf("[File Editor] Successfully saved `%s` (%d bytes). Continue, or answer now.", cleanPath, len(content))
			}
		}
	}
}

func (a *App) chatOllama(ctx context.Context, def ModelDef, req ChatRequest, projectDir string) (string, error) {
	if !isInstalled(def) {
		return "", fmt.Errorf("model not installed")
	}
	if getActiveRemoteOllamaURL() == "" {
		if err := a.engine.Ensure(ctx); err != nil {
			return "", err
		}
	}

	msgs := []ollamaMsg{{Role: "system", Content: localSystemPrompt(projectDir)}}

	hist := req.History
	if len(hist) > maxHistoryTurns {
		hist = hist[len(hist)-maxHistoryTurns:]
	}
	for _, h := range hist {
		if (h.Role != "user" && h.Role != "assistant") || strings.TrimSpace(h.Content) == "" {
			continue
		}
		c := h.Content
		if r := []rune(c); len(r) > maxTurnChars {
			c = string(r[:maxTurnChars])
		}
		msgs = append(msgs, ollamaMsg{Role: h.Role, Content: c})
	}

	cur := ollamaMsg{Role: "user", Content: req.Message}
	if def.SupportsVision {
		cur.Images = rawImages(req.Images)
	}
	msgs = append(msgs, cur)

	tag := def.OllamaTag
	if remote := getActiveRemoteOllamaURL(); remote != "" {
		tag = resolveRemoteOllamaTag(remote, tag)
	}
	return a.ollamaChat(ctx, tag, msgs, nil)
}

func resolveRemoteOllamaTag(remoteURL, requested string) string {
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(remoteURL + "/api/tags")
	if err != nil {
		return requested
	}
	defer resp.Body.Close()
	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil || len(tagsResp.Models) == 0 {
		return requested
	}
	for _, m := range tagsResp.Models {
		if m.Name == requested || strings.HasPrefix(m.Name, requested) {
			return m.Name
		}
	}
	for _, m := range tagsResp.Models {
		if strings.Contains(strings.ToLower(m.Name), "qwen") {
			return m.Name
		}
	}
	return tagsResp.Models[0].Name
}

type ollamaMsg struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

// rawImages strips "data:image/...;base64," prefixes; Ollama wants bare base64
func rawImages(uris []string) []string {
	out := make([]string, 0, len(uris))
	for _, uri := range uris {
		if i := strings.Index(uri, ","); strings.HasPrefix(uri, "data:") && i >= 0 {
			uri = uri[i+1:]
		}
		out = append(out, uri)
	}
	return out
}

// ollamaChat formats the prompt as ChatML and sends a non-streaming /api/generate request with raw=true.
// This bypasses Ollama's internal chat template bug where EOS was emitted on token 1.
func (a *App) ollamaChat(ctx context.Context, tag string, msgs []ollamaMsg, keepAlive any) (string, error) {
	var sb strings.Builder
	var images []string
	for _, m := range msgs {
		sb.WriteString("<|im_start|>")
		sb.WriteString(m.Role)
		sb.WriteString("\n")
		sb.WriteString(m.Content)
		sb.WriteString("<|im_end|>\n")
		if len(m.Images) > 0 {
			// /api/generate is single-turn: images are a request-level field, not per-message,
			// so the most recent message carrying any is what the model actually gets to see.
			images = m.Images
		}
	}
	sb.WriteString("<|im_start|>assistant\n")

	if os.Getenv("SENTCODE_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, "---- ollamaChat prompt ----\n"+sb.String()+"----------------------------")
	}

	payload := map[string]any{
		"model":  tag,
		"prompt": sb.String(),
		"raw":    true,
		"stream": false,
		"options": map[string]any{
			"num_ctx":        8192,
			"num_predict":    2048,
			"repeat_penalty": 1.05,
			"repeat_last_n":  64,
			"temperature":    0.2,
			"top_p":          0.9,
			"stop":           []string{"<|im_end|>", "<|endoftext|>", "<|im_start|>"},
		},
	}
	if keepAlive != nil {
		payload["keep_alive"] = keepAlive
	} else if getActiveRemoteOllamaURL() != "" {
		payload["keep_alive"] = "2h"
	} else {
		payload["keep_alive"] = 0
	}
	if len(images) > 0 {
		payload["images"] = images
	}
	body, _ := json.Marshal(payload)
	hreq, _ := http.NewRequestWithContext(ctx, "POST", a.engine.baseURL()+"/api/generate", bytes.NewReader(body))
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode != 200 || out.Error != "" {
		return "", fmt.Errorf("chat failed (%d): %s", resp.StatusCode, out.Error)
	}
	ans := strings.TrimSpace(out.Response)
	ans = strings.TrimSuffix(ans, "<|im_end|>")
	ans = strings.TrimSuffix(ans, "<|endoftext|>")
	return strings.TrimSpace(cleanChineseTokens(ans)), nil
}

func cleanChineseTokens(s string) string {
	replacer := strings.NewReplacer(
		"例如:", "เช่น:",
		"例如：", "เช่น:",
		"例如", "เช่น",
		"比如:", "เช่น:",
		"比如：", "เช่น:",
		"比如", "เช่น",
		"等等", "ฯลฯ",
		"破解", "แคร็ก/ถอดรหัส",
		"密码", "รหัสผ่าน",
		"黑客", "แฮกเกอร์",
		"攻击", "การโจมตี",
		"吗?", "ไหมครับ?",
		"吗？", "ไหมครับ?",
		"好的，", "ได้ครับ ",
		"好的!", "ได้เลยครับ!",
		"好的", "ได้ครับ",
		"是的，", "ใช่ครับ ",
		"是的", "ใช่ครับ",
	)
	return replacer.Replace(s)
}

// chatCLI runs an external terminal AI once for this message, inside the project folder.
// The command is executed directly (never through a shell) so the prompt cannot inject commands.
func (a *App) chatCLI(ctx context.Context, def ModelDef, req ChatRequest, projectDir string) (string, error) {
	bin, ok := resolveCLI(def.Command)
	if !ok {
		return "", fmt.Errorf("command not found: %s", def.Command)
	}

	prompt := req.Message
	if def.Command == "claude" {
		prompt = withModelHandoff(prompt, req.History, isClaudeModel)
	}
	useArg := false
	args := make([]string, len(def.Args))
	for i, s := range def.Args {
		if strings.Contains(s, "{prompt}") {
			useArg = true
		}
		args[i] = s
	}
	// Windows runs .cmd/.bat shims through cmd.exe, which mangles arguments; pass the prompt on stdin instead
	if runtime.GOOS == "windows" {
		if ext := strings.ToLower(filepath.Ext(bin)); ext == ".cmd" || ext == ".bat" {
			useArg = false
		}
	}
	if useArg {
		for i := range args {
			args[i] = strings.ReplaceAll(args[i], "{prompt}", prompt)
		}
	} else {
		kept := args[:0]
		for _, s := range args {
			if !strings.Contains(s, "{prompt}") {
				kept = append(kept, s)
			}
		}
		args = kept
	}

	if def.Command == "claude" {
		if dataDir := sentDataDir(); dataDir != "" {
			args = append(args, "--add-dir", dataDir)
		}
		args = append(args, claudeCodeSessionArgs(req.LogSessionID, projectDir)...)
		args = append(args, "--output-format", "stream-json", "--verbose")
	}
	streamJSON := def.Command == "claude"

	cmd := exec.CommandContext(ctx, bin, args...)
	hideWindow(cmd)
	if projectDir != "" {
		cmd.Dir = projectDir
	}
	if !useArg {
		cmd.Stdin = strings.NewReader(prompt + "\n")
	}

	a.activeChatCmdMu.Lock()
	a.activeChatCmd = cmd
	a.activeChatCmdMu.Unlock()
	defer func() {
		a.activeChatCmdMu.Lock()
		if a.activeChatCmd == cmd {
			a.activeChatCmd = nil
		}
		a.activeChatCmdMu.Unlock()
	}()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var sb strings.Builder
	// stream-json lines carry whole tool results, so allow far longer lines than bufio's 64KB default
	var streamText strings.Builder // plain assistant text, fallback if no final "result" event arrives
	var streamFinal string
	streamHasFinal, streamIsError := false, false
	liveTools := map[string]LiveAgentStep{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()

		if streamJSON {
			u, ok := parseClaudeStreamLine(line)
			if !ok {
				sb.WriteString(line)
				sb.WriteString("\n")
				continue
			}
			for _, s := range u.Steps {
				liveTools[s.ID] = s
				a.emitLiveStep(s.ID, s.Icon, s.Title, s.Detail, "running")
			}
			for _, id := range u.Done {
				if s, ok := liveTools[id]; ok {
					a.emitLiveStep(s.ID, s.Icon, s.Title, s.Detail, "done")
					delete(liveTools, id)
				}
			}
			a.emitLiveThought(u.Thought)
			streamText.WriteString(u.Text)
			if u.HasFinal {
				streamFinal, streamHasFinal, streamIsError = u.Final, true, u.IsError
			}
			continue
		}

		sb.WriteString(line)
		sb.WriteString("\n")

		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "reading ") || strings.HasPrefix(lower, "view ") || strings.HasPrefix(lower, "read ") {
			idx := strings.Index(trimmed, " ")
			if idx != -1 {
				file := strings.TrimSpace(trimmed[idx:])
				clean := cleanProjectDisplayPath(file, projectDir)
				if clean != "" {
					a.emitLiveStep("claude-read-"+clean, "file", "Reading "+clean, clean, "running")
					go func(f string) {
						time.Sleep(400 * time.Millisecond)
						a.emitLiveStep("claude-read-"+f, "file", "Reading "+f, f, "done")
					}(clean)
				}
			}
		} else if strings.HasPrefix(lower, "editing ") || strings.HasPrefix(lower, "edit ") || strings.HasPrefix(lower, "writing ") || strings.HasPrefix(lower, "write ") {
			idx := strings.Index(trimmed, " ")
			if idx != -1 {
				file := strings.TrimSpace(trimmed[idx:])
				clean := cleanProjectDisplayPath(file, projectDir)
				if clean != "" {
					a.emitLiveStep("claude-edit-"+clean, "edit", "Editing "+clean, clean, "running")
					go func(f string) {
						time.Sleep(500 * time.Millisecond)
						a.emitLiveStep("claude-edit-"+f, "edit", "Saved "+f, f, "done")
					}(clean)
				}
			}
		} else if strings.HasPrefix(lower, "bash: ") || strings.HasPrefix(lower, "running ") {
			idx := strings.Index(trimmed, " ")
			if idx != -1 {
				cmdStr := strings.TrimSpace(trimmed[idx:])
				a.emitLiveStep("claude-cmd-"+cmdStr, "terminal", "Executing "+cmdStr, cmdStr, "running")
			}
		} else if strings.HasPrefix(lower, "thought:") || strings.HasPrefix(lower, "thinking:") {
			a.emitLiveThought(trimmed)
		}
	}
	waitErr := cmd.Wait()

	if streamJSON {
		for _, s := range liveTools { // tools still open when the run ended must not spin forever
			a.emitLiveStep(s.ID, s.Icon, s.Title, s.Detail, "done")
		}
		switch {
		case streamHasFinal && streamFinal != "" && !streamIsError:
			return streamFinal, nil
		case streamHasFinal && streamIsError:
			return "", fmt.Errorf("%s", streamFinal)
		case strings.TrimSpace(streamText.String()) != "":
			return strings.TrimSpace(streamText.String()), nil
		}
	}

	text := strings.TrimSpace(sb.String())
	if text != "" {
		return text, nil
	}
	if waitErr != nil {
		return "", waitErr
	}
	return "(The agent finished without any output.)", nil
}
