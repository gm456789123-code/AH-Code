package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx              context.Context
	currentFolder    string
	userTokens       int
	isOnline         bool
	loginCmdMu       sync.Mutex
	loginCmd         *exec.Cmd
	activeChatCmdMu  sync.Mutex
	activeChatCmd    *exec.Cmd
	activeChatCancel bool
	// activeChatCancelFn aborts a running local-model / CLI-agent turn
	activeChatCancelFn context.CancelFunc
	// activeBuildCmd / activeBuildCancel track a running RunBuildCommand so
	// StopActiveChat can abort it too. Guarded by activeChatCmdMu.
	activeBuildCmd    *exec.Cmd
	activeBuildCancel context.CancelFunc
	engine            *Engine
	models            *modelRegistry
}

// ProjectFileItem represents a file or folder in the project
type ProjectFileItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// CommandResult represents the output of a terminal build command
type CommandResult struct {
	Command  string `json:"command"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
	Success  bool   `json:"success"`
	Duration string `json:"duration"`
}

// ChatRequest sent from frontend
type ChatRequest struct {
	Message     string   `json:"message"`
	Model       string   `json:"model"`  // "grok-fast", "grok-auto", "grok-expert", "grok-heavy", "qwen-coder"
	Images      []string `json:"images"` // Base64 data URIs
	ProjectPath string   `json:"projectPath"`
	SessionID   string   `json:"sessionId"` // blank unless it is a Grok-native session id, used for --resume
	// History carries earlier turns for local models (Grok resumes its own session from disk)
	History []ChatTurn `json:"history"`
	// LogSessionID is the frontend's stable id for this conversation regardless of engine (unlike
	// SessionID, it is never blanked out). It keys the durable, backend-owned conversation log
	// this app writes to disk, which survives switching models and restarting the app.
	LogSessionID string `json:"logSessionId"`
}

// AgentAction represents an individual step executed by the agent
type AgentAction struct {
	Type      string `json:"type"`      // "view_image", "read_file", "command", "search", "status"
	Label     string `json:"label"`     // "Viewed image", "Analyzed", "Ran command", "Searched workspace"
	Detail    string `json:"detail"`    // "screenshot.png", "app.go", "npm run build"
	LineRange string `json:"lineRange"` // "#L1-65"
	Icon      string `json:"icon"`      // "🖼️", "⚛", "🐹", "📜", "📋", "🎨", "⚡"
}

// DiffStats represents code modifications made during an agent turn
type DiffStats struct {
	Additions int      `json:"additions"`
	Deletions int      `json:"deletions"`
	Files     []string `json:"files,omitempty"`
	DiffText  string   `json:"diffText,omitempty"`
}

// ChatResponse sent back to frontend
type ChatResponse struct {
	Reply        string         `json:"reply"`
	Thought      string         `json:"thought"`
	Actions      []AgentAction  `json:"actions"`
	SessionID    string         `json:"sessionId"`
	Status       string         `json:"status"` // "ok", "quota_exceeded", "gateway_error"
	CommandRan   *CommandResult `json:"commandRan,omitempty"`
	TokensLeft   int            `json:"tokensLeft"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	DiffStats    *DiffStats     `json:"diffStats,omitempty"`
}

// QuotaStatus represents the user's current token status
type QuotaStatus struct {
	Allowed         bool   `json:"allowed"`
	TokensRemaining int    `json:"tokensRemaining"`
	Message         string `json:"message"`
}

// GrokAccountInfo represents the real Grok Build auth & package tier.
// LiveSync is true only after a successful call to cli-chat-proxy.grok.com.
// SyncState is "live", "cached" (tier recovered from local logs), or "offline".
type GrokAccountInfo struct {
	IsInstalled bool   `json:"isInstalled"`
	IsLoggedIn  bool   `json:"isLoggedIn"`
	Email       string `json:"email"`
	FirstName   string `json:"firstName"`
	Tier        string `json:"tier"` // e.g. "SUPERGROK PLUS", "SUPERGROK", "FREE"
	GrokPath    string `json:"grokPath"`
	LiveSync    bool   `json:"liveSync"`
	SyncState   string `json:"syncState"`
}

// LoginSessionInfo represents the active Device Code OAuth session
type LoginSessionInfo struct {
	Status   string `json:"status"` // "pending", "success", "error"
	AuthURL  string `json:"authUrl"`
	UserCode string `json:"userCode"`
	Message  string `json:"message"`
}

// NewApp creates a new App application struct
func NewApp() *App {
	loadRemoteOllamaURL()
	return &App{
		userTokens: 100, // Initial quota
		isOnline:   true,
		engine:     &Engine{},
		models:     &modelRegistry{},
	}
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// shutdown is called when the app closes: make sure the private sidecar dies with us.
func (a *App) shutdown(ctx context.Context) {
	a.engine.CancelInstallOnExit()
	a.engine.Stop()
}

// getGrokBinaryPath finds the installed grok.exe binary
func (a *App) getGrokBinaryPath() string {
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".grok", "bin", "grok.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("grok"); err == nil {
		return p
	}
	return ""
}

// hideWindow suppresses visible command prompt windows when executing subprocesses on Windows
func hideWindow(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		}
	}
}

// GetGrokAccountInfo reads the real Grok Build authentication and subscription tier
func (a *App) GetGrokAccountInfo() GrokAccountInfo {
	grokBin := a.getGrokBinaryPath()
	info := GrokAccountInfo{
		IsInstalled: grokBin != "",
		IsLoggedIn:  false,
		Tier:        "CLOUD",
		GrokPath:    grokBin,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return info
	}

	auth := readGrokAuth()
	info.IsLoggedIn = auth.LoggedIn
	info.Email = auth.Email
	info.FirstName = auth.FirstName
	if !info.IsLoggedIn {
		info.Tier = "CLOUD"
		return info
	}

	info.Tier = "FREE"
	settingsOK := false
	if auth.Token != "" {
		body, status, err := grokAPIGet(auth.Token, "/v1/settings")
		if err == nil && status == http.StatusOK {
			var settingsData struct {
				SubscriptionTierDisplay string `json:"subscription_tier_display"`
				SubscriptionTier        string `json:"subscription_tier"`
			}
			if json.Unmarshal(body, &settingsData) == nil {
				settingsOK = true
				tier := settingsData.SubscriptionTierDisplay
				if tier == "" {
					tier = settingsData.SubscriptionTier
				}
				if tier != "" {
					info.Tier = strings.ToUpper(strings.TrimSpace(tier))
				}
			}
		}
	}
	if settingsOK {
		info.LiveSync = true
		info.SyncState = "live"
		return info
	}
	if t := tierFromGrokLogs(home); t != "" {
		info.Tier = t
		info.SyncState = "cached"
		return info
	}
	info.SyncState = "offline"
	return info
}

// CancelGrokLogin aborts any pending login command
func (a *App) CancelGrokLogin() string {
	a.loginCmdMu.Lock()
	defer a.loginCmdMu.Unlock()
	if a.loginCmd != nil && a.loginCmd.Process != nil {
		_ = a.loginCmd.Process.Kill()
		a.loginCmd = nil
	}
	return "cancelled"
}

// killProcessTree forcefully terminates a command and all of its children.
// On Windows, exec.CommandContext's default kill only terminates the shell itself
// (e.g. powershell), leaving detached grandchildren (npm → node) alive and holding the
// output pipe, which makes CombinedOutput never return.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
		hideWindow(killCmd)
		return killCmd.Run()
	}
	return cmd.Process.Kill()
}

// StopActiveChat terminates any actively running Grok background command or
// build command immediately
func (a *App) StopActiveChat() bool {
	a.activeChatCmdMu.Lock()
	cmd := a.activeChatCmd
	cancelFn := a.activeChatCancelFn
	buildCmd := a.activeBuildCmd
	buildCancel := a.activeBuildCancel
	a.activeChatCancel = true
	a.activeChatCmdMu.Unlock()

	stopped := false
	if buildCancel != nil {
		// Cancelling the build context triggers cmd.Cancel, which kills the whole
		// process tree so powershell's npm/node grandchildren cannot linger on.
		buildCancel()
		stopped = true
	}
	if cancelFn != nil {
		cancelFn()
		stopped = true
	}
	if cmd != nil && cmd.Process != nil {
		_ = killProcessTree(cmd)
		stopped = true
	}
	if buildCmd != nil && buildCmd.Process != nil {
		_ = killProcessTree(buildCmd)
		stopped = true
	}
	return stopped
}

