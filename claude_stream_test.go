package main

import "testing"

func TestParseClaudeStreamToolUseAndResult(t *testing.T) {
	use := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"look at the file"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"C:\\proj\\app.go"}}]}}`
	u, ok := parseClaudeStreamLine(use)
	if !ok || len(u.Steps) != 1 {
		t.Fatalf("tool_use must yield one running step, got ok=%v %+v", ok, u)
	}
	s := u.Steps[0]
	if s.ID != "claude-toolu_1" || s.Status != "running" || s.Title != "Reading app.go" || s.Detail != "app.go" || s.Icon != "file" {
		t.Errorf("unexpected step: %+v", s)
	}
	if u.Thought != "look at the file" {
		t.Errorf("thinking must be surfaced, got %q", u.Thought)
	}

	res := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"..."}]}}`
	u, ok = parseClaudeStreamLine(res)
	if !ok || len(u.Done) != 1 || u.Done[0] != "claude-toolu_1" {
		t.Errorf("tool_result must close the matching step, got %+v", u)
	}
}

func TestParseClaudeStreamToolKinds(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]interface{}
		icon  string
		title string
	}{
		{"Edit", map[string]interface{}{"file_path": `C:\p\a.go`}, "edit", "Editing a.go"},
		{"Bash", map[string]interface{}{"command": "go test ./..."}, "terminal", "Executing terminal command"},
		{"Grep", map[string]interface{}{"pattern": "TODO"}, "search", "Searching project: TODO"},
		{"Mystery", map[string]interface{}{}, "terminal", "Using Mystery"},
	}
	for _, c := range cases {
		icon, title, _ := describeClaudeTool(c.name, c.input)
		if icon != c.icon || title != c.title {
			t.Errorf("%s: got %q %q, want %q %q", c.name, icon, title, c.icon, c.title)
		}
	}
}

func TestParseClaudeStreamFinalResultAndNoise(t *testing.T) {
	u, ok := parseClaudeStreamLine(`{"type":"result","subtype":"success","is_error":false,"result":"  เสร็จแล้วครับ  "}`)
	if !ok || !u.HasFinal || u.Final != "เสร็จแล้วครับ" || u.IsError {
		t.Errorf("result event must carry the trimmed final answer, got %+v", u)
	}
	u, _ = parseClaudeStreamLine(`{"type":"result","is_error":true,"result":"boom"}`)
	if !u.IsError {
		t.Error("is_error must propagate")
	}

	for _, noise := range []string{"", "warning: something on stderr", `{not json`, `{"foo":1}`} {
		if _, ok := parseClaudeStreamLine(noise); ok {
			t.Errorf("%q is not a stream event and must be left to the raw-output path", noise)
		}
	}
}
