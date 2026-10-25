package main

import (
	"path/filepath"
	"testing"
)

func TestResolveProjectFile(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		in      string
		wantRel string
		wantOK  bool
	}{
		{"app.go", "app.go", true},
		{"src/main.go", "src/main.go", true},
		{"`src\\main.go`", "src/main.go", true},
		{filepath.Join(dir, "a", "b.txt"), "a/b.txt", true},
		{"../evil.txt", "", false},
		{"a/../../evil.txt", "", false},
		{".git/config", "", false},
		{"node_modules/x/index.js", "", false},
		{"", "", false},
		{".", "", false},
	}
	for _, c := range cases {
		rel, abs, ok := resolveProjectFile(dir, c.in)
		if ok != c.wantOK || rel != c.wantRel {
			t.Errorf("resolveProjectFile(%q) = (%q, %q, %v), want rel %q ok %v", c.in, rel, abs, ok, c.wantRel, c.wantOK)
		}
		if ok && abs != filepath.Join(dir, filepath.FromSlash(rel)) {
			t.Errorf("abs %q not under project for %q", abs, c.in)
		}
	}
}