// StartGrokDeviceLogin triggers device code login, extracts URL and user code, opens browser, and listens for completion
func (a *App) StartGrokDeviceLogin() (LoginSessionInfo, error) {
	grokBin := a.getGrokBinaryPath()
	if grokBin == "" {
		return LoginSessionInfo{Status: "error", Message: "grok.exe not found"}, fmt.Errorf("grok.exe not found")
	}

	a.CancelGrokLogin()

	cmd := exec.Command(grokBin, "login", "--device-auth")
	hideWindow(cmd)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return LoginSessionInfo{Status: "error", Message: err.Error()}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return LoginSessionInfo{Status: "error", Message: err.Error()}, err
	}

	if err := cmd.Start(); err != nil {
		return LoginSessionInfo{Status: "error", Message: err.Error()}, err
	}

	a.loginCmdMu.Lock()
	a.loginCmd = cmd
	a.loginCmdMu.Unlock()

	session := LoginSessionInfo{Status: "pending"}
	foundChan := make(chan bool, 1)
	lineChan := make(chan string, 100)

	// Scan stdout
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			lineChan <- scanner.Text()
		}
	}()

	// Scan stderr (grok login outputs instructions to stderr)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			lineChan <- scanner.Text()
		}
	}()

	codeRegex := regexp.MustCompile(`[A-Z0-9]{4}-[A-Z0-9]{4}`)
	urlRegex := regexp.MustCompile(`https://accounts\.x\.ai/oauth2/device\?user_code=[A-Za-z0-9_-]+`)

	go func() {
		for line := range lineChan {
			// Check URL
			if u := urlRegex.FindString(line); u != "" {
				session.AuthURL = u
				if strings.Contains(u, "user_code=") {
					parts := strings.Split(u, "user_code=")
					if len(parts) > 1 {
						session.UserCode = strings.TrimSpace(parts[1])
					}
				}
				wailsRuntime.EventsEmit(a.ctx, "grok-login-code", session)
				select {
				case foundChan <- true:
				default:
				}
			}

			// Check user code if not found yet
			if session.UserCode == "" {
				if c := codeRegex.FindString(line); c != "" {
					session.UserCode = c
					wailsRuntime.EventsEmit(a.ctx, "grok-login-code", session)
					select {
					case foundChan <- true:
					default:
					}
				}
			}
		}
	}()

	go func() {
		// Wait for command completion
		_ = cmd.Wait()
		a.loginCmdMu.Lock()
		a.loginCmd = nil
		a.loginCmdMu.Unlock()

		// Refresh account info and notify frontend
		info := a.GetGrokAccountInfo()
		wailsRuntime.EventsEmit(a.ctx, "grok-login-complete", info)
	}()

	select {
	case <-foundChan:
		if session.AuthURL != "" {
			wailsRuntime.BrowserOpenURL(a.ctx, session.AuthURL)
		}
		return session, nil
	case <-time.After(3 * time.Second):
		if session.AuthURL != "" {
			wailsRuntime.BrowserOpenURL(a.ctx, session.AuthURL)
			return session, nil
		}
		return LoginSessionInfo{Status: "pending", Message: "Waiting for device authorization code..."}, nil
	}
}

// LogoutGrok clears credentials and resets state
func (a *App) LogoutGrok() GrokAccountInfo {
	grokBin := a.getGrokBinaryPath()
	if grokBin != "" {
		cmd := exec.Command(grokBin, "logout")
		hideWindow(cmd)
		_ = cmd.Run()
	}
	return a.GetGrokAccountInfo()
}

// SelectProjectFolder opens a native folder selection dialog
func (a *App) SelectProjectFolder() (string, error) {
	folder, err := wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Project Directory for Sent-Code",
	})
	if err != nil {
		return "", err
	}
	if folder != "" {
		a.currentFolder = folder
	}
	return folder, nil
}

// GetCurrentFolder returns the currently selected project directory
func (a *App) GetCurrentFolder() string {
	return a.currentFolder
}

// SetCurrentFolder switches the active project folder
func (a *App) SetCurrentFolder(folder string) string {
	a.currentFolder = strings.TrimSpace(folder)
	return a.currentFolder
}

// WorkspaceItem represents an openable or recent project workspace
type WorkspaceItem struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Branch string `json:"branch,omitempty"`
}

func getGitBranch(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	headPath := filepath.Join(projectDir, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "ref: refs/heads/") {
		return strings.TrimPrefix(s, "ref: refs/heads/")
	}
	if len(s) >= 7 {
		return s[:7]
	}
	return ""
}

// GetProjectBranch returns the active git branch for a project
func (a *App) GetProjectBranch(projectDir string) string {
	if projectDir == "" {
		projectDir = a.currentFolder
	}
	return getGitBranch(projectDir)
}

// GetRecentWorkspaces returns discovered recent project folders
func (a *App) GetRecentWorkspaces() []WorkspaceItem {
	var list []WorkspaceItem
	seen := make(map[string]bool)

	add := func(p string) {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || seen[strings.ToLower(p)] {
			return
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			seen[strings.ToLower(p)] = true
			name := filepath.Base(p)
			if name == "" || name == "." || name == "/" || name == "\\" {
				name = p
			}
			list = append(list, WorkspaceItem{
				Path:   p,
				Name:   name,
				Branch: getGitBranch(p),
			})
		}
	}

	if a.currentFolder != "" {
		add(a.currentFolder)
	}

	// 1. Scan ~/.grok/sessions for known projects
	if home, err := os.UserHomeDir(); err == nil {
		grokSessions := filepath.Join(home, ".grok", "sessions")
		if entries, err := os.ReadDir(grokSessions); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					unescaped, err := url.QueryUnescape(e.Name())
					if err == nil && unescaped != "" {
						add(unescaped)
					}
				}
			}
		}
	}

	// Fallback if none found
	if len(list) == 0 {
		add(`C:\Drive-D\My-Program\Sent-Code`)
	}

	return list
}

// listIgnoredDirNames derives the set of top-level directory names to hide in ListProjectFiles.
// It reads .gitignore from projectRoot (best-effort) and adds any top-level directory patterns
// found there (e.g. "dist/", "build/", "target/") to a sensible built-in baseline.
// The baseline always includes node_modules (almost never useful in a file picker).
func listIgnoredDirNames(projectRoot string) map[string]bool {
	ignored := map[string]bool{
		"node_modules": true,
	}
	data, err := os.ReadFile(filepath.Join(projectRoot, ".gitignore"))
	if err != nil {
		// No .gitignore — fall back to a minimal opinionated list only.
		for _, n := range []string{"dist", "target", "build"} {
			ignored[n] = true
		}
		return ignored
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Only treat simple top-level directory patterns (no slashes except trailing).
		// "dist/" → "dist", "build" → "build", ".sentcode" → skip (hidden dirs handled separately).
		name := strings.TrimSuffix(line, "/")
		if strings.ContainsAny(name, "*/!?[\\") || strings.HasPrefix(name, ".") {
			continue
		}
		ignored[name] = true
	}
	return ignored
}

// ListProjectFiles lists top-level files and folders in the project.
// Hidden dot-entries are always excluded. Everything else is filtered using
// listIgnoredDirNames, which reads .gitignore when available.
func (a *App) ListProjectFiles(folderPath string) ([]ProjectFileItem, error) {
	target := folderPath
	if target == "" {
		target = a.currentFolder
	}
	if target == "" {
		return []ProjectFileItem{}, nil
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}

	ignoredDirs := listIgnoredDirNames(target)

	var items []ProjectFileItem
	for _, entry := range entries {
		name := entry.Name()
		// Always skip hidden dot-entries.
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Skip ignored directory names (not files with the same name).
		if entry.IsDir() && ignoredDirs[name] {
			continue
		}

		info, err := entry.Info()
		size := int64(0)
		modTime := ""
		if err == nil {
			size = info.Size()
			modTime = info.ModTime().Format("2006-01-02 15:04")
		}

		items = append(items, ProjectFileItem{
			Name:    name,
			Path:    filepath.Join(target, name),
			IsDir:   entry.IsDir(),
			Size:    size,
			ModTime: modTime,
		})
	}

	return items, nil
}

// runBuildTimeout is the hard cap for one RunBuildCommand invocation.
// A var (not const) so tests can shrink it.
var runBuildTimeout = 5 * time.Minute

// maxBuildOutputBytes is the maximum combined output kept from a build command.
// Only the tail is kept so the AI self-correction loop sees the most relevant errors.
const maxBuildOutputBytes = 16 * 1024

