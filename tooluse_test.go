package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParseToolCall(t *testing.T) {
	cases := []struct {
		in       string
		wantKind toolKind
		wantArg  string
		wantOK   bool
	}{
		{"RUN: npm test", toolRun, "npm test", true},
		{"  run:  go build ./...  ", toolRun, "go build ./...", true}, // case-insensitive, trims
		{"RUN:", toolNone, "", false},                                 // empty command
		{"RUN: ", toolNone, "", false},
		{"", toolNone, "", false},
		{"I think you should RUN: npm test", toolNone, "", false}, // not the whole reply
		// only the first line is ever the argument — trailing text (real or hallucinated by the
		// model) must never be smuggled into the shell command or search query.
		{"RUN: npm test\nand then check the output", toolRun, "npm test", true},
		{"RUN: type secret.txt\n\n Secret word: ABC123", toolRun, "type secret.txt", true},
		{"Here's the fix:\n\n```go\nfunc x() {}\n```", toolNone, "", false},
		{"SEARCH: golang context package", toolSearch, "golang context package", true},
		{"  search:  latest react version  ", toolSearch, "latest react version", true},
		{"SEARCH:", toolNone, "", false},
	}
	for _, c := range cases {
		kind, arg, ok := parseToolCall(c.in)
		if ok != c.wantOK || arg != c.wantArg || kind != c.wantKind {
			t.Errorf("parseToolCall(%q) = (%v, %q, %v), want (%v, %q, %v)", c.in, kind, arg, ok, c.wantKind, c.wantArg, c.wantOK)
		}
	}
}

