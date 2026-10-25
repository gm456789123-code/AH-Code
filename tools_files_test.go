package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseToolCallReplace(t *testing.T) {
	kind, arg, ok := parseToolCall("REPLACE: a.go\n<<<<<<< SEARCH\nx\n=======\ny\n>>>>>>> REPLACE")
	if !ok || kind != toolReplace || !strings.HasPrefix(arg, "a.go\n<<<<<<< SEARCH") {
		t.Fatalf("got (%v, %q, %v)", kind, arg, ok)
	}
}

func TestApplyReplace(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	os.WriteFile(file, []byte("one\r\ntwo\r\nthree\r\n"), 0644)
	block := func(s, r string) string { return "<<<<<<< SEARCH\n" + s + "\n=======\n" + r + "\n>>>>>>> REPLACE" }

	if _, _, err := applyReplace(dir, "a.txt", block("two", "2")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if string(got) != "one\r\n2\r\nthree\r\n" { // CRLF preserved
		t.Errorf("got %q", got)
	}
	if _, _, err := applyReplace(dir, "a.txt", block("missing", "x")); err == nil {
		t.Error("expected not-found error")
	}
	os.WriteFile(file, []byte("a\na\n"), 0644)
	if _, _, err := applyReplace(dir, "a.txt", block("a", "b")); err == nil {
		t.Error("expected ambiguous-match error")
	}
	if _, _, err := applyReplace(dir, "../a.txt", block("a", "b")); err == nil {
		t.Error("expected outside-project error")
	}
}
