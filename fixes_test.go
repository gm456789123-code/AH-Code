package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSaveBase64ImageExtension verifies the extension is derived from the MIME type.
func TestSaveBase64ImageExtension(t *testing.T) {
	app := &App{}
	payload := base64.StdEncoding.EncodeToString([]byte("fake-jpeg-data"))

	path, err := app.SaveBase64Image("data:image/jpeg;base64," + payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	if !strings.HasSuffix(path, ".jpg") {
		t.Errorf("expected .jpg extension, got %s", filepath.Ext(path))
	}
}

// TestSaveBase64ImageInvalidURI verifies that a missing comma returns an error.
func TestSaveBase64ImageInvalidURI(t *testing.T) {
	app := &App{}
	_, err := app.SaveBase64Image("data:image/png;base64")
	if err == nil {
		t.Error("expected error for missing comma in data URI")
	}
}

// TestSaveBase64ImageTooBig verifies the size cap is enforced.
func TestSaveBase64ImageTooBig(t *testing.T) {
	app := &App{}
	// Build a payload that clearly exceeds the pre-decode check.
	huge := strings.Repeat("A", (maxSavedImageBytes/3)*4+100)
	_, err := app.SaveBase64Image("data:image/png;base64," + huge)
	if err == nil {
		t.Error("expected error for oversized payload")
	}
}

// TestSaveBase64ImageCacheCleanup verifies old files are evicted.
func TestSaveBase64ImageCacheCleanup(t *testing.T) {
	app := &App{}
	cacheDir := filepath.Join(os.TempDir(), "sent-code-cache")
	_ = os.MkdirAll(cacheDir, 0755)

	// Plant a stale file (2 hours old).
	stale := filepath.Join(cacheDir, "img_stale_test.png")
	_ = os.WriteFile(stale, []byte("old"), 0644)
	oldTime := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(stale, oldTime, oldTime)

	payload := base64.StdEncoding.EncodeToString([]byte("fresh-image"))
	path, err := app.SaveBase64Image("data:image/png;base64," + payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale cache file should have been removed")
	}
}

// TestRunBuildCommandTimeout verifies that a hanging command is killed within the timeout.
func TestRunBuildCommandTimeout(t *testing.T) {
	app := &App{currentFolder: t.TempDir()}
	old := runBuildTimeout
	runBuildTimeout = 300 * time.Millisecond
	defer func() { runBuildTimeout = old }()

	// Use a command that actually exits when the context is cancelled.
	// On Windows, powershell Start-Sleep with a very long duration is killed by exec.CommandContext.
	// We avoid ping -t because its pipe reader goroutine hangs even after process kill.
	result := app.RunBuildCommand(app.currentFolder, "Start-Sleep -Seconds 60")
	if result.Success {
		t.Error("expected command to fail due to timeout")
	}
	if !strings.Contains(result.Stdout, "timed out") {
		t.Errorf("expected timeout message in output, got: %s", result.Stdout)
	}
}

// TestRunBuildCommandStoppableViaStopActiveChat verifies StopActiveChat aborts a running
// build command (and its process tree) instead of letting it run to completion.
func TestRunBuildCommandStoppableViaStopActiveChat(t *testing.T) {
	app := &App{currentFolder: t.TempDir()}

	done := make(chan CommandResult, 1)
	go func() {
		done <- app.RunBuildCommand(app.currentFolder, "Start-Sleep -Seconds 120; echo finished")
	}()

	// Wait until the command is registered so StopActiveChat can see it.
	deadline := time.Now().Add(15 * time.Second)
	for {
		app.activeChatCmdMu.Lock()
		registered := app.activeBuildCmd != nil && app.activeBuildCmd.Process != nil
		app.activeChatCmdMu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("build command was never registered for StopActiveChat")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !app.StopActiveChat() {
		t.Error("StopActiveChat should report that a running command was stopped")
	}

	select {
	case r := <-done:
		if r.Success {
			t.Errorf("stopped command must not report success: %+v", r)
		}
		if !strings.Contains(r.Stdout, "stopped") {
			t.Errorf("expected stopped note in output, got: %s", r.Stdout)
		}
		if strings.Contains(r.Stdout, "finished") {
			t.Error("command ran to completion despite being stopped")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RunBuildCommand did not return after StopActiveChat")
	}
}

// TestRunBuildCommandPathTraversal verifies a folder outside the project is rejected.
func TestRunBuildCommandPathTraversal(t *testing.T) {
	projectDir := t.TempDir()
	app := &App{currentFolder: projectDir}

	result := app.RunBuildCommand(os.TempDir(), "echo hi")
	if result.Success {
		t.Error("expected rejection for folder outside project")
	}
	if !strings.Contains(result.Stdout, "Rejected") {
		t.Errorf("expected Rejected message, got: %s", result.Stdout)
	}
}

// TestRunBuildCommandOutputCap verifies output is capped at maxBuildOutputBytes (tail).
func TestRunBuildCommandOutputCap(t *testing.T) {
	projectDir := t.TempDir()
	app := &App{currentFolder: projectDir}

	// Generate a command that produces > 16KB of output.
	result := app.RunBuildCommand(projectDir, `python -c "print('X'*20000)"`)
	if len(result.Stdout) > maxBuildOutputBytes+1024 {
		t.Errorf("output not capped: got %d bytes", len(result.Stdout))
	}
}

// TestListIgnoredDirNamesWithGitignore verifies that dirs listed in .gitignore are excluded.
func TestListIgnoredDirNamesWithGitignore(t *testing.T) {
	dir := t.TempDir()
	gitignore := "dist/\nbuild/\ntarget\n# comment\n*.log\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0644); err != nil {
		t.Fatal(err)
	}

	ignored := listIgnoredDirNames(dir)
	for _, name := range []string{"dist", "build", "target", "node_modules"} {
		if !ignored[name] {
			t.Errorf("expected %q to be in ignored set", name)
		}
	}
	// Wildcard patterns like *.log should NOT be in the map (can't match dir names exactly).
	if ignored["*.log"] {
		t.Error("wildcard pattern *.log should not be in ignored dirs map")
	}
}

// TestListIgnoredDirNamesNoGitignore verifies the fallback baseline when .gitignore is absent.
func TestListIgnoredDirNamesNoGitignore(t *testing.T) {
	dir := t.TempDir() // no .gitignore
	ignored := listIgnoredDirNames(dir)
	for _, name := range []string{"dist", "build", "target", "node_modules"} {
		if !ignored[name] {
			t.Errorf("expected fallback to include %q", name)
		}
	}
}

// TestListProjectFilesShowsBuildWhenNotInGitignore verifies that a "build" source directory is
// visible when the project's .gitignore does NOT list it.
func TestListProjectFilesShowsBuildWhenNotInGitignore(t *testing.T) {
	dir := t.TempDir()
	// .gitignore that does not mention "build".
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules\n"), 0644)
	_ = os.Mkdir(filepath.Join(dir, "build"), 0755) // user source folder named "build"
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)

	app := &App{currentFolder: dir}
	items, err := app.ListProjectFiles(dir)
	if err != nil {
		t.Fatalf("ListProjectFiles error: %v", err)
	}

	names := map[string]bool{}
	for _, item := range items {
		names[item.Name] = true
	}
	if !names["build"] {
		t.Error("build directory should be visible when it is not in .gitignore")
	}
	if !names["main.go"] {
		t.Error("main.go should be visible")
	}
}
