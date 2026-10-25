package main

import (
	"testing"
	"time"
)

func TestGrokEffortMatchesOfficialMenu(t *testing.T) {
	cases := map[string]string{
		"grok-fast":   "low",
		"grok-auto":   "medium",
		"grok-expert": "high",
		"grok-heavy":  "xhigh",
		"supergrok":   "medium",
	}
	for model, effort := range cases {
		args := appendGrokEffort([]string{"--resume", "abc"}, model)
		args = append(args, "-p", "hi")
		if !argFollows(args, "--effort", effort) {
			t.Errorf("%s args = %v, want --effort %s", model, args, effort)
		}
	}
	plain := appendGrokEffort([]string{"-p", "hi"}, "qwen-coder-7b")
	if len(plain) != 2 || argFollows(plain, "--effort", "low") {
		t.Errorf("local model must keep its own engine, got %v", plain)
	}
}

func TestParseGrokBillingWeeklyRemaining(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	body := []byte(`{"config":{
		"creditUsagePercent": 51,
		"currentPeriod": {"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2026-10-03T00:00:00Z"},
		"productUsage":[
			{"product":"GrokChat","usagePercent":80},
			{"product":"GrokBuild","usagePercent":10}
		]
	}}`)
	q, err := parseGrokBilling(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Live || q.Source != "live" || !q.WeeklyKnown || q.WeeklyRemainingPct != 49 {
		t.Fatalf("weekly remaining: %+v", q)
	}
	if q.PeriodLabel != "Weekly" || q.WeeklyRefreshTime != "2 days, 0 hours" {
		t.Fatalf("window: %+v", q)
	}
	if !q.ProductKnown || q.ProductLabel != "Grok Build" || q.ProductRemainingPct != 90 {
		t.Fatalf("product slice: %+v", q)
	}
	if q.FiveHourKnown {
		t.Fatalf("billing payload has no 5-hour window: %+v", q)
	}
}

func TestParseGrokBillingOmitsUnknownPercent(t *testing.T) {
	q, err := parseGrokBilling([]byte(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_MONTHLY"}}}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !q.Live || q.WeeklyKnown || q.PeriodLabel != "Monthly" {
		t.Fatalf("%+v", q)
	}
}

func TestParseGrokBillingRejectsGarbage(t *testing.T) {
	if _, err := parseGrokBilling([]byte(`not-json`), time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRemainingPctClamps(t *testing.T) {
	if remainingPct(0) != 100 || remainingPct(100) != 0 || remainingPct(150) != 0 || remainingPct(-4) != 100 {
		t.Fatalf("clamp failed: %d %d %d %d", remainingPct(0), remainingPct(100), remainingPct(150), remainingPct(-4))
	}
	if remainingPct(49.6) != 50 {
		t.Fatalf("round: %d", remainingPct(49.6))
	}
}
