package quota

import (
	"math"
	"testing"
	"time"
)

// fittedFixture supplies independent bounded cycle estimates.
func fittedFixture(now time.Time, minutes int, low, high float64) FittedCycle {
	return FittedCycle{Value: CycleValue{Status: "estimated", CycleStartedAt: checked(now.Add(-time.Duration(minutes) * time.Minute)), ResetsAt: checked(now), ValueLowUsd: usd(low), ValueHighUsd: usd(high), ValueUsd: usd(low/2 + high/2), Samples: []CycleValueSample{}, History: []CycleValueHistory{}, ObservationSource: "logs"}, PlanType: "plus", WindowStructure: "primary:" + time.Duration(minutes).String(), WindowMinutes: minutes}
}

// TestBucketSpendBounds excludes model buckets and bounds uncertain attribution separately.
func TestBucketSpendBounds(test *testing.T) {
	start, at := cycleInstant("10:00"), cycleInstant("10:30")
	duration := 300
	records := []BucketCostRecord{
		{CostRecord: CostRecord{At: start.Add(time.Minute), CostUsd: 10}, Bucket: "codex"},
		{CostRecord: CostRecord{At: start.Add(time.Minute), CostUsd: 20}},
		{CostRecord: CostRecord{At: start.Add(time.Minute), CostUsd: 1000, IsUnpriced: true}, Bucket: "codex_bengalfox"},
	}
	history := NewBucketCostHistory(records, "codex", at.Add(2*time.Minute), start)
	value := EstimateCycle(CycleSeries{}, "codex", Window{Id: "primary", UsedPercent: 50, ResetsAt: checked(start.Add(5 * time.Hour)), WindowDurationMins: &duration}, checked(at), history, at.Add(2*time.Minute))
	if value.Status != "insufficientData" || value.Records != 2 || value.UnpricedRecords != 0 || value.SpentUsd != 30 || math.Abs(*value.Samples[0].ImpliedLowUsd-10.0/51*100) > 1e-6 || math.Abs(*value.Samples[0].ImpliedHighUsd-30.0/49.5*100) > 1e-6 {
		test.Fatal(value)
	}
	if history.lowerTotal(start, at) != 10 {
		test.Fatal("unknown spend entered the lower bound")
	}
}

// TestCyclePriorCombination verifies precision shrinkage, cold priors and adjustment detection.
func TestCyclePriorCombination(test *testing.T) {
	now := cycleInstant("12:00")
	prior := []FittedCycle{fittedFixture(now.Add(-time.Hour), 10080, 80, 120), fittedFixture(now.Add(-8*24*time.Hour), 10080, 90, 110)}
	for _, fixture := range []struct {
		name              string
		point, radius     float64
		status            string
		shouldUse, adjust bool
	}{
		{"thin own fit", 130, .4, "estimated", true, false},
		{"precise own fit", 105, .1, "estimated", true, false},
		{"cold own fit", 0, 0, "insufficientData", true, false},
		{"adjustment", 400, .1, "estimated", false, true},
		{"missing local usage", 0, 0, "noLocalUsage", false, false},
		{"no cycle", 0, 0, "noCycle", false, false},
		{"poor own fit", 400, .1, "inconsistent", false, false},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			current := fittedFixture(now.Add(time.Hour), 10080, 95, 105)
			current.Value.Status = fixture.status
			current.Value.UsedPercent = 10
			current.Value.ObservationSource = "probes"
			current.Value.ValueUsd, current.Value.ValueLowUsd, current.Value.ValueHighUsd = nil, nil, nil
			if fixture.point > 0 {
				setCycleBand(&current.Value, fixture.point, fixture.radius)
			}
			result := CombineCyclePrior(current, prior, now)
			if result.IsPriorUsed != fixture.shouldUse || result.IsAdjustmentSuspected != fixture.adjust || result.PriorCycleCount != 2 {
				test.Fatal(result)
			}
			if fixture.shouldUse {
				if result.Status != "estimated" || result.ObservationSource != "both" || result.ValueUsd == nil {
					test.Fatal(result)
				}
				if fixture.point > 100 && (*result.ValueUsd <= 100 || *result.ValueUsd >= fixture.point) {
					test.Fatal("failed to shrink toward prior", result)
				}
				if fixture.point == 0 && math.Abs(*result.ValueUsd-100) > 1e-6 {
					test.Fatal(result)
				}
			}
			if fixture.adjust && (result.History[0].IsUsedForPrior || *result.ValueUsd != fixture.point) {
				test.Fatal("adjustment retained prior", result)
			}
		})
	}
}