// RunBuildCommand executes a build or test command in the project folder.
//
// Safety invariants:
//   - folderPath must be the user's selected project directory (or a subdirectory of it).
//     Any path that would escape the project root is rejected to prevent path-traversal abuse.
//   - The command is capped to runBuildTimeout; commands that hang (dev servers, prompts
//     waiting for stdin) are killed automatically, matching the behaviour of StopActiveChat.
//   - The running command is registered on the App so StopActiveChat can abort it mid-flight;
//     both stop and timeout kill the entire process tree (powershell → npm → node), not just
//     the shell, so CombinedOutput always returns.
//   - Combined output is capped at maxBuildOutputBytes (tail) so large build logs don't
//     accumulate unboundedly in the self-correction loop.
func (a *App) RunBuildCommand(folderPath string, commandStr string) CommandResult {
	start := time.Now()

	targetDir := folderPath
	if targetDir == "" {
		targetDir = a.currentFolder
	}
	if targetDir == "" {
		return CommandResult{
			Command:  commandStr,
			Stdout:   "No project folder selected.",
			ExitCode: 1,
			Success:  false,
			Duration: "0s",
		}
	}

	// Guard: targetDir must be the selected project root or a child of it.
	// filepath.Rel returns a path starting with ".." when target escapes root.
	projectRoot := a.currentFolder
	if projectRoot != "" {
		rel, err := filepath.Rel(projectRoot, targetDir)
		if err != nil || strings.HasPrefix(rel, "..") {
			return CommandResult{
				Command:  commandStr,
				Stdout:   fmt.Sprintf("Rejected: folder %q is outside the selected project %q.", targetDir, projectRoot),
				ExitCode: 1,
				Success:  false,
				Duration: "0s",
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), runBuildTimeout)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", commandStr)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", commandStr)
	}
	hideWindow(cmd)
	cmd.Dir = targetDir

	// CommandContext's default cancel only kills the shell process; the npm/node
	// grandchildren it spawned survive, keep the output pipe open and CombinedOutput
	// never returns. Kill the whole tree instead, and force Wait to give up if some
	// detached grandchild still holds the pipe.
	cmd.Cancel = func() error {
		_ = killProcessTree(cmd)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	// Register so StopActiveChat can abort this command mid-flight.
	a.activeChatCmdMu.Lock()
	a.activeBuildCmd = cmd
	a.activeBuildCancel = cancel
	a.activeChatCmdMu.Unlock()
	defer func() {
		a.activeChatCmdMu.Lock()
		if a.activeBuildCmd == cmd {
			a.activeBuildCmd = nil
			a.activeBuildCancel = nil
		}
		a.activeChatCmdMu.Unlock()
		cancel()
	}()

	out, err := cmd.CombinedOutput()
	duration := time.Since(start).Round(time.Millisecond).String()

	// Cap output: keep the tail so the self-correction loop sees the most recent errors.
	if len(out) > maxBuildOutputBytes {
		out = out[len(out)-maxBuildOutputBytes:]
	}
	outStr := string(out)
	if ctx.Err() == context.DeadlineExceeded {
		outStr += fmt.Sprintf("\n\n(command timed out after %s and was killed)", runBuildTimeout)
	} else if ctx.Err() == context.Canceled {
		outStr += "\n\n(command was stopped and its process tree was killed)"
	}

	exitCode := 0
	success := err == nil
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return CommandResult{
		Command:  commandStr,
		Stdout:   outStr,
		ExitCode: exitCode,
		Success:  success,
		Duration: duration,
	}
}

// CheckQuotaStatus checks token balance with the host
func (a *App) CheckQuotaStatus() QuotaStatus {
	if a.userTokens <= 0 {
		return QuotaStatus{
			Allowed:         false,
			TokensRemaining: 0,
			Message:         "You've reached your current usage limit. Please refill your tokens or upgrade your plan to continue using this model.",
		}
	}
	return QuotaStatus{
		Allowed:         true,
		TokensRemaining: a.userTokens,
		Message:         "",
	}
}

// RefillTokens simulates adding tokens for testing / after payment
func (a *App) RefillTokens(amount int) int {
	a.userTokens += amount
	return a.userTokens
}

// SetOnlineStatus simulates toggling connection status
func (a *App) SetOnlineStatus(online bool) bool {
	a.isOnline = online
	return a.isOnline
}

// logID picks the key for the durable conversation log: the frontend's stable per-conversation
// id, falling back to SessionID for older callers/tests that only set that one.
func (req ChatRequest) logID() string {
	if req.LogSessionID != "" {
		return req.LogSessionID
	}
	return req.SessionID
}

// SendChatMessage executes prompts against the real Grok Build binary or local engine, and
// durably logs both sides of the turn to this conversation's on-disk record (see sessionstore.go)
// so history survives switching models mid-chat and surviving an app restart.
func (a *App) SendChatMessage(req ChatRequest) ChatResponse {
	id := req.logID()
	dir := req.ProjectPath
	if dir == "" {
		dir = a.currentFolder
	}
	if id != "" {
		appendStoredTurn(id, dir, "user", "", req.Message, len(req.Images))
	}

	resp := a.sendChatMessageInner(req)

	if id != "" && resp.Status == "ok" && strings.TrimSpace(resp.Reply) != "" {
		appendStoredTurn(id, dir, "assistant", req.Model, resp.Reply, 0)
	}
	return resp
}

// LiveAgentStep represents an execution step emitted in real time to the UI (Antigravity style)
type LiveAgentStep struct {
	ID        string `json:"id"`
	Icon      string `json:"icon"`
	Title     string `json:"title"`
	Detail    string `json:"detail,omitempty"`
	Status    string `json:"status"` // "pending" | "running" | "done"
	Timestamp int64  `json:"timestamp"`
}

func (a *App) emitLiveStep(id, icon, title, detail, status string) {
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "chat-live-step", LiveAgentStep{
			ID:        id,
			Icon:      icon,
			Title:     title,
			Detail:    detail,
			Status:    status,
			Timestamp: time.Now().UnixMilli(),
		})
	}
}

func (a *App) emitLiveThought(chunk string) {
	if a.ctx != nil && chunk != "" {
		wailsRuntime.EventsEmit(a.ctx, "chat-thought-chunk", chunk)
	}
}

// cleanProjectDisplayPath returns a clean project-relative path or base name,
// and returns "" if the path is an internal system/program file that shouldn't be exposed.
func cleanProjectDisplayPath(p, projectDir string) string {
	if p == "" {
		return ""
	}
	norm := filepath.ToSlash(p)
	normLower := strings.ToLower(norm)

	// Filter out internal application / system paths completely
	systemMarkers := []string{
		"/.grok/", "/.grok", "\\.grok",
		"/appdata/", "\\appdata", "appdata",
		"/tmp/", "\\tmp", "/temp/", "\\temp",
		"/.system_generated/", "\\.system_generated",
		"node_modules", ".git",
	}
	for _, marker := range systemMarkers {
		if strings.Contains(normLower, marker) {
			return ""
		}
	}

	if projectDir != "" {
		if rel, err := filepath.Rel(projectDir, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}

	base := filepath.Base(p)
	if base == "." || base == "/" || base == "\\" {
		return ""
	}
	return base
}

// computeWorkspaceDiff runs git diff --numstat and git diff to measure code changes in projectDir
func computeWorkspaceDiff(projectDir string) *DiffStats {
	if projectDir == "" {
		return nil
	}
	cmdStat := exec.Command("git", "diff", "--numstat")
	cmdStat.Dir = projectDir
	hideWindow(cmdStat)
	outStat, err := cmdStat.Output()
	if err != nil || len(outStat) == 0 {
		cmdStaged := exec.Command("git", "diff", "--cached", "--numstat")
		cmdStaged.Dir = projectDir
		hideWindow(cmdStaged)
		outStaged, err2 := cmdStaged.Output()
		if err2 != nil || len(outStaged) == 0 {
			return nil
		}
		outStat = outStaged
	}

	totalAdd := 0
	totalDel := 0
	var files []string

	lines := strings.Split(string(outStat), "\n")
	for _, l := range lines {
		parts := strings.Fields(l)
		if len(parts) >= 3 {
			var add, del int
			fmt.Sscanf(parts[0], "%d", &add)
			fmt.Sscanf(parts[1], "%d", &del)
			totalAdd += add
			totalDel += del
			files = append(files, parts[2])
		}
	}

	if totalAdd == 0 && totalDel == 0 && len(files) == 0 {
		return nil
	}

	cmdDiff := exec.Command("git", "diff")
	cmdDiff.Dir = projectDir
	hideWindow(cmdDiff)
	outDiff, _ := cmdDiff.Output()

	diffText := string(outDiff)
	if len(diffText) == 0 {
		cmdStagedDiff := exec.Command("git", "diff", "--cached")
		cmdStagedDiff.Dir = projectDir
		hideWindow(cmdStagedDiff)
		outDiff, _ = cmdStagedDiff.Output()
		diffText = string(outDiff)
	}

	if len(diffText) > 200000 {
		diffText = diffText[:200000] + "\n...(diff truncated)"
	}

	return &DiffStats{
		Additions: totalAdd,
		Deletions: totalDel,
		Files:     files,
		DiffText:  diffText,
	}
}

// monitorWorkspaceModifications scans for file edits/writes happening in projectDir during turn
func (a *App) monitorWorkspaceModifications(targetDir string, stop <-chan struct{}) {
	if targetDir == "" {
		return
	}
	startTime := time.Now().Add(-500 * time.Millisecond)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	reportedFiles := make(map[string]bool)

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		_ = filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			rel, err := filepath.Rel(targetDir, path)
			if err != nil {
				return nil
			}
			if strings.HasPrefix(rel, ".") || strings.HasPrefix(rel, "node_modules") || strings.HasPrefix(rel, "dist") || strings.HasPrefix(rel, "build") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !info.IsDir() && info.ModTime().After(startTime) {
				if !reportedFiles[rel] {
					reportedFiles[rel] = true
					fileName := filepath.Base(rel)
					a.emitLiveStep("fs-"+rel, "edit", "Editing "+fileName, rel, "running")
					go func(r, fn string) {
						time.Sleep(600 * time.Millisecond)
						a.emitLiveStep("fs-"+r, "edit", "Saved "+fn, r, "done")
					}(rel, fileName)
				}
			}
			return nil
		})
	}
}

