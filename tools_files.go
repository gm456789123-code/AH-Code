package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const replaceFilePrefix = "REPLACE:"

// resolveProjectFile maps a model-supplied path (relative or absolute) to a file inside
// projectDir. It refuses anything that escapes the project or touches .git / node_modules,
// and returns the project-relative slash path for display.
func resolveProjectFile(projectDir, p string) (rel, abs string, ok bool) {
	p = strings.Trim(strings.TrimSpace(p), "`\"'")
	if p == "" {
		return "", "", false
	}
	root, err := filepath.Abs(projectDir)
	if err != nil {
		return "", "", false
	}
	abs = p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, p)
	}
	abs = filepath.Clean(abs)
	r, err := filepath.Rel(root, abs)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	rel = filepath.ToSlash(r)
	for _, seg := range strings.Split(rel, "/") {
		if strings.EqualFold(seg, ".git") || strings.EqualFold(seg, "node_modules") {
			return "", "", false
		}
	}
	return rel, abs, true
}

// parseReplaceBlock extracts the search/replace pair from the body after the path line:
//
//	<<<<<<< SEARCH
//	old text
//	=======
//	new text
//	>>>>>>> REPLACE
func parseReplaceBlock(body string) (search, replace string, ok bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	const start, mid, end = "<<<<<<< SEARCH\n", "\n=======\n", "\n>>>>>>> REPLACE"
	i := strings.Index(body, start)
	if i < 0 {
		return "", "", false
	}
	body = body[i+len(start):]
	j := strings.Index(body, mid)
	if j < 0 {
		return "", "", false
	}
	search = body[:j]
	body = body[j+len(mid):]
	k := strings.LastIndex(body, end)
	if k < 0 {
		return "", "", false
	}
	return search, body[:k], search != ""
}

// applyReplace edits one file by replacing a single exact occurrence of search.
func applyReplace(projectDir, path, body string) (rel string, msg string, err error) {
	rel, abs, ok := resolveProjectFile(projectDir, path)
	if !ok {
		return "", "", fmt.Errorf("path is outside the project folder or a protected location")
	}
	search, replace, ok := parseReplaceBlock(body)
	if !ok {
		return rel, "", fmt.Errorf("bad REPLACE format; use a '<<<<<<< SEARCH', '=======', '>>>>>>> REPLACE' block")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return rel, "", err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	switch n := strings.Count(text, search); {
	case n == 0:
		return rel, "", fmt.Errorf("the SEARCH text was not found in the file; READ it again and copy it exactly")
	case n > 1:
		return rel, "", fmt.Errorf("the SEARCH text matches %d places; include more surrounding lines to make it unique", n)
	}
	text = strings.Replace(text, search, replace, 1)
	if strings.Contains(string(data), "\r\n") {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	if err := os.WriteFile(abs, []byte(text), 0644); err != nil {
		return rel, "", err
	}
	return rel, fmt.Sprintf("[File Editor] Replaced text in `%s`. Continue, or answer now.", rel), nil
}