// TestCyclePriorRespectsCurrentSpend prevents history from overriding current monetary evidence.
func TestCyclePriorRespectsCurrentSpend(test *testing.T) {
	now := cycleInstant("12:00")
	for _, used := range []float64{80, 100} {
		current := fittedFixture(now.Add(time.Hour), 300, 14, 16)
		current.Value.Status = "insufficientData"
		current.Value.ValueUsd, current.Value.ValueLowUsd, current.Value.ValueHighUsd = nil, nil, nil
		current.Value.SpentUsd, current.Value.UsedPercent = 19, used
		result := CombineCyclePrior(current, []FittedCycle{fittedFixture(now.Add(-time.Hour), 300, 14, 16)}, now)
		if result.ValueUsd == nil || *result.ValueUsd < 19 || *result.ValueLowUsd < 19 || *result.ValueHighUsd < *result.ValueUsd {
			test.Fatal("historical prediction below already-used amount", result)
		}
	}
	current := fittedFixture(now.Add(time.Hour), 10080, 1100, 1300)
	current.Value.SpentUsd, current.Value.UsedPercent = 972, 81
	current.Value.Samples = []CycleValueSample{{UsedPercent: 81, SpentUsd: 972, ImpliedLowUsd: usd(972.0 / 82 * 100), ImpliedHighUsd: usd(972.0 / 80.5 * 100), IsUsable: true}}
	result := CombineCyclePrior(current, []FittedCycle{fittedFixture(now.Add(-time.Hour), 10080, 600, 1000)}, now)
	if result.ValueUsd == nil || *result.ValueUsd < 1185 || *result.ValueUsd > 1210 {
		test.Fatal("historical shrinkage overrode mature current evidence", result)
	}
}

// TestCyclePriorKeepsUnknownSpendInUpperBound preserves uncertain bucket attribution during shrinkage.
func TestCyclePriorKeepsUnknownSpendInUpperBound(test *testing.T) {
	start, now := cycleInstant("10:00"), cycleInstant("12:00")
	minutes := 300
	history := NewBucketCostHistory([]BucketCostRecord{{CostRecord: CostRecord{At: start, CostUsd: 10}, Bucket: "codex"}, {CostRecord: CostRecord{At: start, CostUsd: 400}}}, "codex", now, start)
	current := fittedFixture(now.Add(3*time.Hour), minutes, 19, 21)
	current.Value = EstimateCycle(CycleSeries{}, "codex", Window{Id: "primary", UsedPercent: 50, ResetsAt: checked(start.Add(5 * time.Hour)), WindowDurationMins: &minutes}, checked(now), history, now)
	result := CombineCyclePrior(current, []FittedCycle{fittedFixture(now.Add(-time.Hour), minutes, 19, 21)}, now)
	if result.ValueUsd == nil || math.Abs(*result.ValueUsd-20) > 1e-6 || *result.ValueLowUsd < 10 || *result.ValueLowUsd >= 410 || *result.ValueHighUsd < 410 {
		test.Fatal("unknown bucket spend entered confirmed estimate or vanished from upper bound", result)
	}
}

