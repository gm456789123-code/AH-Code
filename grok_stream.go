package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// grokStreamUpdate is what one streaming-json line from Grok means for the UI.
type grokStreamUpdate struct {
	Steps     []LiveAgentStep
	Done      []string
	Thought   string
	Text      string
	SessionID string
	IsEnd     bool
}

type grokStreamEvent struct {
	Type       string                 `json:"type"`
	Data       string                 `json:"data"`
	ToolCallID string                 `json:"toolCallId"`
	Title      string                 `json:"title"`
	ToolName   string                 `json:"toolName"`
	Status     *string                `json:"status"`
	RawInput   map[string]interface{} `json:"rawInput"`
	SessionID  string                 `json:"sessionId"`
	StopReason string                 `json:"stopReason"`
}

// parseGrokStreamLine parses one NDJSON line from `grok --output-format streaming-json`.
func parseGrokStreamLine(line string, targetDir string) (grokStreamUpdate, bool) {
	var u grokStreamUpdate
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return u, false
	}

	var ev grokStreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type == "" {
		return u, false
	}

	switch ev.Type {
	case "thought":
		u.Thought = ev.Data
	case "text":
		u.Text = ev.Data
	case "tool_call":
		toolID := ev.ToolCallID
		if toolID == "" {
			toolID = fmt.Sprintf("grok-tool-%s", ev.Title)
		}
		name := ev.ToolName
		if name == "" {
			name = ev.Title
		}
		icon, title, detail := describeGrokTool(name, ev.RawInput, targetDir)
		if title != "" {
			u.Steps = append(u.Steps, LiveAgentStep{
				ID:     toolID,
				Icon:   icon,
				Title:  title,
				Detail: detail,
				Status: "running",
			})
		}
	case "tool_call_update":
		if ev.Status != nil && (strings.EqualFold(*ev.Status, "completed") || strings.EqualFold(*ev.Status, "done") || strings.EqualFold(*ev.Status, "success")) {
			if ev.ToolCallID != "" {
				u.Done = append(u.Done, ev.ToolCallID)
			}
		}
	case "end":
		u.IsEnd = true
		u.SessionID = ev.SessionID
	}

	return u, true
}

func grokInputStr(in map[string]interface{}, keys ...string) string {
	if in == nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := in[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// describeGrokTool turns a Grok tool call into icon, title, and display detail
func describeGrokTool(name string, in map[string]interface{}, targetDir string) (icon, title, detail string) {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "read_file") || strings.Contains(lower, "read"):
		p := grokInputStr(in, "target_file", "path", "file_path", "file")
		clean := cleanProjectDisplayPath(p, targetDir)
		if clean == "" && p != "" {
			clean = filepath.Base(p)
		}
		if clean == "" {
			clean = "file"
		}
		return "file", "Reading " + filepath.Base(clean), clean

	case strings.Contains(lower, "write") || strings.Contains(lower, "create"):
		p := grokInputStr(in, "target_file", "path", "file_path")
		clean := cleanProjectDisplayPath(p, targetDir)
		if clean == "" && p != "" {
			clean = filepath.Base(p)
		}
		if clean == "" {
			clean = "file"
		}
		return "edit", "Writing " + filepath.Base(clean), clean

	case strings.Contains(lower, "search_replace") || strings.Contains(lower, "replace") || strings.Contains(lower, "edit"):
		p := grokInputStr(in, "target_file", "path", "file_path")
		clean := cleanProjectDisplayPath(p, targetDir)
		if clean == "" && p != "" {
			clean = filepath.Base(p)
		}
		if clean == "" {
			clean = "file"
		}
		return "edit", "Editing " + filepath.Base(clean), clean

	case strings.Contains(lower, "terminal") || strings.Contains(lower, "command") || strings.Contains(lower, "exec") || strings.Contains(lower, "run"):
		cmd := grokInputStr(in, "command", "cmd", "script")
		if len(cmd) > 80 {
			cmd = cmd[:77] + "..."
		}
		return "terminal", "Executing terminal command", cmd

	case strings.Contains(lower, "grep") || strings.Contains(lower, "search"):
		q := grokInputStr(in, "query", "pattern", "search_term")
		return "search", "Searching codebase", q

	case strings.Contains(lower, "list_dir") || strings.Contains(lower, "dir") || strings.Contains(lower, "ls"):
		p := grokInputStr(in, "path", "target_dir", "dir")
		clean := cleanProjectDisplayPath(p, targetDir)
		return "folder", "Inspecting directory", clean

	case strings.Contains(lower, "web_search"):
		q := grokInputStr(in, "query", "search_query")
		return "search", "Searching web", q

	case strings.Contains(lower, "web_fetch") || strings.Contains(lower, "fetch") || strings.Contains(lower, "url"):
		url := grokInputStr(in, "url", "link")
		return "search", "Fetching URL", url

	default:
		return "terminal", "Running " + name, ""
	}
}
