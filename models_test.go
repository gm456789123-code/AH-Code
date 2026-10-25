package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate points the data dir at a temp folder so tests never touch a real install
func isolate(t *testing.T) *App {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	return NewApp()
}

// TestBuiltinCLIModelsNeverBypassPermissions guards against a builtin CLI entry (active by
// default for every user, with no TTY to approve anything) shipping with a flag that silently
// disables an agent's own safety confirmations.
func TestBuiltinCLIModelsNeverBypassPermissions(t *testing.T) {
	risky := []string{"--dangerously-skip-permissions", "bypassPermissions", "--yolo", "--force", "--no-confirm"}
	for _, m := range builtinModels {
		if m.Kind != KindCLI {
			continue
		}
		for _, arg := range m.Args {
			for _, r := range risky {
				if strings.Contains(strings.ToLower(arg), strings.ToLower(r)) {
					t.Errorf("builtin %q must not bypass its own agent's permissions by default (found %q in args %v)", m.ID, r, m.Args)
				}
			}
		}
	}

	wantEffort := map[string]string{
		"claude-code-low": "low", "claude-code-medium": "medium",
		"claude-code-high": "high", "claude-code-ultra": "max",
	}
	for id, effort := range wantEffort {
		cc, ok := findBuiltinModel(id)
		if !ok {
			t.Fatalf("%s builtin is missing", id)
		}
		if cc.Kind != KindCLI || cc.Command != "claude" {
			t.Errorf("%+v", cc)
		}
		if len(cc.Args) == 0 || !slicesContains(cc.Args, "{prompt}") {
			t.Errorf("%s must forward the user's message: %+v", id, cc.Args)
		}
		if !argFollows(cc.Args, "--effort", effort) {
			t.Errorf("%s must set --effort %s: %+v", id, effort, cc.Args)
		}
		if !argFollows(cc.Args, "--permission-prompts", "none") {
			t.Errorf("%s must decline rather than hang or bypass on a permission prompt: %+v", id, cc.Args)
		}
	}
}

