package quota

import (
	"testing"
	"time"
)

// TestSpentWeeklySuppressesSessionPool checks when blocked session quota becomes usable again.
func TestSpentWeeklySuppressesSessionPool(test *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []struct {
		name         string
		driver       string
		weeklyId     string
		used         float64
		sessionReset string
		weeklyReset  string
		checkedAt    string
		wantUsed     int
		wantReset    string
		wantRefill   float64
	}{
		{"session resets first", "codex", "secondary", 20, "2026-10-29T13:00:00Z", "2026-10-30T12:00:00Z", checked(now), 100, "2026-10-30T12:00:00Z", 100},
		{"weekly resets first", "codex", "secondary", 20, "2026-10-30T13:00:00Z", "2026-10-30T12:00:00Z", checked(now), 100, "2026-10-30T12:00:00Z", 80},
		{"both exhausted", "codex", "secondary", 100, "2026-10-30T13:00:00Z", "2026-10-30T12:00:00Z", checked(now), 100, "2026-10-30T13:00:00Z", 100},
		{"equal resets", "codex", "secondary", 20, "2026-10-30T12:00:00Z", "2026-10-30T12:00:00Z", checked(now), 100, "2026-10-30T12:00:00Z", 100},
		{"Claude subscription", "claudeAgent", "seven_day", 20, "2026-10-30T13:00:00Z", "2026-10-30T12:00:00Z", checked(now), 100, "2026-10-30T12:00:00Z", 80},
		{"Claude model allowance", "claudeAgent", "seven_day_opus", 20, "2026-10-30T13:00:00Z", "2026-10-30T12:00:00Z", checked(now), 20, "2026-10-30T13:00:00Z", 20},
		{"other provider", "other", "secondary", 20, "2026-10-30T13:00:00Z", "2026-10-30T12:00:00Z", checked(now), 20, "2026-10-30T13:00:00Z", 20},
		{"missing session reset", "codex", "secondary", 20, "", "2026-10-30T12:00:00Z", checked(now), 100, "", 0},
		{"missing weekly reset", "codex", "secondary", 20, "2026-10-30T13:00:00Z", "", checked(now), 100, "", 0},
		{"invalid weekly reset", "codex", "secondary", 20, "2026-10-30T13:00:00Z", "invalid", checked(now), 100, "", 0},
		{"stale weekly reset", "codex", "secondary", 20, "2026-10-30T13:00:00Z", checked(now), checked(now), 100, "", 0},
		{"stale session reset", "codex", "secondary", 20, checked(now), "2026-10-30T12:00:00Z", checked(now), 100, "", 0},
		{"snapshot clock ahead", "codex", "secondary", 20, "2026-10-30T13:00:00Z", "2026-10-30T12:00:00Z", "2026-10-30T12:00:00Z", 100, "", 0},
		{"invalid snapshot clock", "codex", "secondary", 20, "2026-10-30T13:00:00Z", "2026-10-30T12:00:00Z", "invalid", 100, "", 0},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			reported := Window{Id: "primary", Kind: "session", UsedPercent: scenario.used, ResetsAt: scenario.sessionReset, WindowDurationMins: duration(300)}
			account := PooledAccount{Key: "first", Driver: scenario.driver, Limits: makeLimits(scenario.checkedAt, []Window{reported, {Id: scenario.weeklyId, Kind: "weekly", UsedPercent: 100, ResetsAt: scenario.weeklyReset}})}
			pool := CollectPools([]PooledAccount{account}, now)[0]
			session := pool.Windows[0]
			column := session.Columns[0]
			if session.UsedPercent != scenario.wantUsed || session.RemainingPercent != 100-scenario.wantUsed || column.Window.ResetsAt != scenario.wantReset || column.RefillPercent != scenario.wantRefill {
				test.Fatalf("wrong blocked session availability: %+v, %+v", session, column)
			}
			if scenario.wantUsed == 100 && (session.Pace != "" || column.Window.WindowDurationMins != nil) {
				test.Fatalf("weekly suppression has a session pace clock: %+v", session)
			}
			if scenario.wantReset == "" {
				if len(session.Resets) != 0 {
					test.Fatal("unknown availability acquired a reset", session.Resets)
				}
			} else if len(session.Resets) != 1 || session.Resets[0].RestoresPercent != int(scenario.wantRefill) || session.Resets[0].At != iso(scenario.wantReset) {
				test.Fatal("reset does not restore usable quota", session.Resets)
			}
			if account.Limits.Windows[0] != reported {
				test.Fatal("pooling changed the reported snapshot")
			}
		})
	}
}

// TestSpentWeeklyPoolSharesAndOrder uses usable reset clocks for stable columns and refill shares.
func TestSpentWeeklyPoolSharesAndOrder(test *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	blocked := PooledAccount{Key: "blocked", Driver: "codex", Limits: makeLimits(checked(now), []Window{
		{Id: "primary", Kind: "session", UsedPercent: 20, ResetsAt: "2026-10-29T13:00:00Z", WindowDurationMins: duration(300)},
		{Id: "secondary", Kind: "weekly", UsedPercent: 100, ResetsAt: "2026-10-30T12:00:00Z"},
	})}
	available := PooledAccount{Key: "available", Driver: "codex", Limits: makeLimits(checked(now), []Window{
		{Id: "primary", Kind: "session", UsedPercent: 50, ResetsAt: "2026-10-29T14:30:00Z", WindowDurationMins: duration(300)},
	})}
	pool := CollectPools([]PooledAccount{blocked, available}, now)[0]
	session := pool.Windows[0]
	if pool.Accounts[0].Key != "available" || session.Columns[0].AccountKey != "available" || session.UsedPercent != 75 || session.RemainingPercent != 25 || session.Pace != "on" {
		test.Fatal("blocked member distorted ordering, equal shares or pace", pool)
	}
	if len(session.Resets) != 2 || session.Resets[0].RestoresPercent != 25 || session.Resets[1].RestoresPercent != 50 {
		test.Fatal("refill shares do not match usable quota", session.Resets)
	}
	blocked.Limits.Windows[0].ResetsAt = "2026-10-30T13:00:00Z"
	session = CollectPools([]PooledAccount{blocked, available}, now)[0].Windows[0]
	if session.Resets[1].RestoresPercent != 40 {
		test.Fatal("weekly reset overstated partially spent session refill", session.Resets)
	}
}
