package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const ollamaReleaseAPI = "https://api.github.com/repos/ollama/ollama/releases/latest"

// EngineStatus describes the local inference sidecar (Ollama) state
type EngineStatus struct {
	Supported  bool   `json:"supported"`
	Installed  bool   `json:"installed"`
	Running    bool   `json:"running"`
	Installing bool   `json:"installing"`
	Version    string `json:"version"`
}

// EngineProgress is emitted to the frontend as "engine-install-progress"
type EngineProgress struct {
	Stage      string  `json:"stage"` // "resolving", "downloading", "verifying", "extracting", "done", "error", "cancelled"
	Percent    float64 `json:"percent"`
	Downloaded int64   `json:"downloaded"`
	Total      int64   `json:"total"`
	Message    string  `json:"message"`
}

// Engine manages the private Ollama sidecar. It listens on a random loopback port
// and keeps its model store inside the Sent-Code data directory, so it never
// shares state with an Ollama the user installed separately.
type Engine struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	port          int
	installing    bool
	installCancel context.CancelFunc
}

// sentDataDir returns (and creates) the per-user Sent-Code data directory
func sentDataDir() string {
	var base string
	if runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
	}
	if base == "" {
		if d, err := os.UserConfigDir(); err == nil {
			base = d
		} else {
			base = os.TempDir()
		}
	}
	dir := filepath.Join(base, "Sent-Code")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

func engineDir() string { return filepath.Join(sentDataDir(), "engine", "ollama") }
func modelsDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		userOllama := filepath.Join(home, ".ollama", "models")
		if _, err := os.Stat(userOllama); err == nil {
			return userOllama
		}
	}
	return filepath.Join(sentDataDir(), "engine", "models")
}
func isSystemOllamaRunning() bool {
	client := &http.Client{Timeout: 600 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:11434/api/version")
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		return true
	}
	return false
}

func ollamaBinary() string {
	if p, err := exec.LookPath("ollama"); err == nil {
		return p
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		sysPath := filepath.Join(localAppData, "Programs", "Ollama", "ollama.exe")
		if _, err := os.Stat(sysPath); err == nil {
			return sysPath
		}
	}
	name := "ollama"
	if runtime.GOOS == "windows" {
		name = "ollama.exe"
	}
	return filepath.Join(engineDir(), name)
}

func engineSupported() bool { return runtime.GOOS == "windows" }

// maskError returns clear, actionable error messages instead of masking everything as 502.
func maskError(err error) string {
	if err == nil {
		return "Service gateway unavailable (ERR_CONNECTION_REFUSED: 502)."
	}
	msg := err.Error()
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "No connection could be made") {
		return "⚠️ ไม่สามารถเชื่อมต่อกับบริการ AI ได้ (Connection Refused)\nโปรดตรวจสอบว่า Ollama กำลังทำงาน หรือหากใช้งาน Cloud GPU ให้ตรวจดูว่าลิงก์ Colab ยังทำงานอยู่ครับ"
	}
	if strings.Contains(msg, "not found") {
		return fmt.Sprintf("⚠️ ไม่พบโมเดลที่เลือก: %s\nโปรดตรวจสอบว่าได้ดาวน์โหลดโมเดลเข้าเครื่องหรือบน Cloud GPU แล้ว", msg)
	}
	return fmt.Sprintf("⚠️ เกิดข้อผิดพลาดในการประมวลผล: %s", msg)
}

// Remote Ollama server configuration (e.g. Google Colab GPU / A100 via Cloudflare tunnel)
var (
	remoteOllamaMu  sync.RWMutex
	remoteOllamaURL string
)

type RemoteOllamaStatus struct {
	Configured bool     `json:"configured"`
	Connected  bool     `json:"connected"`
	URL        string   `json:"url"`
	Models     []string `json:"models"`
	Error      string   `json:"error,omitempty"`
}

func remoteOllamaConfigPath() string {
	return filepath.Join(sentDataDir(), "remote_ollama.txt")
}

func getActiveRemoteOllamaURL() string {
	remoteOllamaMu.RLock()
	defer remoteOllamaMu.RUnlock()
	return remoteOllamaURL
}