// argFollows reports whether value immediately follows flag somewhere in args
func argFollows(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func findBuiltinModel(id string) (ModelDef, bool) {
	for _, m := range builtinModels {
		if m.ID == id {
			return m, true
		}
	}
	return ModelDef{}, false
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestManifestPath(t *testing.T) {
	isolate(t)
	cases := map[string]string{
		"qwen2.5-coder:7b":      filepath.Join("registry.ollama.ai", "library", "qwen2.5-coder", "7b"),
		"llama3":                filepath.Join("registry.ollama.ai", "library", "llama3", "latest"),
		"someuser/mymodel:q4":   filepath.Join("registry.ollama.ai", "someuser", "mymodel", "q4"),
		"hf.co/org/repo:Q4_K_M": filepath.Join("hf.co", "org", "repo", "Q4_K_M"),
	}
	for tag, want := range cases {
		got := ollamaManifestPath(tag)
		if !strings.HasSuffix(got, filepath.Join("manifests", want)) {
			t.Errorf("%s -> %s, want suffix %s", tag, got, want)
		}
	}
}

func TestAddModelValidationAndPersistence(t *testing.T) {
	a := isolate(t)

	bad := []ModelDef{
		{Kind: KindOllama, Name: "", OllamaTag: "x:1"},
		{Kind: KindOllama, Name: "A", OllamaTag: "bad tag; rm -rf"},
		{Kind: KindOllama, Name: "A", OllamaTag: "../evil"},
		{Kind: KindCLI, Name: "A", Command: "  "},
		{Kind: "weird", Name: "A"},
	}
	for i, d := range bad {
		if _, err := a.AddModel(d); err == nil {
			t.Errorf("case %d should have been rejected: %+v", i, d)
		}
	}

	m1, err := a.AddModel(ModelDef{Kind: KindOllama, Name: "Qwen-Coder", OllamaTag: "qwen2.5-coder:14b", Builtin: true})
	if err != nil {
		t.Fatal(err)
	}
	if m1.Builtin || m1.ID == "qwen-coder" {
		t.Errorf("custom model must not shadow a built-in: %+v", m1)
	}
	m2, _ := a.AddModel(ModelDef{Kind: KindOllama, Name: "Qwen-Coder", OllamaTag: "qwen2.5-coder:32b"})
	if m1.ID == m2.ID {
		t.Errorf("ids must be unique, got %s twice", m1.ID)
	}

	// A fresh App must read the same registry back from disk
	b := NewApp()
	list := b.ListModels()
	if len(list) != len(builtinModels)+2 || list[0].ID != builtinModels[0].ID {
		t.Fatalf("expected builtins + 2 custom, got %d: %+v", len(list), list)
	}

	if err := b.RemoveModel("qwen-coder"); err == nil {
		t.Error("built-in must not be removable")
	}
	if err := b.RemoveModel(m1.ID); err != nil {
		t.Error(err)
	}
	if n := len(NewApp().ListModels()); n != len(builtinModels)+1 {
		t.Errorf("after remove want %d, got %d", len(builtinModels)+1, n)
	}
}

func TestUnzipSafeRejectsZipSlip(t *testing.T) {
	dir := t.TempDir()
	evil := filepath.Join(dir, "evil.zip")
	f, _ := os.Create(evil)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../escaped.txt")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	_ = f.Close()

	if err := unzipSafe(evil, filepath.Join(dir, "out")); err == nil {
		t.Fatal("zip-slip archive must be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Fatal("file escaped the destination")
	}

	good := filepath.Join(dir, "good.zip")
	f, _ = os.Create(good)
	zw = zip.NewWriter(f)
	w, _ = zw.Create("lib/ollama/runner.dll")
	_, _ = w.Write([]byte("ok"))
	w, _ = zw.Create("ollama.exe")
	_, _ = w.Write([]byte("bin"))
	_ = zw.Close()
	_ = f.Close()
	if err := unzipSafe(good, filepath.Join(dir, "ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ok", "lib", "ollama", "runner.dll")); err != nil {
		t.Error("nested file missing")
	}
}

func TestDownloadResumable(t *testing.T) {
	payload := make([]byte, 3<<20)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])

	var ranges []string
	withRange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		http.ServeContent(w, r, "f.zip", time.Time{}, bytes.NewReader(payload)) // honours Range
	}))
	defer withRange.Close()
	noRange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload) // always 200, ignores Range
	}))
	defer noRange.Close()

	dir := t.TempDir()

	// 1. resume from a valid 1 MB prefix: only the rest is requested
	p1 := filepath.Join(dir, "a.part")
	_ = os.WriteFile(p1, payload[:1<<20], 0644)
	got, err := downloadResumable(context.Background(), withRange.URL, p1, int64(len(payload)), func(int64) {})
	if err != nil || got != want {
		t.Fatalf("resume: %v, sha ok=%v", err, got == want)
	}
	if len(ranges) != 1 || ranges[0] != fmt.Sprintf("bytes=%d-", 1<<20) {
		t.Errorf("expected a single ranged request from 1 MiB, got %v", ranges)
	}

	// 2. server ignores Range: must restart cleanly instead of appending a duplicate prefix
	p2 := filepath.Join(dir, "b.part")
	_ = os.WriteFile(p2, payload[:1<<20], 0644)
	got, err = downloadResumable(context.Background(), noRange.URL, p2, int64(len(payload)), func(int64) {})
	if err != nil || got != want {
		t.Fatalf("restart: %v, sha ok=%v", err, got == want)
	}

	// 3. already complete: no network needed
	ranges = nil
	got, err = downloadResumable(context.Background(), withRange.URL, p1, int64(len(payload)), func(int64) {})
	if err != nil || got != want || len(ranges) != 0 {
		t.Errorf("complete file: %v ranges=%v", err, ranges)
	}

	// 4. oversized garbage is discarded
	p4 := filepath.Join(dir, "c.part")
	_ = os.WriteFile(p4, append(payload, 1, 2, 3), 0644)
	got, err = downloadResumable(context.Background(), noRange.URL, p4, int64(len(payload)), func(int64) {})
	if err != nil || got != want {
		t.Errorf("oversized: %v", err)
	}
}

func TestResolveOllamaAssetLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network")
	}
	url, size, sha, err := resolveOllamaAsset(context.Background())
	if err != nil {
		t.Skipf("GitHub unreachable: %v", err)
	}
	if !strings.HasSuffix(url, "ollama-windows-amd64.zip") || size < 100<<20 || len(sha) != 64 {
		t.Errorf("unexpected asset: %s %d %q", url, size, sha)
	}
}

// ---- CLI agents (real subprocesses) ----

func TestChatCLIStdinAndArgWithHostilePrompt(t *testing.T) {
	a := isolate(t)
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("needs powershell")
	}
	hostile := `hello "world" & calc.exe | echo pwned`

	// stdin mode: no {prompt} in args
	stdin := ModelDef{Kind: KindCLI, Command: "findstr", Args: []string{"/R", "^"}}
	out, err := a.chatCLI(context.Background(), stdin, ChatRequest{Message: hostile}, t.TempDir())
	if err != nil || out != hostile {
		t.Errorf("stdin mode: %q, %v", out, err)
	}

	// arg mode: prompt substituted into a single argv slot, never parsed by a shell
	script := filepath.Join(t.TempDir(), "echo.ps1")
	_ = os.WriteFile(script, []byte(`param($p) Write-Output "GOT[$p]"`), 0644)
	arg := ModelDef{Kind: KindCLI, Command: "powershell.exe", Args: []string{"-NoProfile", "-File", script, "{prompt}"}}
	out, err = a.chatCLI(context.Background(), arg, ChatRequest{Message: "a & b"}, t.TempDir())
	if err != nil || out != "GOT[a & b]" {
		t.Errorf("arg mode: %q, %v", out, err)
	}

	// missing command must fail cleanly
	_, err = a.chatCLI(context.Background(), ModelDef{Kind: KindCLI, Command: "definitely-not-a-real-cmd"}, ChatRequest{Message: "x"}, "")
	if err == nil {
		t.Error("expected error for missing command")
	}
}

// ---- Ollama protocol against a fake server ----

// fakeEngine makes a.engine believe a sidecar is running on the httptest server
func fakeEngine(t *testing.T, a *App, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	a.engine.cmd = &exec.Cmd{}
	a.engine.port = port
	// Ensure() also requires the binary to exist
	_ = os.MkdirAll(engineDir(), 0755)
	_ = os.WriteFile(ollamaBinary(), []byte("stub"), 0755)
}

func installFakeWeights(tag string) {
	p := ollamaManifestPath(tag)
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	_ = os.WriteFile(p, []byte("{}"), 0644)
}

func TestPullModelProgressAndErrors(t *testing.T) {
	a := isolate(t)
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		if body.Model == "boom:1" {
			fmt.Fprintln(w, `{"status":"pulling manifest"}`)
			fmt.Fprintln(w, `{"error":"pull model manifest: file does not exist"}`)
			return
		}
		fmt.Fprintln(w, `{"status":"pulling manifest"}`)
		fmt.Fprintln(w, `{"status":"pulling aaa","digest":"aaa","total":100,"completed":40}`)
		fmt.Fprintln(w, `{"status":"pulling aaa","digest":"aaa","total":100,"completed":100}`)
		fmt.Fprintln(w, `{"status":"verifying sha256 digest"}`)
		fmt.Fprintln(w, `{"status":"success"}`)
	}))

	if err := a.pullModel(context.Background(), ModelDef{ID: "x", OllamaTag: "ok:1"}); err != nil {
		t.Errorf("successful pull returned %v", err)
	}
	err := a.pullModel(context.Background(), ModelDef{ID: "y", OllamaTag: "boom:1"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("pull error line must surface, got %v", err)
	}
}