func TestIsDangerousCommand(t *testing.T) {
	dangerous := []string{
		"rm -rf /",
		"rm -rf ~",
		"rm -rf $HOME",
		"sudo rm -rf /",
		"mkfs.ext4 /dev/sda1",
		":(){ :|:& };:",
		"shutdown /s /t 0",
		"Stop-Computer -Force",
		"dd if=/dev/zero of=/dev/sda",
		`del /s /q C:\`,
		`Remove-Item -Recurse -Force C:\`,
		`format C:`,
	}
	for _, c := range dangerous {
		if !isDangerousCommand(c) {
			t.Errorf("must be refused as dangerous: %q", c)
		}
	}

	safe := []string{
		"npm test",
		"go build ./...",
		"git status",
		"rm -rf node_modules",
		"rm -rf dist",
		`Remove-Item -Recurse -Force .\dist`,
		"ls -la",
		"python -m pytest",
		"echo hello",
	}
	for _, c := range safe {
		if isDangerousCommand(c) {
			t.Errorf("ordinary project command wrongly refused: %q", c)
		}
	}
}

func havePowershell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("needs powershell")
	}
}

func TestRunToolCommandRunsInProjectDirAndTimesOut(t *testing.T) {
	havePowershell(t)
	dir := t.TempDir()
	write(t, dir, "marker.txt", "hi")

	res := runToolCommand(dir, "Get-ChildItem -Name")
	if !res.Success || !strings.Contains(res.Stdout, "marker.txt") {
		t.Errorf("command must run inside the project dir: %+v", res)
	}

	old := toolCommandTimeout
	toolCommandTimeout = 300 * time.Millisecond
	t.Cleanup(func() { toolCommandTimeout = old })
	res = runToolCommand(dir, "Start-Sleep -Seconds 5")
	if res.Success || !strings.Contains(res.Stdout, "timed out") {
		t.Errorf("a hung command must be stopped and reported, got %+v", res)
	}
}

func TestFormatToolOutputCapsLength(t *testing.T) {
	ok := formatToolOutput(CommandResult{Command: "echo hi", Stdout: "hi", Success: true})
	if !strings.Contains(ok, "succeeded") || !strings.Contains(ok, "hi") {
		t.Errorf("%q", ok)
	}
	failed := formatToolOutput(CommandResult{Command: "false", ExitCode: 1, Success: false})
	if !strings.Contains(failed, "failed (exit 1)") || !strings.Contains(failed, "no output") {
		t.Errorf("%q", failed)
	}
	huge := formatToolOutput(CommandResult{Command: "x", Stdout: strings.Repeat("y", 5000), Success: true})
	if !strings.Contains(huge, "truncated") || strings.Count(huge, "y") >= 5000 {
		t.Errorf("long output must be truncated")
	}
}

func TestChatOllamaWithToolsRunsCommandAndAnswers(t *testing.T) {
	havePowershell(t)
	a := isolate(t)
	dir := t.TempDir()
	write(t, dir, "notes.txt", "the answer is 42")

	step := 0
	var promptsSeen []string
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		promptsSeen = append(promptsSeen, body.Prompt)
		step++
		switch step {
		case 1:
			fmt.Fprint(w, `{"response":"RUN: Get-Content notes.txt"}`)
		default:
			fmt.Fprint(w, `{"response":"The notes say the answer is 42."}`)
		}
	}))
	def, _ := a.findModel("qwen-coder-7b")
	installFakeWeights(def.OllamaTag)

	reply, actions, err := a.chatOllamaWithTools(context.Background(), def, ChatRequest{Message: "what's the answer?"}, dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(reply, "42") {
		t.Errorf("final reply should use the command's real output: %q", reply)
	}
	if len(actions) != 1 || actions[0].Type != "command" || !strings.Contains(actions[0].Detail, "notes.txt") {
		t.Errorf("expected one recorded command action, got %+v", actions)
	}
	if step != 2 {
		t.Errorf("expected exactly 2 model calls (ask, then answer), got %d", step)
	}
	if !strings.Contains(promptsSeen[1], "the answer is 42") {
		t.Errorf("second call must include the real command output: %q", promptsSeen[1])
	}
}

func TestChatOllamaWithToolsRefusesDangerousCommand(t *testing.T) {
	a := isolate(t)
	dir := t.TempDir()

	step := 0
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		if step == 1 {
			fmt.Fprint(w, `{"response":"RUN: rm -rf /"}`)
		} else {
			fmt.Fprint(w, `{"response":"I won't run that."}`)
		}
	}))
	def, _ := a.findModel("qwen-coder-7b")
	installFakeWeights(def.OllamaTag)

	reply, actions, err := a.chatOllamaWithTools(context.Background(), def, ChatRequest{Message: "clean everything"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "won't run") {
		t.Errorf("model's follow-up reply expected, got %q", reply)
	}
	if len(actions) != 1 || actions[0].Label != "Refused command" {
		t.Errorf("dangerous command must be logged as refused, not run: %+v", actions)
	}
}

func TestChatOllamaWithToolsCapsIterations(t *testing.T) {
	havePowershell(t)
	a := isolate(t)
	dir := t.TempDir()

	calls := 0
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"response":"RUN: echo again"}`) // never stops asking to run something
	}))
	def, _ := a.findModel("qwen-coder-7b")
	installFakeWeights(def.OllamaTag)

	_, actions, err := a.chatOllamaWithTools(context.Background(), def, ChatRequest{Message: "loop forever"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if calls != maxToolIterations+1 {
		t.Errorf("expected %d model calls (cap + final), got %d", maxToolIterations+1, calls)
	}
	if len(actions) != maxToolIterations {
		t.Errorf("expected %d recorded commands, got %d", maxToolIterations, len(actions))
	}
}

func TestChatOllamaWithToolsSkipsToolsWithoutProject(t *testing.T) {
	a := isolate(t)
	calls := 0
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"response":"RUN: echo hi"}`) // even if the model tries, there's nowhere to run it
	}))
	def, _ := a.findModel("qwen-coder-7b")
	installFakeWeights(def.OllamaTag)

	reply, actions, err := a.chatOllamaWithTools(context.Background(), def, ChatRequest{Message: "hi"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(actions) != 0 {
		t.Errorf("no project selected must never attempt a tool call: calls=%d actions=%+v", calls, actions)
	}
	if reply != "RUN: echo hi" {
		t.Errorf("without a project the raw reply passes through untouched: %q", reply)
	}
}