// monitorGrokLiveTurn reads real-time updates.jsonl from Grok's session folder to stream tool calls and thoughts
func (a *App) monitorGrokLiveTurn(sessionDir, initialSessionID, targetDir string, stop <-chan struct{}) {
	if sessionDir == "" {
		return
	}
	activeID := initialSessionID
	var updatesFile *os.File
	var currentOffset int64

	for activeID == "" {
		select {
		case <-stop:
			return
		case <-time.After(100 * time.Millisecond):
		}
		if entries, err := os.ReadDir(sessionDir); err == nil {
			var latestID string
			var latestTime int64
			for _, entry := range entries {
				if entry.IsDir() {
					if info, err := entry.Info(); err == nil && info.ModTime().UnixMilli() > latestTime {
						latestTime = info.ModTime().UnixMilli()
						latestID = entry.Name()
					}
				}
			}
			if latestID != "" {
				activeID = latestID
			}
		}
	}

	sessionSubDir := filepath.Join(sessionDir, activeID)
	updatesPath := filepath.Join(sessionSubDir, "updates.jsonl")

	defer func() {
		if updatesFile != nil {
			_ = updatesFile.Close()
		}
	}()

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		if updatesFile == nil {
			f, err := os.Open(updatesPath)
			if err != nil {
				continue
			}
			updatesFile = f
			currentOffset = 0
		}

		stat, err := updatesFile.Stat()
		if err != nil || stat.Size() <= currentOffset {
			continue
		}

		buf := make([]byte, stat.Size()-currentOffset)
		n, err := updatesFile.ReadAt(buf, currentOffset)
		if n > 0 {
			currentOffset += int64(n)
			lines := strings.Split(string(buf[:n]), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				var raw map[string]any
				if err := json.Unmarshal([]byte(line), &raw); err != nil {
					continue
				}
				params, _ := raw["params"].(map[string]any)
				if params == nil {
					continue
				}
				update, _ := params["update"].(map[string]any)
				if update == nil {
					continue
				}

				sessionUpdate, _ := update["sessionUpdate"].(string)
				switch sessionUpdate {
				case "agent_thought_chunk":
					if content, ok := update["content"].(map[string]any); ok {
						if text, ok := content["text"].(string); ok && text != "" {
							a.emitLiveThought(text)
						}
					}
				case "tool_call", "tool_call_update":
					title, _ := update["title"].(string)
					toolCallID, _ := update["toolCallId"].(string)
					status, _ := update["status"].(string)
					if toolCallID == "" {
						toolCallID = fmt.Sprintf("grok-tool-%d", time.Now().UnixNano())
					}

					stepStatus := "running"
					if strings.EqualFold(status, "completed") || strings.EqualFold(status, "done") {
						stepStatus = "done"
					}

					icon := "terminal"
					actionTitle := title
					detail := ""

					rawInput, _ := update["rawInput"].(map[string]any)
					filePath := ""
					if rawInput != nil {
						if p, ok := rawInput["path"].(string); ok {
							filePath = p
						} else if p, ok := rawInput["file_path"].(string); ok {
							filePath = p
						} else if p, ok := rawInput["url"].(string); ok {
							detail = p
						} else if q, ok := rawInput["query"].(string); ok {
							detail = q
						} else if c, ok := rawInput["command"].(string); ok {
							detail = c
						}
					}

					if filePath != "" {
						clean := cleanProjectDisplayPath(filePath, targetDir)
						if clean == "" {
							// Filter out internal system / app / engine directories completely
							continue
						}
						filePath = clean
						detail = clean
					}

					lowerTitle := strings.ToLower(title)
					if strings.Contains(lowerTitle, "view") || strings.Contains(lowerTitle, "read") || strings.Contains(lowerTitle, "inspect") {
						icon = "file"
						if filePath != "" {
							actionTitle = "Reading " + filepath.Base(filePath)
							detail = filePath
						} else if actionTitle == "" {
							actionTitle = "Reading project file"
						}
					} else if strings.Contains(lowerTitle, "edit") || strings.Contains(lowerTitle, "write") || strings.Contains(lowerTitle, "create") || strings.Contains(lowerTitle, "replace") {
						icon = "edit"
						if filePath != "" {
							actionTitle = "Editing " + filepath.Base(filePath)
							detail = filePath
						} else if actionTitle == "" {
							actionTitle = "Editing file"
						}
					} else if strings.Contains(lowerTitle, "fetch") || strings.Contains(lowerTitle, "web") || strings.Contains(lowerTitle, "search") {
						icon = "search"
					}

					if actionTitle != "" {
						a.emitLiveStep(toolCallID, icon, actionTitle, detail, stepStatus)
					}
				}
			}
		}
	}
}

// sendChatMessageInner executes prompts against the real Grok Build binary or local engine
func (a *App) sendChatMessageInner(req ChatRequest) ChatResponse {
	targetDir := req.ProjectPath
	if targetDir == "" {
		targetDir = a.currentFolder
	}
	if len(req.Images) > 0 {
		a.emitLiveStep("step-vision", "image", fmt.Sprintf("Attached %d image%s for this turn", len(req.Images), pluralInt(len(req.Images))), "", "done")
	}

	stopFsMonitor := make(chan struct{})
	defer close(stopFsMonitor)
	go a.monitorWorkspaceModifications(targetDir, stopFsMonitor)

	// 1. Connection check with Error Masking
	if !a.isOnline {
		return ChatResponse{
			Status:       "gateway_error",
			Reply:        "Service gateway unavailable (ERR_CONNECTION_REFUSED: 502). Please verify your network connectivity or subscription status.",
			ErrorMessage: "Network handshake timeout",
			TokensLeft:   a.userTokens,
		}
	}

	// 2. Token Quota check
	if a.userTokens <= 0 {
		return ChatResponse{
			Status:       "quota_exceeded",
			Reply:        "You've reached your current usage limit. Please refill your tokens or upgrade your plan to continue using this model.",
			ErrorMessage: "Quota limit reached",
			TokensLeft:   0,
		}
	}

	// Deduct 1 token per interaction
	a.userTokens--

	// Durability fallback: if the caller sent no history (a fresh frontend session that hasn't
	// reloaded its own message list yet, a CLI/test caller, ...), recover it from our own disk log.
	if len(req.History) == 0 {
		if id := req.logID(); id != "" {
			req.History = storedHistoryFallback(targetDir, id)
		}
	}

	isGrok := strings.HasPrefix(req.Model, "grok-") || req.Model == "supergrok"

	// Everything else goes through the model registry (local Ollama models and CLI agents)
	if !isGrok {
		def, ok := a.findModel(req.Model)
		if !ok || !isInstalled(def) {
			a.userTokens++ // nothing ran, do not charge
			return ChatResponse{
				Status:     "model_unavailable",
				Reply:      "This model is not installed yet. Open Settings → Models to install it, or pick another model.",
				TokensLeft: a.userTokens,
			}
		}
		return a.runRegistryModel(req, def, targetDir)
	}

	// If using Grok modes (Fast, Auto, Expert, Heavy), execute with real Grok Build!
	if isGrok {
		if resp, handled := a.runGrokTurn(req, targetDir); handled {
			return resp
		}
	}

	// Grok engine missing or returned nothing: nothing was delivered, so do not charge
	a.userTokens++
	return ChatResponse{
		Status:       "gateway_error",
		Reply:        maskError(fmt.Errorf("grok engine unavailable")),
		ErrorMessage: "Network handshake timeout",
		TokensLeft:   a.userTokens,
	}
}