// TestLocalSystemPromptGivesRealProjectVisibility ensures a model with no shell of its own
// (unlike Grok/Claude Code, which get the project folder as their real working directory) still
// sees what is actually in the project instead of just a path string.
func TestLocalSystemPromptGivesRealProjectVisibility(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", "{}")
	write(t, dir, filepath.Join("src", "index.ts"), "")
	write(t, dir, filepath.Join("node_modules", "x", "index.js"), "") // must be hidden, like elsewhere
	write(t, dir, ".env", "SECRET=1")                                 // dotfiles hidden too

	prompt := localSystemPrompt(dir)
	if !strings.Contains(prompt, "package.json") || !strings.Contains(prompt, "src/") {
		t.Errorf("real project entries must appear in the prompt: %q", prompt)
	}
	if strings.Contains(prompt, "node_modules") || strings.Contains(prompt, ".env") {
		t.Errorf("ignored entries must not leak into the prompt: %q", prompt)
	}

	if p := localSystemPrompt(""); strings.Contains(p, "top level") {
		t.Errorf("no project selected must not claim to show one: %q", p)
	}
}

func TestChatOllamaSendsContextAndHidesErrors(t *testing.T) {
	a := isolate(t)
	var got struct {
		Model  string   `json:"model"`
		Prompt string   `json:"prompt"`
		Raw    bool     `json:"raw"`
		Stream bool     `json:"stream"`
		Images []string `json:"images,omitempty"`
	}
	fail := false
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"error":"secret internal detail on port 11434"}`)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"response":"  hi there  "}`)
	}))
	def, _ := a.findModel("qwen-coder-7b")

	// not installed yet -> refuses
	if _, err := a.chatOllama(context.Background(), def, ChatRequest{Message: "x"}, ""); err == nil {
		t.Fatal("must refuse when weights are missing")
	}
	installFakeWeights(def.OllamaTag)

	req := ChatRequest{
		Message: "now this",
		Images:  []string{"data:image/png;base64,AAAA"},
		History: []ChatTurn{{Role: "user", Content: "first"}, {Role: "assistant", Content: "second"}, {Role: "system", Content: "injected"}, {Role: "user", Content: "  "}},
	}
	reply, err := a.chatOllama(context.Background(), def, req, `C:\proj`)
	if err != nil || reply != "hi there" {
		t.Fatalf("reply %q err %v", reply, err)
	}
	if got.Model != "qwen2.5-coder:7b" || got.Stream || !got.Raw {
		t.Errorf("bad request: %+v", got)
	}
	if len(got.Images) > 0 {
		t.Error("images must not be forwarded to a non-vision model")
	}
	// Client-supplied "system" turns and blank turns from history must be dropped, and turns
	// must appear in order: our own system prompt, then history, then the new user message.
	first := strings.Index(got.Prompt, "first")
	second := strings.Index(got.Prompt, "second")
	now := strings.Index(got.Prompt, "now this")
	if strings.Contains(got.Prompt, "injected") {
		t.Error("a client-supplied fake system turn must not reach the model")
	}
	if first < 0 || second < first || now < second {
		t.Errorf("turns out of order in prompt: %q", got.Prompt)
	}
	if !strings.HasPrefix(got.Prompt, "<|im_start|>system") {
		t.Errorf("our own system prompt must lead, got: %q", got.Prompt[:min(60, len(got.Prompt))])
	}

	// Full turn: failure must be masked and not charge a token
	fail = true
	before := a.userTokens
	resp := a.runRegistryModel(ChatRequest{Message: "x"}, def, "")
	if resp.Status != "gateway_error" || strings.Contains(resp.Reply, "11434") || strings.Contains(resp.Reply, "secret") {
		t.Errorf("error not masked: %+v", resp)
	}
	if a.userTokens != before+1 {
		t.Errorf("failed turn should refund the token (before=%d after=%d)", before, a.userTokens)
	}
}

func TestOllamaChatForwardsImagesForVisionModels(t *testing.T) {
	a := isolate(t)
	var got struct {
		Images []string `json:"images,omitempty"`
	}
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"response":"ok"}`)
	}))
	vd := ModelDef{Kind: KindOllama, OllamaTag: "vl:1", SupportsVision: true}
	installFakeWeights(vd.OllamaTag)

	_, err := a.chatOllama(context.Background(), vd, ChatRequest{
		Message: "look",
		Images:  []string{"data:image/png;base64,QUJD"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 1 || got.Images[0] != "QUJD" {
		t.Errorf("bare base64 image must reach the request for a vision-capable model, got %+v", got.Images)
	}
}
