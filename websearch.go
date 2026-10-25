package main

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxSearchResults = 5
	searchTimeout    = 12 * time.Second
	searchSnippetCap = 300 // characters kept per result snippet
)

var searchHTTP = &http.Client{Timeout: searchTimeout}

// SearchResult is one organic result from webSearch.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// ddgResultRe matches one organic (non-sponsored) result block from lite.duckduckgo.com's HTML:
// a result-link anchor wrapping a DuckDuckGo outbound redirect (?uddg=<encoded target>), followed
// by its result-snippet cell. Verified against the live page's real markup before writing this.
var ddgResultRe = regexp.MustCompile(`(?s)<a rel="nofollow" href="//duckduckgo\.com/l/\?uddg=([^&"]+)[^"]*"\s+class=['"]result-link['"]>(.*?)</a>.*?class=['"]result-snippet['"]>(.*?)</td>`)

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string {
	return strings.TrimSpace(html.UnescapeString(htmlTagRe.ReplaceAllString(s, "")))
}

// isSponsoredResult filters out ads: DuckDuckGo's sponsored slots resolve (via the same uddg
// redirect organic results use) to their own ad-tracking endpoint rather than a real destination.
func isSponsoredResult(targetURL string) bool {
	l := strings.ToLower(targetURL)
	return strings.Contains(l, "duckduckgo.com/y.js") || strings.Contains(l, "bing.com/aclick") || strings.Contains(l, "ad_domain=")
}

// webSearch performs a keyless web search (lite.duckduckgo.com, no API key/account needed) and
// returns the top organic results. Used to give local models (which have no browsing of their
// own, unlike Grok/Claude Code) a way to look something up instead of only guessing.
func webSearch(ctx context.Context, query string) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	u := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SentCode/1.0)")
	resp, err := searchHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("search returned %d", resp.StatusCode)
	}

	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil || len(buf) > 2<<20 { // 2 MiB is far more than a lite results page needs
			break
		}
	}
	return parseDDGResults(string(buf)), nil
}

// parseDDGResults extracts organic results from a lite.duckduckgo.com results page. Pulled out
// of webSearch so it can be tested against a saved real page without hitting the network.
func parseDDGResults(body string) []SearchResult {
	var out []SearchResult
	for _, m := range ddgResultRe.FindAllStringSubmatch(body, -1) {
		encodedTarget, title, snippet := m[1], m[2], m[3]
		target, err := url.QueryUnescape(encodedTarget)
		if err != nil || isSponsoredResult(target) {
			continue
		}
		title = stripTags(title)
		snippet = stripTags(snippet)
		if r := []rune(snippet); len(r) > searchSnippetCap {
			snippet = string(r[:searchSnippetCap]) + "..."
		}
		if title == "" || target == "" {
			continue
		}
		out = append(out, SearchResult{Title: title, URL: target, Snippet: snippet})
		if len(out) >= maxSearchResults {
			break
		}
	}
	return out
}

// formatSearchOutput turns search results into the next message handed back to the model.
func formatSearchOutput(query string, results []SearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("[Search] \"%s\" returned no results. Answer using what you already know, or try a different query.", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Search] Top results for \"%s\":\n", query)
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s — %s\n   %s\n", i+1, r.Title, r.URL, r.Snippet)
	}
	b.WriteString("Continue, or answer now if you have enough to finish.")
	return b.String()
}