// runGrokTurn executes one prompt turn with the real Grok Build binary, registering the
// turn's command on the App so StopActiveChat can abort it mid-flight. handled is false
// when the Grok binary is missing or the turn produced neither output nor an error; the
// caller then refunds the token and degrades to the generic gateway error.
func (a *App) runGrokTurn(req ChatRequest, targetDir string) (ChatResponse, bool) {
	grokBin := a.getGrokBinaryPath()
	if grokBin == "" {
		return ChatResponse{}, false
	}

	startTime := time.Now()

	modelName := "Grok Auto"
	switch req.Model {
	case "grok-fast":
		modelName = "Grok Fast"
	case "grok-expert":
		modelName = "Grok Expert"
	case "grok-heavy":
		modelName = "Grok Heavy"
	}
	a.emitLiveStep("step-model", "brain", "Reasoning with "+modelName, "", "running")

	type ACPBlock struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		Data     string `json:"data,omitempty"`
		MimeType string `json:"mimeType,omitempty"`
	}

	var args []string

	sessionDir := a.findGrokSessionDir(targetDir)
	activeSessionID := req.SessionID
	resuming := false
	if activeSessionID != "" && sessionDir != "" {
		if _, err := os.Stat(filepath.Join(sessionDir, activeSessionID)); err == nil {
			resuming = true
		}
	}

	// Continuity across model switches: Grok's own resumed session only knows what Grok
	// itself answered. Anything after its last turn (or everything, on Grok's first turn
	// in this conversation) is handed over explicitly so it can pick up where it left off.
	msg := req.Message
	if msg == "" && len(req.Images) > 0 {
		msg = "Describe this image and analyze its code or design context."
	}
	msg = withModelHandoff(msg, req.History, isGrokModel)

	// If images are attached, construct ACP content blocks file for vision
	if len(req.Images) > 0 {
		savedPaths := saveChatAttachments(targetDir, req.Images)
		if len(savedPaths) > 0 {
			msg += formatAttachmentNote(targetDir, savedPaths)
		}

		var blocks []ACPBlock
		blocks = append(blocks, ACPBlock{Type: "text", Text: msg})

		for _, imgURI := range req.Images {
			mime := "image/png"
			rawBase64 := imgURI
			if strings.HasPrefix(imgURI, "data:") {
				parts := strings.SplitN(imgURI, ",", 2)
				if len(parts) == 2 {
					rawBase64 = parts[1]
					header := parts[0]
					if semi := strings.Index(header, ";"); semi != -1 {
						mime = strings.TrimPrefix(header[:semi], "data:")
					}
				}
			}
			blocks = append(blocks, ACPBlock{
				Type:     "image",
				Data:     rawBase64,
				MimeType: mime,
			})
		}

		jsonBytes, err := json.Marshal(blocks)
		if err == nil {
			tempPromptFile := filepath.Join(os.TempDir(), fmt.Sprintf("grok_prompt_%d.json", time.Now().UnixNano()))
			if err := os.WriteFile(tempPromptFile, jsonBytes, 0600); err == nil {
				defer os.Remove(tempPromptFile)
				args = append(args, "--prompt-file", tempPromptFile)
			}
		}
	}

	if resuming {
		args = append([]string{"--resume", activeSessionID}, args...)
	}
	args = appendGrokEffort(args, req.Model)
	args = append(args, "--output-format", "streaming-json", "--always-approve")
	if len(req.Images) == 0 {
		args = append(args, "-p", msg)
	}

	cmd := exec.Command(grokBin, args...)
	hideWindow(cmd)
	if targetDir != "" {
		cmd.Dir = targetDir
	}

	a.activeChatCmdMu.Lock()
	a.activeChatCmd = cmd
	a.activeChatCancel = false
	a.activeChatCmdMu.Unlock()

	defer func() {
		a.activeChatCmdMu.Lock()
		if a.activeChatCmd == cmd {
			a.activeChatCmd = nil
			a.activeChatCancel = false
		}
		a.activeChatCmdMu.Unlock()
	}()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ChatResponse{
			Status:     "error",
			Reply:      fmt.Sprintf("Grok Pipe Error: %s", err.Error()),
			TokensLeft: a.userTokens,
		}, true
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return ChatResponse{
			Status:     "error",
			Reply:      fmt.Sprintf("Grok Start Error: %s", err.Error()),
			TokensLeft: a.userTokens,
		}, true
	}

	var textBuilder strings.Builder
	var thoughtBuilder strings.Builder
	liveTools := map[string]LiveAgentStep{}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		u, ok := parseGrokStreamLine(line, targetDir)
		if !ok {
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "{") {
				textBuilder.WriteString(line)
				textBuilder.WriteString("\n")
			}
			continue
		}

		if u.Thought != "" {
			thoughtBuilder.WriteString(u.Thought)
			a.emitLiveThought(u.Thought)
		}
		if u.Text != "" {
			textBuilder.WriteString(u.Text)
		}
		for _, s := range u.Steps {
			liveTools[s.ID] = s
			a.emitLiveStep(s.ID, s.Icon, s.Title, s.Detail, "running")
		}
		for _, id := range u.Done {
			if s, ok := liveTools[id]; ok {
				a.emitLiveStep(id, s.Icon, s.Title, s.Detail, "done")
			} else {
				a.emitLiveStep(id, "terminal", "Completed tool", "", "done")
			}
		}
		if u.SessionID != "" {
			activeSessionID = u.SessionID
		}
	}

	cmdErr := cmd.Wait()

	for id, s := range liveTools {
		a.emitLiveStep(id, s.Icon, s.Title, s.Detail, "done")
	}

	a.activeChatCmdMu.Lock()
	wasStopped := a.activeChatCancel
	a.activeChatCmdMu.Unlock()

	duration := time.Since(startTime).Round(time.Millisecond).String()
	thoughtStr := fmt.Sprintf("Thought for %s", duration)
	if th := strings.TrimSpace(thoughtBuilder.String()); th != "" {
		thoughtStr = fmt.Sprintf("%s\n\n%s", thoughtStr, th)
	}

	// If new session, detect generated session ID
	if activeSessionID == "" && sessionDir != "" {
		if entries, err := os.ReadDir(sessionDir); err == nil {
			var latestID string
			var latestTime int64
			for _, entry := range entries {
				if entry.IsDir() {
					if info, err := entry.Info(); err == nil && info.ModTime().UnixMilli() > latestTime {
						latestTime = info.ModTime().UnixMilli()
						latestID = entry.Name()
					}
				}
			}
			if latestID != "" {
				activeSessionID = latestID
			}
		}
	}

	// Build dynamic real agent actions (Antigravity style)
	var actions []AgentAction

	if len(req.Images) > 0 {
		count := len(req.Images)
		detail := fmt.Sprintf("(%d attachment)", count)
		if count > 1 {
			detail = fmt.Sprintf("(%d attachments)", count)
		}
		actions = append(actions, AgentAction{
			Type:   "view_image",
			Label:  "Viewed image",
			Detail: detail,
			Icon:   "🖼️",
		})
	}

	if wasStopped {
		return ChatResponse{
			Status:  "cancelled",
			Thought: thoughtStr,
			Actions: []AgentAction{
				{
					Type:  "status",
					Label: "Stopped",
					Icon:  "⏹",
				},
			},
			SessionID:  activeSessionID,
			Reply:      "(Generation stopped by user)",
			TokensLeft: a.userTokens,
		}, true
	}

	for _, s := range liveTools {
		actions = append(actions, AgentAction{
			Type:   s.Icon,
			Label:  s.Title,
			Detail: s.Detail,
			Icon:   s.Icon,
		})
	}

	actions = append(actions, AgentAction{
		Type:  "status",
		Label: "Finished turn",
		Icon:  "✓",
	})

	hasCodeModifications := false
	for _, s := range liveTools {
		lowerTitle := strings.ToLower(s.Title)
		if s.Icon == "edit" || strings.Contains(lowerTitle, "edit") || strings.Contains(lowerTitle, "write") || strings.Contains(lowerTitle, "create") || strings.Contains(lowerTitle, "replace") || strings.Contains(lowerTitle, "patch") {
			hasCodeModifications = true
			break
		}
	}
	for _, act := range actions {
		if act.Type == "edit_file" || act.Icon == "edit" || strings.Contains(strings.ToLower(act.Label), "edit") || strings.Contains(strings.ToLower(act.Label), "write") || strings.Contains(strings.ToLower(act.Label), "saved") {
			hasCodeModifications = true
			break
		}
	}

	replyText := strings.TrimSpace(textBuilder.String())
	if strings.Contains(replyText, "reached your free Grok Build usage limit") || strings.Contains(replyText, "reached your Grok Build usage limit") {
		a.userTokens++ // refund token since it was interrupted by quota limit
		return ChatResponse{
			Status:     "quota_exceeded",
			Thought:    thoughtStr,
			Actions:    []AgentAction{{Type: "status", Label: "Quota Limit", Icon: "⚠️"}},
			SessionID:  activeSessionID,
			Reply:      "⚠️ คุณใช้โควต้า Grok Build ฟรีของบัญชี xAI ครบตามกำหนดชั่วคราวแล้ว (Rate limit reached)\n\nกรุณารอสักครู่ (ประมาณ 1-2 ชั่วโมง) เพื่อรีเฟรชรอบใหม่ หรืออัปเกรดเป็น SuperGrok ที่ https://grok.com/supergrok",
			TokensLeft: a.userTokens,
		}, true
	}

	if replyText != "" {
		a.emitLiveStep("step-model", "brain", "Reasoning with "+modelName, "", "done")

		thinkRe := regexp.MustCompile(`(?s)<(?:think|thought)>(.*?)</(?:think|thought)>`)
		if m := thinkRe.FindStringSubmatch(replyText); len(m) > 1 {
			rawThought := strings.TrimSpace(m[1])
			replyText = strings.TrimSpace(thinkRe.ReplaceAllString(replyText, ""))
			if rawThought != "" {
				thoughtStr = fmt.Sprintf("%s\n\n%s", thoughtStr, rawThought)
			}
		}

		resp := ChatResponse{
			Status:     "ok",
			Thought:    thoughtStr,
			Actions:    actions,
			SessionID:  activeSessionID,
			Reply:      replyText,
			TokensLeft: a.userTokens,
		}
		if targetDir != "" && hasCodeModifications {
			resp.DiffStats = computeWorkspaceDiff(targetDir)
		}
		return resp, true
	}

	if cmdErr != nil {
		return ChatResponse{
			Status:     "error",
			Thought:    thoughtStr,
			Actions:    actions,
			SessionID:  activeSessionID,
			Reply:      fmt.Sprintf("Grok Error: %s", cmdErr.Error()),
			TokensLeft: a.userTokens,
		}, true
	}

	resp := ChatResponse{
		Status:     "ok",
		Thought:    thoughtStr,
		Actions:    actions,
		SessionID:  activeSessionID,
		Reply:      replyText,
		TokensLeft: a.userTokens,
	}
	if targetDir != "" && hasCodeModifications {
		resp.DiffStats = computeWorkspaceDiff(targetDir)
	}
	return resp, true
}

