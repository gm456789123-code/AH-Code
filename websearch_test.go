package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestParseDDGResultsAgainstRealFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/ddg_sample.html")
	if err != nil {
		t.Fatal(err)
	}
	results := parseDDGResults(string(body))
	if len(results) != maxSearchResults {
		t.Fatalf("want %d results (capped), got %d: %+v", maxSearchResults, len(results), results)
	}

	first := results[0]
	if first.Title != "context package - context - Go Packages" {
		t.Errorf("title = %q", first.Title)
	}
	if first.URL != "https://pkg.go.dev/context" {
		t.Errorf("URL must be the real decoded target, not the DuckDuckGo redirect: %q", first.URL)
	}
	if !strings.Contains(first.Snippet, "Package") || !strings.Contains(first.Snippet, "context") {
		t.Errorf("snippet = %q", first.Snippet)
	}
	if strings.Contains(first.Snippet, "<b>") || strings.Contains(first.Title, "<") {
		t.Errorf("HTML tags must be stripped: title=%q snippet=%q", first.Title, first.Snippet)
	}

	for _, r := range results {
		if strings.Contains(strings.ToLower(r.URL), "duckduckgo.com/y.js") || strings.Contains(strings.ToLower(r.URL), "bing.com/aclick") {
			t.Errorf("a sponsored/ad result leaked through: %+v", r)
		}
		if strings.Contains(r.URL, "duckduckgo.com/l/") {
			t.Errorf("URL must be unwrapped from the DuckDuckGo redirect, got %q", r.URL)
		}
	}
}

func TestFormatSearchOutput(t *testing.T) {
	empty := formatSearchOutput("golang", nil)
	if !strings.Contains(empty, "no results") {
		t.Errorf("%q", empty)
	}

	out := formatSearchOutput("golang context", []SearchResult{
		{Title: "context package", URL: "https://pkg.go.dev/context", Snippet: "Package context defines..."},
	})
	if !strings.Contains(out, "context package") || !strings.Contains(out, "pkg.go.dev/context") || !strings.Contains(out, "Package context defines") {
		t.Errorf("%q", out)
	}
}

func TestIsSponsoredResult(t *testing.T) {
	sponsored := []string{
		"https://duckduckgo.com/y.js?ad_domain=udemy.com&ad_provider=bingv7aa",
		"https://www.bing.com/aclick?ld=e8HWqgKp",
	}
	for _, s := range sponsored {
		if !isSponsoredResult(s) {
			t.Errorf("must be detected as sponsored: %q", s)
		}
	}
	real := []string{"https://pkg.go.dev/context", "https://go.dev/blog/context"}
	for _, s := range real {
		if isSponsoredResult(s) {
			t.Errorf("a real result must not be filtered: %q", s)
		}
	}
}

func TestWebSearchEmptyQuery(t *testing.T) {
	if _, err := webSearch(context.Background(), "  "); err == nil {
		t.Error("blank query must be rejected before making a request")
	}
}

// TestLiveWebSearch hits the real DuckDuckGo lite endpoint.
// Opt-in: SENTCODE_LIVE=1 go test -run TestLiveWebSearch -v
func TestLiveWebSearch(t *testing.T) {
	if os.Getenv("SENTCODE_LIVE") == "" {
		t.Skip("set SENTCODE_LIVE=1 to run against the real internet")
	}
	results, err := webSearch(context.Background(), "golang context package")
	if err != nil {
		t.Fatalf("webSearch: %v", err)
	}
	t.Logf("%+v", results)
	if len(results) == 0 {
		t.Fatal("expected at least one real result")
	}
	for _, r := range results {
		if r.Title == "" || r.URL == "" {
			t.Errorf("incomplete result: %+v", r)
		}
	}
}
