package main

import (
	"encoding/json"
	"strings"
)

// Claude Code in `-p` mode with `--output-format stream-json --verbose` prints one JSON event per
// line: assistant turns (text, thinking and tool_use blocks), tool results fed back as "user"
// turns, and a closing "result" event holding the final answer. Parsing these is what lets the UI
// show what the agent is really doing (reading a file, running a command...) while it works,
// instead of guessing from plain-text output that only ever contains the final answer.

// claudeStreamUpdate is what one stream line means for the UI and for the final reply.
type claudeStreamUpdate struct {
	Steps    []LiveAgentStep // tools that just started (status "running"; Timestamp is set by the emitter)
	Done     []string        // ids of tools that just finished
	Thought  string          // extended-thinking text
	Text     string          // plain assistant text, used as a fallback reply if no result event arrives
	Final    string          // the "result" event's final answer
	HasFinal bool
	IsError  bool
}

type claudeStreamEvent struct {
	Type    string `json:"type"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Message struct {
		Content []struct {
			Type      string                 `json:"type"`
			Text      string                 `json:"text"`
			Thinking  string                 `json:"thinking"`
			ID        string                 `json:"id"`
			Name      string                 `json:"name"`
			Input     map[string]interface{} `json:"input"`
			ToolUseID string                 `json:"tool_use_id"`
		} `json:"content"`
	} `json:"message"`
}

// parseClaudeStreamLine returns ok=false for anything that is not a JSON event (e.g. stderr noise
// merged into stdout), so callers can keep those lines as raw output.
func parseClaudeStreamLine(line string) (claudeStreamUpdate, bool) {
	var u claudeStreamUpdate
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return u, false
	}
	var ev claudeStreamEvent
	if json.Unmarshal([]byte(line), &ev) != nil || ev.Type == "" {
		return u, false
	}

	switch ev.Type {
	case "assistant":
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				u.Text += c.Text
			case "thinking":
				u.Thought += c.Thinking
			case "tool_use":
				icon, title, detail := describeClaudeTool(c.Name, c.Input)
				if title != "" {
					u.Steps = append(u.Steps, LiveAgentStep{ID: "claude-" + c.ID, Icon: icon, Title: title, Detail: detail, Status: "running"})
				}
			}
		}
	case "user":
		for _, c := range ev.Message.Content {
			if c.Type == "tool_result" && c.ToolUseID != "" {
				u.Done = append(u.Done, "claude-"+c.ToolUseID)
			}
		}
	case "result":
		u.Final = strings.TrimSpace(ev.Result)
		u.HasFinal = true
		u.IsError = ev.IsError
	}
	return u, true
}

func claudeInputString(in map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s, ok := in[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// describeClaudeTool turns a tool call into the icon / title / detail shown in the live trajectory.
func describeClaudeTool(name string, in map[string]interface{}) (icon, title, detail string) {
	switch name {
	case "Read":
		p := claudeInputString(in, "file_path", "path")
		clean := cleanProjectDisplayPath(p, "")
		if clean == "" {
			return "", "", ""
		}
		return "file", "Reading " + clean, clean
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		p := claudeInputString(in, "file_path", "notebook_path", "path")
		clean := cleanProjectDisplayPath(p, "")
		if clean == "" {
			return "", "", ""
		}
		return "edit", "Editing " + clean, clean
	case "Bash":
		c := claudeInputString(in, "command")
		return "terminal", "Executing terminal command", c
	case "Grep", "Glob":
		q := claudeInputString(in, "pattern")
		return "search", "Searching project: " + q, q
	case "WebSearch", "WebFetch":
		q := claudeInputString(in, "query", "url")
		return "search", "Searching web", q
	case "Task", "Agent":
		return "brain", "Sub-agent task", claudeInputString(in, "description")
	}
	return "terminal", "Using " + name, ""
}