func isGrokModel(model string) bool {
	return strings.HasPrefix(model, "grok") || model == "supergrok"
}

func isClaudeModel(model string) bool {
	return strings.HasPrefix(model, "claude")
}

// trailingForeignGap returns history the home model has not answered: everything after its
// latest assistant turn, or the whole history when it has never answered this chat.
func trailingForeignGap(history []ChatTurn, isHome func(model string) bool) []ChatTurn {
	cut := 0
	for i, h := range history {
		if h.Role == "assistant" && isHome(h.Model) {
			cut = i + 1
		}
	}
	return history[cut:]
}

// trailingNonGrokGap is the Grok-shaped view of trailingForeignGap.
func trailingNonGrokGap(history []ChatTurn) []ChatTurn {
	return trailingForeignGap(history, isGrokModel)
}

// withModelHandoff prepends other models' turns so a resumed engine can continue the same chat.
func withModelHandoff(prompt string, history []ChatTurn, isHome func(model string) bool) string {
	if pre := grokHandoffPrelude(trailingForeignGap(history, isHome)); pre != "" {
		return pre + prompt
	}
	return prompt
}

// grokHandoffPrelude turns a gap in history into a compact, English-labeled context block so
// Grok can continue a conversation started on a different model instead of starting cold.
func grokHandoffPrelude(gap []ChatTurn) string {
	if len(gap) > maxHistoryTurns {
		gap = gap[len(gap)-maxHistoryTurns:]
	}
	var b strings.Builder
	wrote := false
	for _, h := range gap {
		if (h.Role != "user" && h.Role != "assistant") || strings.TrimSpace(h.Content) == "" {
			continue
		}
		c := h.Content
		if r := []rune(c); len(r) > maxTurnChars {
			c = string(r[:maxTurnChars])
		}
		label := "User"
		if h.Role == "assistant" {
			label = "Assistant"
			if h.Model != "" {
				label += " (" + h.Model + ")"
			}
		}
		fmt.Fprintf(&b, "%s: %s\n\n", label, c)
		wrote = true
	}
	if !wrote {
		return ""
	}
	return "[Conversation handoff: the turns below were answered by a different AI model earlier in this same chat. " +
		"Read them for context, then answer the user's latest message that follows. " +
		"Reply in the same language the user has been using.]\n\n" + b.String() + "---\n\n"
}

// maxSavedImageBytes is the maximum decoded image size accepted by SaveBase64Image.
// Images larger than this are very unusual for clipboard screenshots and are rejected.
const maxSavedImageBytes = 25 * 1024 * 1024 // 25 MB

// SaveBase64Image saves a pasted clipboard image to a temporary cache directory.
//
// Fixes compared to the previous version:
//   - Uses strings.Cut so any extra commas inside the base64 payload are handled correctly.
//   - Derives the file extension from the MIME type in the data URI header.
//   - Rejects payloads larger than maxSavedImageBytes before writing to disk.
//   - Evicts cache files older than 1 hour so the directory doesn't grow without bound.
func (a *App) SaveBase64Image(dataURI string) (string, error) {
	// "data:image/png;base64,<payload>" — Cut on the first comma only.
	header, payload, ok := strings.Cut(dataURI, ",")
	if !ok || payload == "" {
		return "", fmt.Errorf("invalid data URI: missing base64 payload")
	}

	// Determine extension from MIME type (header looks like "data:image/jpeg;base64").
	ext := ".png"
	header = strings.ToLower(header)
	switch {
	case strings.Contains(header, "image/jpeg") || strings.Contains(header, "image/jpg"):
		ext = ".jpg"
	case strings.Contains(header, "image/webp"):
		ext = ".webp"
	case strings.Contains(header, "image/gif"):
		ext = ".gif"
	}

	// Guard: reject suspiciously large payloads before decoding.
	if len(payload) > (maxSavedImageBytes/3)*4+4 {
		return "", fmt.Errorf("image too large (base64 length %d exceeds limit)", len(payload))
	}

	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	if len(data) > maxSavedImageBytes {
		return "", fmt.Errorf("decoded image too large (%d bytes, limit %d)", len(data), maxSavedImageBytes)
	}

	cacheDir := filepath.Join(os.TempDir(), "sent-code-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	// Evict files older than 1 hour to prevent unbounded accumulation.
	if entries, err := os.ReadDir(cacheDir); err == nil {
		cutoff := time.Now().Add(-1 * time.Hour)
		for _, e := range entries {
			if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(cacheDir, e.Name()))
			}
		}
	}

	filename := fmt.Sprintf("img_%d%s", time.Now().UnixNano(), ext)
	fullPath := filepath.Join(cacheDir, filename)

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", err
	}

	return fullPath, nil
}

// SessionItem represents a chat session from real Grok history
type SessionItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Project   string `json:"project"`
	UpdatedAt int64  `json:"updatedAt"` // unix ms
	DateStr   string `json:"dateStr"`
}

// CacheStats represents session history cache health and volume
type CacheStats struct {
	TotalSessions  int    `json:"totalSessions"`
	TotalSizeBytes int64  `json:"totalSizeBytes"`
	SizeFormatted  string `json:"sizeFormatted"`
	NeedsWarning   bool   `json:"needsWarning"`
	WarningMessage string `json:"warningMessage"`
}