// TestCyclePriorRegimesAndCaps checks metadata, disjoint jumps, skipping and age limits.
func TestCyclePriorRegimesAndCaps(test *testing.T) {
	now := cycleInstant("12:00")
	for _, fixture := range []struct {
		name           string
		minutes, count int
		change         string
		want           int
	}{
		{"weekly cap", 10080, 8, "", 4},
		{"short cap", 300, 20, "", 12},
		{"short age", 1440 - 1, 20, "", 7},
		{"plan", 10080, 4, "plan", 1},
		{"structure", 10080, 4, "structure", 1},
		{"duration", 10080, 4, "duration", 1},
		{"jump", 10080, 4, "jump", 1},
		{"small disjoint change", 10080, 4, "small", 4},
		{"skip inconsistent", 10080, 4, "skip", 1},
		{"current plan", 10080, 4, "current plan", 0},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			current := fittedFixture(now.Add(time.Hour), fixture.minutes, 50, 150)
			cycles := []FittedCycle{}
			for index := 0; index < fixture.count; index++ {
				reset := now.Add(-time.Duration(index) * time.Duration(fixture.minutes) * time.Minute)
				if fixture.name == "short age" {
					reset = now.Add(-time.Duration(index)*24*time.Hour - time.Minute)
				}
				cycle := fittedFixture(reset, fixture.minutes, 99, 101)
				if index > 0 {
					switch fixture.change {
					case "plan":
						cycle.PlanType = "pro"
					case "structure":
						cycle.WindowStructure += ";secondary:10080"
					case "duration":
						cycle.WindowMinutes++
					case "jump":
						cycle.Value.ValueLowUsd, cycle.Value.ValueHighUsd = usd(299), usd(301)
						cycle.Value.ValueUsd = usd(300)
					case "small":
						cycle.Value.ValueLowUsd, cycle.Value.ValueHighUsd = usd(109), usd(111)
						cycle.Value.ValueUsd = usd(110)
					}
				}
				if index == 1 && fixture.change == "skip" {
					cycle.Value.Status = "inconsistent"
					cycle.PlanType = "different"
				}
				cycles = append(cycles, cycle)
			}
			if fixture.change == "current plan" {
				current.PlanType = "pro"
			}
			result := CombineCyclePrior(current, cycles, now)
			if result.PriorCycleCount != fixture.want || len(result.History) != min(6, len(cycles)) {
				test.Fatalf("got %d want %d: %+v", result.PriorCycleCount, fixture.want, result)
			}
		})
	}
}

// TestFitCyclesUsesIndependentResetIdentities prevents cross-cycle intersections.
func TestFitCyclesUsesIndependentResetIdentities(test *testing.T) {
	start := cycleInstant("00:00")
	minutes := 300
	observations := []CycleObservation{}
	records := []CostRecord{}
	for index := 0; index < 2; index++ {
		base := start.Add(time.Duration(index) * 5 * time.Hour)
		spend := 10.0 + float64(index)*90
		records = append(records, CostRecord{At: base.Add(10 * time.Minute), CostUsd: spend})
		observations = append(observations, CycleObservation{CycleSample: CycleSample{checked(base.Add(time.Hour)), 25, checked(base.Add(5 * time.Hour)), &minutes}, WindowId: "primary", Source: "logs"})
		observations = append(observations, CycleObservation{CycleSample: CycleSample{checked(base.Add(2 * time.Hour)), 50, checked(base.Add(5*time.Hour + 2*time.Second)), &minutes}, WindowId: "primary", Source: "logs"})
	}
	fits := FitCycles(observations, "codex", "codex", "primary", NewCostHistory(records, start.Add(12*time.Hour), start), start.Add(12*time.Hour), DefaultCycleLag)
	if len(fits) != 2 || fits[0].Value.Status != "estimated" || fits[1].Value.Status != "estimated" || fits[0].ObservationCount != 2 || fits[1].ObservationCount != 2 || *fits[0].Value.ValueLowUsd <= *fits[1].Value.ValueHighUsd {
		test.Fatal(fits)
	}
}

