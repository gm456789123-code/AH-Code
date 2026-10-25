package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const grokProxyBase = "https://cli-chat-proxy.grok.com"

type grokAuth struct {
	LoggedIn  bool
	Email     string
	FirstName string
	Token     string
}

// readGrokAuth reads the local Grok Build login. The session token stays in this process.
func readGrokAuth() grokAuth {
	var auth grokAuth
	home, err := os.UserHomeDir()
	if err != nil {
		return auth
	}
	authBytes, err := os.ReadFile(filepath.Join(home, ".grok", "auth.json"))
	if err != nil || len(authBytes) <= 10 {
		return auth
	}
	var authData map[string]interface{}
	if err := json.Unmarshal(authBytes, &authData); err != nil {
		return auth
	}
	for _, v := range authData {
		obj, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		auth.LoggedIn = true
		if email, ok := obj["email"].(string); ok {
			auth.Email = email
		}
		if fn, ok := obj["first_name"].(string); ok {
			auth.FirstName = fn
		}
		if key, ok := obj["key"].(string); ok {
			auth.Token = key
		}
		break
	}
	return auth
}

func grokAPIGet(token, path string) ([]byte, int, error) {
	if token == "" {
		return nil, 0, fmt.Errorf("missing grok session")
	}
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, grokProxyBase+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "grok-shell")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// grokEffort maps the in-app modes onto the effort levels grok-4.7 actually advertises.
// grok CLI: --effort low|medium|high|xhigh. There is no separate fast/auto/expert/heavy model id.
func grokEffort(model string) (string, bool) {
	switch model {
	case "grok-fast":
		return "low", true
	case "grok-auto", "supergrok":
		return "medium", true
	case "grok-expert":
		return "high", true
	case "grok-heavy":
		return "xhigh", true
	default:
		return "", false
	}
}

func appendGrokEffort(args []string, model string) []string {
	effort, ok := grokEffort(model)
	if !ok {
		return args
	}
	return append(args, "--effort", effort)
}

func remainingPct(used float64) int {
	if math.IsNaN(used) || used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	rem := int(math.Round(100 - used))
	if rem < 0 {
		return 0
	}
	if rem > 100 {
		return 100
	}
	return rem
}

func formatRefreshRemaining(now, end time.Time) string {
	diff := end.Sub(now)
	if diff <= time.Minute {
		return "a few minutes"
	}
	totalMin := int(diff.Minutes())
	days := totalMin / (24 * 60)
	hours := (totalMin % (24 * 60)) / 60
	mins := totalMin % 60
	if days > 0 {
		return fmt.Sprintf("%d day%s, %d hour%s", days, pluralInt(days), hours, pluralInt(hours))
	}
	if hours > 0 {
		return fmt.Sprintf("%d hour%s, %d minute%s", hours, pluralInt(hours), mins, pluralInt(mins))
	}
	return fmt.Sprintf("%d minute%s", mins, pluralInt(mins))
}

func usagePeriodLabel(kind string) string {
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "USAGE_PERIOD_TYPE_MONTHLY", "MONTHLY":
		return "Monthly"
	case "USAGE_PERIOD_TYPE_WEEKLY", "WEEKLY", "":
		return "Weekly"
	default:
		return "Usage"
	}
}

func productDisplayName(product string) string {
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "grokbuild", "grok-build", "grok_build":
		return "Grok Build"
	default:
		if strings.TrimSpace(product) == "" {
			return "Grok"
		}
		return product
	}
}

type grokBillingPayload struct {
	Config struct {
		CreditUsagePercent *float64 `json:"creditUsagePercent"`
		CurrentPeriod      struct {
			Type string `json:"type"`
			End  string `json:"end"`
		} `json:"currentPeriod"`
		BillingPeriodEnd string `json:"billingPeriodEnd"`
		ProductUsage     []struct {
			Product      string   `json:"product"`
			UsagePercent *float64 `json:"usagePercent"`
		} `json:"productUsage"`
	} `json:"config"`
}

// parseGrokBilling reads GET /v1/billing?format=credits.
// creditUsagePercent is the amount used, so the UI stores what is left.
func parseGrokBilling(body []byte, now time.Time) (GrokQuotaUsage, error) {
	var payload grokBillingPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return GrokQuotaUsage{}, err
	}
	cfg := payload.Config
	out := GrokQuotaUsage{
		Live:        true,
		Source:      "live",
		PeriodLabel: usagePeriodLabel(cfg.CurrentPeriod.Type),
	}
	if cfg.CreditUsagePercent != nil {
		out.WeeklyKnown = true
		out.WeeklyRemainingPct = remainingPct(*cfg.CreditUsagePercent)
	}
	endRaw := cfg.CurrentPeriod.End
	if endRaw == "" {
		endRaw = cfg.BillingPeriodEnd
	}
	if end, ok := parseGrokTime(endRaw); ok {
		out.WeeklyRefreshTime = formatRefreshRemaining(now, end)
	}

	var pickedName string
	var pickedUsed float64
	have := false
	for _, p := range cfg.ProductUsage {
		if p.UsagePercent == nil {
			continue
		}
		if !have || strings.EqualFold(p.Product, "GrokBuild") {
			pickedName = p.Product
			pickedUsed = *p.UsagePercent
			have = true
			if strings.EqualFold(p.Product, "GrokBuild") {
				break
			}
		}
	}
	if have {
		out.ProductKnown = true
		out.ProductLabel = productDisplayName(pickedName)
		out.ProductRemainingPct = remainingPct(pickedUsed)
	}
	return out, nil
}

func parseGrokTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func tierFromGrokLogs(home string) string {
	logPath := filepath.Join(home, ".grok", "logs", "unified.jsonl")
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-250; i-- {
		line := lines[i]
		if !strings.Contains(line, "subscription.check.complete") && !strings.Contains(line, "paywall_check_result") && !strings.Contains(line, `"tier":`) {
			continue
		}
		var entry struct {
			Ctx struct {
				Tier             string `json:"tier"`
				SubscriptionTier string `json:"subscription_tier"`
			} `json:"ctx"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		t := entry.Ctx.Tier
		if t == "" {
			t = entry.Ctx.SubscriptionTier
		}
		if t != "" && t != "null" {
			return strings.ToUpper(strings.TrimSpace(t))
		}
	}
	return ""
}

func (a *App) fetchGrokQuota(now time.Time) GrokQuotaUsage {
	unavailable := GrokQuotaUsage{Source: "unavailable", PeriodLabel: "Weekly"}
	auth := readGrokAuth()
	if !auth.LoggedIn || auth.Token == "" {
		return unavailable
	}
	body, status, err := grokAPIGet(auth.Token, "/v1/billing?format=credits")
	if err != nil || status != http.StatusOK {
		return unavailable
	}
	parsed, err := parseGrokBilling(body, now)
	if err != nil {
		return unavailable
	}
	return parsed
}