// GrokQuotaUsage is the allowance from cli-chat-proxy.grok.com/v1/billing?format=credits.
// Percents are remaining. WeeklyKnown is false when the payload omits a figure; that is
// not 100% left. grok.com does not report a separate 5-hour window, so FiveHourKnown stays false.
type GrokQuotaUsage struct {
	WeeklyRemainingPct   int    `json:"weeklyRemainingPct"`
	WeeklyRefreshTime    string `json:"weeklyRefreshTime"`
	WeeklyKnown          bool   `json:"weeklyKnown"`
	PeriodLabel          string `json:"periodLabel"`
	FiveHourRemainingPct int    `json:"fiveHourRemainingPct"`
	FiveHourRefreshTime  string `json:"fiveHourRefreshTime"`
	FiveHourKnown        bool   `json:"fiveHourKnown"`
	ProductLabel         string `json:"productLabel"`
	ProductRemainingPct  int    `json:"productRemainingPct"`
	ProductKnown         bool   `json:"productKnown"`
	RequestsLast5Hours   int    `json:"requestsLast5Hours"`
	RequestsLastWeek     int    `json:"requestsLastWeek"`
	Tier                 string `json:"tier"`
	Live                 bool   `json:"live"`
	Source               string `json:"source"`
}

// ChatMessageItem represents a message inside a session
type ChatMessageItem struct {
	ID        string        `json:"id"`
	Sender    string        `json:"sender"`
	Text      string        `json:"text"`
	Thought   string        `json:"thought,omitempty"`
	Actions   []AgentAction `json:"actions,omitempty"`
	Timestamp string        `json:"timestamp"`
	Model     string        `json:"model,omitempty"`
}

// findGrokSessionDir resolves the session storage path for the given project
func (a *App) findGrokSessionDir(projectPath string) string {
	target := projectPath
	if target == "" {
		target = a.currentFolder
	}
	if target == "" {
		target = `C:\Drive-D\My-Program\Sent-Code`
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	baseSessions := filepath.Join(home, ".grok", "sessions")

	// Check direct encoded match: e.g. C%3A%5CDrive-D%5CMy-Program%5CSent-Code
	encoded := url.QueryEscape(target)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	directPath := filepath.Join(baseSessions, encoded)
	if _, err := os.Stat(directPath); err == nil {
		return directPath
	}

	// Fallback scan in ~/.grok/sessions for directory matching target
	entries, err := os.ReadDir(baseSessions)
	if err == nil {
		targetBase := strings.ToLower(filepath.Base(target))
		for _, entry := range entries {
			if entry.IsDir() {
				unescaped, _ := url.QueryUnescape(entry.Name())
				if strings.EqualFold(unescaped, target) || strings.EqualFold(filepath.Base(unescaped), targetBase) {
					return filepath.Join(baseSessions, entry.Name())
				}
			}
		}
	}

	return directPath
}

// GetGrokSessions reads real conversation sessions from ~/.grok/sessions/<workspace> and backend session storage
func (a *App) GetGrokSessions(projectPath string) []SessionItem {
	var sessions []SessionItem
	seen := make(map[string]bool)

	sessionDir := a.findGrokSessionDir(projectPath)
	if sessionDir != "" {
		// 1. Read prompt_history.jsonl if available to map session prompts
		promptMap := make(map[string]string)
		promptHistoryPath := filepath.Join(sessionDir, "prompt_history.jsonl")
		if f, err := os.Open(promptHistoryPath); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) == "" {
					continue
				}
				var item struct {
					SessionID string `json:"session_id"`
					Prompt    string `json:"prompt"`
				}
				if err := json.Unmarshal([]byte(line), &item); err == nil && item.SessionID != "" {
					if item.Prompt != "" {
						promptMap[item.SessionID] = item.Prompt
					}
				}
			}
			_ = f.Close()
		}

		// 2. Scan all session subdirectories
		if entries, err := os.ReadDir(sessionDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				sessionID := entry.Name()
				subDir := filepath.Join(sessionDir, sessionID)

				// Read summary.json
				summaryPath := filepath.Join(subDir, "summary.json")
				var title string
				updatedAt := time.Now().UnixMilli()

				if summaryBytes, err := os.ReadFile(summaryPath); err == nil {
					var summary struct {
						GeneratedTitle string `json:"generated_title"`
						SessionSummary string `json:"session_summary"`
						UpdatedAt      string `json:"updated_at"`
						LastActiveAt   string `json:"last_active_at"`
					}
					if err := json.Unmarshal(summaryBytes, &summary); err == nil {
						if summary.GeneratedTitle != "" {
							title = summary.GeneratedTitle
						} else if summary.SessionSummary != "" {
							title = summary.SessionSummary
						}

						tStr := summary.UpdatedAt
						if tStr == "" {
							tStr = summary.LastActiveAt
						}
						if tStr != "" {
							if t, err := time.Parse(time.RFC3339, tStr); err == nil {
								updatedAt = t.UnixMilli()
							}
						}
					}
				} else {
					if info, err := entry.Info(); err == nil {
						updatedAt = info.ModTime().UnixMilli()
					}
				}

				if title == "" {
					if p, ok := promptMap[sessionID]; ok && p != "" {
						title = p
					} else {
						title = sessionID
						if len(sessionID) > 8 {
							title = sessionID[:8]
						}
					}
				}

				// Clean up title length
				if len([]rune(title)) > 40 {
					title = string([]rune(title)[:40]) + "..."
				}

				seen[sessionID] = true
				sessions = append(sessions, SessionItem{
					ID:        sessionID,
					Title:     title,
					Project:   filepath.Base(sessionDir),
					UpdatedAt: updatedAt,
					DateStr:   time.UnixMilli(updatedAt).Format("15:04"),
				})
			}
		}
	}

	// 3. Scan backend stored sessions in AppData for all models (Qwen, Claude, local, unassigned)
	scanStoredDir := func(storedDir, fallbackProject string) {
		if storedDir == "" {
			return
		}
		entries, err := os.ReadDir(storedDir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			fPath := filepath.Join(storedDir, entry.Name())
			b, err := os.ReadFile(fPath)
			if err != nil {
				continue
			}
			var s StoredSession
			if err := json.Unmarshal(b, &s); err != nil || s.ID == "" {
				continue
			}
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true

			title := s.Title
			if title == "" && len(s.Turns) > 0 {
				title = titleFrom(s.Turns[0].Content)
			}
			if title == "" {
				title = s.ID
				if len(title) > 8 {
					title = title[:8]
				}
			}
			if len([]rune(title)) > 40 {
				title = string([]rune(title)[:40]) + "..."
			}

			updatedAt := time.Now().UnixMilli()
			if s.UpdatedAt != "" {
				if t, err := time.Parse(time.RFC3339, s.UpdatedAt); err == nil {
					updatedAt = t.UnixMilli()
				}
			} else if s.CreatedAt != "" {
				if t, err := time.Parse(time.RFC3339, s.CreatedAt); err == nil {
					updatedAt = t.UnixMilli()
				}
			} else if info, err := entry.Info(); err == nil {
				updatedAt = info.ModTime().UnixMilli()
			}

			projName := s.ProjectPath
			if projName == "" {
				projName = fallbackProject
			}
			if projName != "" {
				projName = filepath.Base(projName)
			} else {
				projName = "General"
			}

			sessions = append(sessions, SessionItem{
				ID:        s.ID,
				Title:     title,
				Project:   projName,
				UpdatedAt: updatedAt,
				DateStr:   time.UnixMilli(updatedAt).Format("15:04"),
			})
		}
	}

	scanStoredDir(sessionsDir(projectPath), projectPath)
	if a.currentFolder != "" && a.currentFolder != projectPath {
		scanStoredDir(sessionsDir(a.currentFolder), a.currentFolder)
	}
	scanStoredDir(sessionsDir(""), "Unassigned")
	scanStoredDir(filepath.Join(sentDataDir(), "sessions"), "General")

	if pEntries, err := os.ReadDir(filepath.Join(sentDataDir(), "projects")); err == nil {
		for _, pe := range pEntries {
			if pe.IsDir() {
				scanStoredDir(filepath.Join(sentDataDir(), "projects", pe.Name(), "sessions"), pe.Name())
			}
		}
	}

	// Sort most recent first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})

	return sessions
}

// DeleteGrokSession removes a single session folder or stored session from disk
func (a *App) DeleteGrokSession(projectPath string, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	deleted := false
	sessionDir := a.findGrokSessionDir(projectPath)
	if sessionDir != "" {
		targetDir := filepath.Join(sessionDir, sessionID)
		if _, err := os.Stat(targetDir); err == nil {
			if os.RemoveAll(targetDir) == nil {
				deleted = true
			}
		}
	}
	// Also delete from backend stored sessions in all locations
	safeID := sessionIDSafe.ReplaceAllString(sessionID, "_") + ".json"
	pathsToDelete := []string{
		sessionLogPath(projectPath, sessionID),
		sessionLogPath(a.currentFolder, sessionID),
		sessionLogPath("", sessionID),
		filepath.Join(sentDataDir(), "sessions", safeID),
	}
	if pEntries, err := os.ReadDir(filepath.Join(sentDataDir(), "projects")); err == nil {
		for _, pe := range pEntries {
			if pe.IsDir() {
				pathsToDelete = append(pathsToDelete, filepath.Join(sentDataDir(), "projects", pe.Name(), "sessions", safeID))
			}
		}
	}
	for _, p := range pathsToDelete {
		if _, err := os.Stat(p); err == nil {
			if os.Remove(p) == nil {
				deleted = true
			}
		}
	}
	return deleted
}