func setActiveRemoteOllamaURL(u string) {
	remoteOllamaMu.Lock()
	defer remoteOllamaMu.Unlock()
	remoteOllamaURL = u
}

func loadRemoteOllamaURL() string {
	b, err := os.ReadFile(remoteOllamaConfigPath())
	if err == nil {
		u := strings.TrimSpace(string(b))
		u = strings.TrimRight(u, "/")
		setActiveRemoteOllamaURL(u)
		return u
	}
	return ""
}

// GetRemoteOllamaStatus returns current remote connection info
func (a *App) GetRemoteOllamaStatus() RemoteOllamaStatus {
	u := getActiveRemoteOllamaURL()
	if u == "" {
		return RemoteOllamaStatus{Configured: false, Connected: false}
	}
	return a.TestRemoteOllamaURL(u)
}

// SetRemoteOllamaURL updates or clears the remote server URL
func (a *App) SetRemoteOllamaURL(rawURL string) (RemoteOllamaStatus, error) {
	u := strings.TrimSpace(rawURL)
	u = strings.TrimRight(u, "/")
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	setActiveRemoteOllamaURL(u)
	_ = os.WriteFile(remoteOllamaConfigPath(), []byte(u), 0644)
	if u == "" {
		return RemoteOllamaStatus{Configured: false, Connected: false}, nil
	}
	st := a.TestRemoteOllamaURL(u)
	return st, nil
}

// TestRemoteOllamaURL verifies connectivity to an Ollama server URL (e.g. cloudflared tunnel)
func (a *App) TestRemoteOllamaURL(rawURL string) RemoteOllamaStatus {
	u := strings.TrimSpace(rawURL)
	u = strings.TrimRight(u, "/")
	if u == "" {
		return RemoteOllamaStatus{Configured: false, Connected: false}
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}

	st := RemoteOllamaStatus{
		Configured: true,
		URL:        u,
	}

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get(u + "/api/tags")
	if err != nil {
		st.Connected = false
		st.Error = err.Error()
		return st
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		st.Connected = false
		st.Error = fmt.Sprintf("HTTP %d from remote server", resp.StatusCode)
		return st
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err == nil {
		for _, m := range tagsResp.Models {
			st.Models = append(st.Models, m.Name)
		}
	}
	st.Connected = true
	return st
}

func (e *Engine) baseURL() string {
	if remote := getActiveRemoteOllamaURL(); remote != "" {
		return remote
	}
	if isSystemOllamaRunning() {
		return "http://127.0.0.1:11434"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return fmt.Sprintf("http://127.0.0.1:%d", e.port)
}

func (e *Engine) isRunningLocked() bool {
	if isSystemOllamaRunning() {
		return true
	}
	// port is reset to 0 by the reaper goroutine when the process exits
	return e.cmd != nil && e.port != 0
}

// version queries the running sidecar; empty string when unreachable
func (e *Engine) version(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/api/version", e.baseURL())
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	if json.NewDecoder(resp.Body).Decode(&v) != nil {
		return ""
	}
	return v.Version
}

// Ensure starts the sidecar if it is not already running and waits until it answers
func (e *Engine) Ensure(ctx context.Context) error {
	if isSystemOllamaRunning() {
		return nil
	}
	if !engineSupported() {
		return fmt.Errorf("local engine is not supported on %s yet", runtime.GOOS)
	}
	bin := ollamaBinary()
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("engine not installed")
	}

	e.mu.Lock()
	if e.isRunningLocked() {
		e.mu.Unlock()
		return nil
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		e.mu.Unlock()
		return err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	_ = os.MkdirAll(modelsDir(), 0755)
	cmd := exec.Command(bin, "serve")
	hideWindow(cmd)
	cmd.Dir = engineDir()
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("OLLAMA_HOST=127.0.0.1:%d", port),
		"OLLAMA_MODELS="+modelsDir(),
		"OLLAMA_KEEP_ALIVE=10m",
		"OLLAMA_NOHISTORY=1",
		"OLLAMA_VULKAN=0",
		"CUDA_VISIBLE_DEVICES=0",
	)
	if err := cmd.Start(); err != nil {
		e.mu.Unlock()
		return err
	}
	e.cmd = cmd
	e.port = port
	e.mu.Unlock()

	// Reap the process so ProcessState is set when it exits
	go func() {
		_ = cmd.Wait()
		e.mu.Lock()
		if e.cmd == cmd {
			e.port = 0
		}
		e.mu.Unlock()
	}()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.version(ctx) != "" {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	e.Stop()
	return fmt.Errorf("engine did not become ready")
}

// CancelInstallOnExit aborts an in-flight engine download when the app closes
func (e *Engine) CancelInstallOnExit() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.installCancel != nil {
		e.installCancel()
	}
}

// Stop terminates the sidecar and everything it spawned
func (e *Engine) Stop() {
	e.mu.Lock()
	cmd := e.cmd
	e.cmd = nil
	e.port = 0
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
		hideWindow(kill)
		_ = kill.Run()
	} else {
		_ = cmd.Process.Kill()
	}
}