// TestProbeCycleIdentityAndExpiredPrior preserves reset identity and rejects absent current cycles.
func TestProbeCycleIdentityAndExpiredPrior(test *testing.T) {
	start, now := cycleInstant("10:00"), cycleInstant("12:00")
	minutes := 300
	reset := checked(start.Add(5 * time.Hour))
	series := CycleSeries{Samples: []CycleSample{{checked(start.Add(30 * time.Minute)), 50, reset, &minutes}}}
	history := NewCostHistory([]CostRecord{{At: start.Add(10 * time.Minute), CostUsd: 50}}, now, start)
	value := EstimateCycles(series, nil, "codex", Window{Id: "primary", UsedPercent: 10, ResetsAt: reset, WindowDurationMins: &minutes}, checked(start.Add(time.Hour)), "plus", "primary:300", history, now)
	if value.Status != "estimated" || len(value.Samples) != 2 {
		test.Fatal("same-reset percentage drop discarded constraints", value)
	}
	previous := fittedFixture(now.Add(-time.Hour), minutes, 90, 110)
	previous.Value.WindowId = "primary"
	expired := EstimateCyclesWithHistory(CycleSeries{}, nil, []FittedCycle{previous}, "codex", Window{Id: "primary", UsedPercent: 50, ResetsAt: checked(now.Add(-time.Minute)), WindowDurationMins: &minutes}, checked(now.Add(-time.Hour)), "plus", previous.WindowStructure, history, now)
	if expired.Status != "noCycle" || expired.IsPriorUsed || expired.ValueUsd != nil || len(expired.History) == 0 {
		test.Fatal("expired cycle acquired a fabricated estimate", expired)
	}
}

// TestCurrentCycleFollowsLatestObservation handles a weekly-to-short-window regime change.
func TestCurrentCycleFollowsLatestObservation(test *testing.T) {
	now := cycleInstant("12:00")
	weekly, short := 10080, 300
	logs := []CycleObservation{{CycleSample: CycleSample{checked(now.Add(-30 * time.Minute)), 50, checked(now.Add(4 * 24 * time.Hour)), &weekly}, WindowId: "primary", Source: "logs", PlanType: "plus", WindowStructure: "primary:10080"}}
	history := NewCostHistory([]CostRecord{{At: now.Add(-40 * time.Minute), CostUsd: 50}}, now, now.Add(-8*24*time.Hour))
	for _, offset := range []time.Duration{4 * time.Hour, -time.Minute} {
		window := Window{Id: "primary", UsedPercent: 10, ResetsAt: checked(now.Add(offset)), WindowDurationMins: &short}
		value := EstimateCycles(CycleSeries{}, logs, "codex", window, checked(now.Add(-10*time.Minute)), "plus", "primary:300", history, now)
		if offset > 0 && (value.UsedPercent != 10 || value.CycleStartedAt != checked(now.Add(-time.Hour)) || value.ResetsAt != window.ResetsAt) {
			test.Fatal("older weekly reset superseded the new short regime", value)
		}
		if offset < 0 && value.Status != "noCycle" {
			test.Fatal("expired short regime revived an older weekly window", value)
		}
	}
}

// BenchmarkFitCycles250K measures bounded output over realistic observation volume.
func BenchmarkFitCycles250K(benchmark *testing.B) {
	start := cycleInstant("00:00")
	minutes := 10080
	observations := make([]CycleObservation, 250000)
	records := make([]CostRecord, 250000)
	for index := range observations {
		cycle := index / 25000
		at := start.Add(time.Duration(cycle)*7*24*time.Hour + time.Duration(index%25000+1)*time.Second)
		observations[index] = CycleObservation{CycleSample: CycleSample{checked(at), float64(index%25000+1) / 250, checked(start.Add(time.Duration(cycle+1) * 7 * 24 * time.Hour)), &minutes}, WindowId: "primary", Source: "logs"}
		records[index] = CostRecord{At: at, CostUsd: 0.004}
	}
	now := start.Add(75 * 24 * time.Hour)
	history := NewCostHistory(records, now, start)
	benchmark.ResetTimer()
	for index := 0; index < benchmark.N; index++ {
		fits := FitCycles(observations, "codex", "codex", "primary", history, now, DefaultCycleLag)
		if len(fits) != 10 {
			benchmark.Fatal(len(fits))
		}
	}
}