// GetCacheStats measures session history size and determines if a warning is needed
func (a *App) GetCacheStats(projectPath string) CacheStats {
	sessionDir := a.findGrokSessionDir(projectPath)
	if sessionDir == "" {
		return CacheStats{}
	}

	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return CacheStats{}
	}

	sessionCount := 0
	var totalBytes int64

	for _, entry := range entries {
		if entry.IsDir() {
			sessionCount++
			subDir := filepath.Join(sessionDir, entry.Name())
			_ = filepath.Walk(subDir, func(_ string, info os.FileInfo, err error) error {
				if err == nil && info != nil && !info.IsDir() {
					totalBytes += info.Size()
				}
				return nil
			})
		}
	}

	var sizeFormatted string
	if totalBytes > 1024*1024 {
		sizeFormatted = fmt.Sprintf("%.1f MB", float64(totalBytes)/(1024*1024))
	} else {
		sizeFormatted = fmt.Sprintf("%d KB", totalBytes/1024)
	}

	needsWarning := false
	var warningMsg string
	if sessionCount >= 35 {
		needsWarning = true
		warningMsg = fmt.Sprintf("ประวัติการแชทเริ่มสะสมเยอะ (%d เซสชัน, %s) อาจส่งผลให้การโหลดช้าลง", sessionCount, sizeFormatted)
	} else if totalBytes >= 30*1024*1024 {
		needsWarning = true
		warningMsg = fmt.Sprintf("ขนาดแคชประวัติแชทสะสม %s ซึ่งค่อนข้างใหญ่ แนะนำให้ลบประวัติเก่าเพื่อคืนพื้นที่", sizeFormatted)
	}

	return CacheStats{
		TotalSessions:  sessionCount,
		TotalSizeBytes: totalBytes,
		SizeFormatted:  sizeFormatted,
		NeedsWarning:   needsWarning,
		WarningMessage: warningMsg,
	}
}

// ClearOldSessions removes old sessions while preserving the most recent keepCount sessions
func (a *App) ClearOldSessions(projectPath string, keepCount int) int {
	if keepCount < 1 {
		keepCount = 10
	}
	sessions := a.GetGrokSessions(projectPath)
	if len(sessions) <= keepCount {
		return 0
	}

	sessionDir := a.findGrokSessionDir(projectPath)
	if sessionDir == "" {
		return 0
	}

	deleted := 0
	for i := keepCount; i < len(sessions); i++ {
		subDir := filepath.Join(sessionDir, sessions[i].ID)
		if err := os.RemoveAll(subDir); err == nil {
			deleted++
		}
	}
	return deleted
}

// PruneExpiredSessions backwards-compatible stub
func (a *App) PruneExpiredSessions(projectPath string) int {
	return 0
}

// LoadGrokSessionMessages reads the real chat messages for a specific session (Grok or stored backend session)
func (a *App) LoadGrokSessionMessages(projectPath string, sessionID string) []ChatMessageItem {
	var messages []ChatMessageItem

	sessionDir := a.findGrokSessionDir(projectPath)
	if sessionDir != "" {
		chatPath := filepath.Join(sessionDir, sessionID, "chat_history.jsonl")
		if f, err := os.Open(chatPath); err == nil {
			defer f.Close()

			userQueryRegex := regexp.MustCompile(`(?s)<user_query>\s*(.*?)\s*</user_query>`)

			scanner := bufio.NewScanner(f)
			buf := make([]byte, 1024*1024)
			scanner.Buffer(buf, 10*1024*1024)

			msgIdx := 0

			for scanner.Scan() {
				line := scanner.Bytes()
				if len(line) == 0 {
					continue
				}

				var raw struct {
					Type    string          `json:"type"`
					Content json.RawMessage `json:"content"`
				}
				if err := json.Unmarshal(line, &raw); err != nil {
					continue
				}

				if raw.Type == "user" {
					var text string
					var strContent string
					if err := json.Unmarshal(raw.Content, &strContent); err == nil {
						text = strContent
					} else {
						var blocks []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						}
						if err := json.Unmarshal(raw.Content, &blocks); err == nil {
							for _, b := range blocks {
								if b.Type == "text" {
									if m := userQueryRegex.FindStringSubmatch(b.Text); len(m) > 1 {
										text = strings.TrimSpace(m[1])
										break
									} else if !strings.HasPrefix(b.Text, "<user_info>") && !strings.HasPrefix(b.Text, "<system-reminder>") {
										text = strings.TrimSpace(b.Text)
									}
								}
							}
						}
					}

					if text != "" {
						msgIdx++
						messages = append(messages, ChatMessageItem{
							ID:        fmt.Sprintf("grok_%s_%d", sessionID, msgIdx),
							Sender:    "user",
							Text:      text,
							Timestamp: time.Now().Format("15:04"),
						})
					}
				} else if raw.Type == "assistant" {
					var text string
					var strContent string
					if err := json.Unmarshal(raw.Content, &strContent); err == nil {
						text = strings.TrimSpace(strContent)
					} else {
						var blocks []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						}
						if err := json.Unmarshal(raw.Content, &blocks); err == nil {
							var parts []string
							for _, b := range blocks {
								if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
									parts = append(parts, strings.TrimSpace(b.Text))
								}
							}
							text = strings.Join(parts, "\n\n")
						}
					}

					if text != "" {
						msgIdx++
						messages = append(messages, ChatMessageItem{
							ID:      fmt.Sprintf("grok_%s_%d", sessionID, msgIdx),
							Sender:  "assistant",
							Text:    text,
							Thought: "Thought for a few seconds",
							Actions: []AgentAction{
								{
									Type:  "status",
									Label: "Finished turn",
									Icon:  "✓",
								},
							},
							Timestamp: time.Now().Format("15:04"),
						})
					}
				}
			}
		}
	}

	// If Grok history wasn't found or was empty, load from backend durable session store
	if len(messages) == 0 {
		var stored StoredSession
		foundStored := false

		safeID := sessionIDSafe.ReplaceAllString(sessionID, "_") + ".json"
		candidatePaths := []string{
			sessionLogPath(projectPath, sessionID),
			sessionLogPath(a.currentFolder, sessionID),
			sessionLogPath("", sessionID),
			filepath.Join(sentDataDir(), "sessions", safeID),
		}
		if pEntries, err := os.ReadDir(filepath.Join(sentDataDir(), "projects")); err == nil {
			for _, pe := range pEntries {
				if pe.IsDir() {
					candidatePaths = append(candidatePaths, filepath.Join(sentDataDir(), "projects", pe.Name(), "sessions", safeID))
				}
			}
		}

		for _, p := range candidatePaths {
			if b, err := os.ReadFile(p); err == nil {
				var s StoredSession
				if json.Unmarshal(b, &s) == nil && len(s.Turns) > 0 {
					stored = s
					foundStored = true
					break
				}
			}
		}

		if foundStored && len(stored.Turns) > 0 {
			for idx, t := range stored.Turns {
				sender := t.Role
				if sender == "" {
					sender = "user"
				}
				timeStr := ""
				if t.Timestamp != "" {
					if pt, err := time.Parse(time.RFC3339, t.Timestamp); err == nil {
						timeStr = pt.Format("15:04")
					}
				}
				if timeStr == "" {
					timeStr = time.Now().Format("15:04")
				}
				item := ChatMessageItem{
					ID:        fmt.Sprintf("msg_%s_%d", sessionID, idx+1),
					Sender:    sender,
					Text:      t.Content,
					Timestamp: timeStr,
					Model:     t.Model,
				}
				if sender == "assistant" {
					item.Thought = "Completed"
					if t.Model != "" {
						item.Thought = "Answered by " + t.Model
					}
					item.Actions = []AgentAction{
						{
							Type:  "status",
							Label: "Finished turn",
							Icon:  "✓",
						},
					}
				}
				messages = append(messages, item)
			}
		}
	}

	return messages
}

func pluralInt(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// GetGrokQuotaUsage reads the signed-in account's allowance from grok.com.
// projectPath is unused; the Wails binding keeps the original signature.
func (a *App) GetGrokQuotaUsage(projectPath string) GrokQuotaUsage {
	return a.fetchGrokQuota(time.Now())
}