// resolveOllamaAsset asks GitHub for the latest standalone Windows build
func resolveOllamaAsset(ctx context.Context) (url string, size int64, sha string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ollamaReleaseAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Sent-Code")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", 0, "", fmt.Errorf("release lookup returned %d", resp.StatusCode)
	}
	var rel struct {
		Assets []struct {
			Name               string `json:"name"`
			Size               int64  `json:"size"`
			Digest             string `json:"digest"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", 0, "", err
	}
	for _, a := range rel.Assets {
		if a.Name == "ollama-windows-amd64.zip" {
			return a.BrowserDownloadURL, a.Size, strings.TrimPrefix(a.Digest, "sha256:"), nil
		}
	}
	return "", 0, "", fmt.Errorf("no compatible engine build found in latest release")
}

func (a *App) emitEngine(p EngineProgress) {
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "engine-install-progress", p)
	}
}

// GetEngineStatus reports whether the local inference sidecar is installed/running
func (a *App) GetEngineStatus() EngineStatus {
	st := EngineStatus{Supported: engineSupported()}
	if isSystemOllamaRunning() {
		st.Installed = true
		st.Running = true
		st.Version = a.engine.version(context.Background())
		return st
	}
	if _, err := os.Stat(ollamaBinary()); err == nil {
		st.Installed = true
	}
	a.engine.mu.Lock()
	st.Installing = a.engine.installing
	a.engine.mu.Unlock()
	if v := a.engine.version(context.Background()); v != "" {
		st.Running = true
		st.Version = v
	}
	return st
}

// InstallEngine downloads and unpacks the Ollama sidecar in the background.
// Progress arrives through "engine-install-progress" events.
func (a *App) InstallEngine() error {
	if !engineSupported() {
		return fmt.Errorf("local engine is not supported on this platform yet")
	}
	a.engine.mu.Lock()
	if a.engine.installing {
		a.engine.mu.Unlock()
		return fmt.Errorf("install already in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.engine.installing = true
	a.engine.installCancel = cancel
	a.engine.mu.Unlock()

	go func() {
		err := a.installEngine(ctx)
		a.engine.mu.Lock()
		a.engine.installing = false
		a.engine.installCancel = nil
		a.engine.mu.Unlock()
		cancel()

		switch {
		case err == nil:
			a.emitEngine(EngineProgress{Stage: "done", Percent: 100, Message: "Engine ready"})
		case ctx.Err() != nil:
			a.emitEngine(EngineProgress{Stage: "cancelled", Message: "Installation cancelled"})
		default:
			fmt.Fprintln(os.Stderr, "engine install failed:", err)
			a.emitEngine(EngineProgress{Stage: "error", Message: maskError(err)})
		}
	}()
	return nil
}

// CancelEngineInstall aborts a running engine download
func (a *App) CancelEngineInstall() bool {
	a.engine.mu.Lock()
	defer a.engine.mu.Unlock()
	if a.engine.installCancel != nil {
		a.engine.installCancel()
		return true
	}
	return false
}

func (a *App) installEngine(ctx context.Context) error {
	a.emitEngine(EngineProgress{Stage: "resolving", Message: "Contacting update service..."})
	url, size, wantSHA, err := resolveOllamaAsset(ctx)
	if err != nil {
		return err
	}

	root := filepath.Join(sentDataDir(), "engine")
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	// The partial file is named after the release digest, so a newer release never resumes an older download.
	// It is kept on failure/cancel so the next attempt continues where this one stopped.
	tag := "latest"
	if len(wantSHA) >= 12 {
		tag = wantSHA[:12]
	}
	zipPath := filepath.Join(root, "engine-"+tag+".zip.part")

	// 1. Download (resumable)
	gotSHA, err := downloadResumable(ctx, url, zipPath, size, func(done int64) {
		pct := 0.0
		if size > 0 {
			pct = float64(done) * 90 / float64(size) // download = first 90%
		}
		a.emitEngine(EngineProgress{Stage: "downloading", Percent: pct, Downloaded: done, Total: size, Message: "Downloading engine..."})
	})
	if err != nil {
		return err
	}

	// 2. Verify integrity
	a.emitEngine(EngineProgress{Stage: "verifying", Percent: 92, Downloaded: size, Total: size, Message: "Verifying download..."})
	if wantSHA != "" && !strings.EqualFold(gotSHA, wantSHA) {
		_ = os.Remove(zipPath) // corrupt: start fresh next time
		return fmt.Errorf("checksum mismatch")
	}
	defer os.Remove(zipPath)

	// 3. Extract into a temp dir, then swap in
	a.emitEngine(EngineProgress{Stage: "extracting", Percent: 95, Message: "Unpacking engine..."})
	if a.engine.version(ctx) != "" {
		a.engine.Stop()
	}
	tmp := engineDir() + ".new"
	_ = os.RemoveAll(tmp)
	if err := unzipSafe(zipPath, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if _, err := os.Stat(filepath.Join(tmp, filepath.Base(ollamaBinary()))); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("archive did not contain the engine binary")
	}
	_ = os.RemoveAll(engineDir())
	return os.Rename(tmp, engineDir())
}

// downloadResumable fetches url into path, continuing from any bytes already there (HTTP Range),
// and returns the SHA-256 of the complete file. onProgress receives the total bytes on disk so far.
func downloadResumable(ctx context.Context, url, path string, size int64, onProgress func(done int64)) (string, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	var have int64
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		if size > 0 && st.Size() > size {
			_ = f.Truncate(0) // longer than the release: garbage
		} else if _, err := io.Copy(hasher, f); err != nil { // leaves f positioned at the end
			return "", err
		} else {
			have = st.Size()
		}
	}

	if size <= 0 || have < size {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("User-Agent", "Sent-Code")
		if have > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		switch {
		case have > 0 && resp.StatusCode == http.StatusPartialContent:
			// continue appending
		case resp.StatusCode == http.StatusOK:
			// server ignored Range (or fresh start): begin again from zero
			if have > 0 {
				if err := f.Truncate(0); err != nil {
					return "", err
				}
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return "", err
				}
				hasher.Reset()
				have = 0
			}
		default:
			return "", fmt.Errorf("download returned %d", resp.StatusCode)
		}

		buf := make([]byte, 256*1024)
		lastEmit := time.Time{}
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := f.Write(buf[:n]); werr != nil {
					return "", werr
				}
				hasher.Write(buf[:n])
				have += int64(n)
				if time.Since(lastEmit) > 150*time.Millisecond {
					lastEmit = time.Now()
					onProgress(have)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return "", rerr
			}
		}
	}
	if size > 0 && have != size {
		return "", fmt.Errorf("download incomplete (%d of %d bytes)", have, size)
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// unzipSafe extracts a zip while rejecting paths that escape dest (zip-slip)
func unzipSafe(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range r.File {
		target := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanDest) && filepath.Clean(target) != filepath.Clean(dest) {
			return fmt.Errorf("illegal path in archive")
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// UninstallEngine stops and removes the sidecar binary (downloaded models are kept)
func (a *App) UninstallEngine() error {
	a.engine.Stop()
	return os.RemoveAll(engineDir())
}
