package quota

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// cycleInstant supplies deterministic UTC fixture timestamps.
func cycleInstant(value string) time.Time {
	return sampleTime("2026-10-06T" + value + ":00Z")
}

// TestPercentInterval checks rounding coverage at integral, fractional and endpoint values.
func TestPercentInterval(test *testing.T) {
	for _, fixture := range []struct{ used, low, high float64 }{
		{37, 36.5, 38}, {37.25, 37.245, 37.26}, {0, 0, 1}, {100, 99.5, 100}, {100.5, 99.5, 100}, {-1, 0, 1}, {math.NaN(), 0, 1}, {math.Inf(1), 0, 1},
	} {
		low, high := PercentInterval(fixture.used)
		if math.Abs(low-fixture.low) > 1e-9 || math.Abs(high-fixture.high) > 1e-9 {
			test.Fatalf("%v: got [%v, %v], want [%v, %v]", fixture.used, low, high, fixture.low, fixture.high)
		}
	}
}

// TestCycleRobustFit covers proportional recovery, quantized plateaus and outliers.
func TestCycleRobustFit(test *testing.T) {
	for _, name := range []string{"proportional", "integral plateaus", "outliers", "lag"} {
		test.Run(name, func(test *testing.T) {
			start := cycleInstant("10:00")
			minutes := 300
			reset := checked(start.Add(5 * time.Hour))
			samples := []CycleSample{}
			records := []CostRecord{}
			for index := 1; index <= 30; index++ {
				at := start.Add(time.Duration(index) * 5 * time.Minute)
				used, cost := float64(index), 2.0
				if name == "integral plateaus" {
					used = math.Floor(float64(index) / 3)
					cost = 2.0 / 3
				}
				if name == "outliers" && (index == 4 || index == 12 || index == 24) {
					used += 25
				}
				recordAt := at
				if name == "lag" {
					recordAt = at.Add(-time.Minute)
				}
				records = append(records, CostRecord{At: recordAt, CostUsd: cost})
				samples = append(samples, CycleSample{checked(at), used, reset, &minutes})
			}
			lag := time.Duration(0)
			if name == "lag" {
				lag = time.Minute
			}
			now := start.Add(3 * time.Hour)
			result := estimateConstraints(CycleValue{UsedPercent: samples[len(samples)-1].UsedPercent, Samples: []CycleValueSample{}}, samples, start, NewCostHistory(records, now, start), now, lag)
			if result.Status != "estimated" || result.ValueUsd == nil || math.Abs(*result.ValueUsd-200) > 12 {
				test.Fatalf("%s: %+v", name, result)
			}
			if *result.ValueLowUsd > 200 || *result.ValueHighUsd < 200 {
				test.Fatal("true value outside uncertainty band", result)
			}
			if name == "integral plateaus" && result.ConstraintCount >= len(samples) {
				test.Fatal("plateaus weighted as repeated evidence")
			}
			if name == "lag" {
				unshifted := estimateConstraints(CycleValue{UsedPercent: 30}, samples, start, NewCostHistory(records, now, start), now, 5*time.Minute)
				if *unshifted.ValueUsd == *result.ValueUsd {
					test.Fatal("timestamp shift had no effect")
				}
			}
		})
	}
}

// TestCycleValueStatuses distinguishes sparse, saturated, missing and genuinely poor fits.
func TestCycleValueStatuses(test *testing.T) {
	start, now := cycleInstant("10:00"), cycleInstant("12:00")
	minutes := 300
	reset := checked(start.Add(5 * time.Hour))
	for _, fixture := range []struct {
		name, status string
		used         []float64
		costs        []float64
	}{
		{"one tick", "insufficientData", []float64{25}, []float64{50}},
		{"plateau", "insufficientData", []float64{25, 25, 25}, []float64{40, 1, 1}},
		{"zero", "insufficientData", []float64{0, 0}, []float64{1, 1}},
		{"saturated", "insufficientData", []float64{100, 100}, []float64{100, 100}},
		{"elsewhere", "noLocalUsage", []float64{25}, nil},
		{"proportional", "estimated", []float64{10, 20, 30}, []float64{10, 10, 10}},
		{"poor fit", "inconsistent", []float64{80, 60, 40, 20, 10}, []float64{5, 5, 5, 5, 5}},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			samples := []CycleSample{}
			records := []CostRecord{}
			for index, used := range fixture.used {
				at := start.Add(time.Duration(index+1) * 10 * time.Minute)
				samples = append(samples, CycleSample{checked(at), used, reset, &minutes})
				if index < len(fixture.costs) {
					records = append(records, CostRecord{At: at, CostUsd: fixture.costs[index]})
				}
			}
			result := estimateConstraints(CycleValue{UsedPercent: fixture.used[len(fixture.used)-1], Samples: []CycleValueSample{}}, samples, start, NewCostHistory(records, now, start), now, 0)
			if result.Status != fixture.status {
				test.Fatal(result)
			}
			if result.Samples == nil {
				test.Fatal("nil samples")
			}
			if (fixture.status == "estimated" || fixture.status == "inconsistent") != (result.ValueUsd != nil) {
				test.Fatal("point estimate availability", result)
			}
		})
	}
}

// TestCycleDetection checks reset jitter, early resets, missing metadata and expired cycles.
func TestCycleDetection(test *testing.T) {
	duration := 300
	for _, fixture := range []struct {
		name, previousAt, nextAt, previousReset, nextReset, now, start, status string
		previousUsed, nextUsed                                                 float64
		minutes                                                                *int
		isReset                                                                bool
	}{
		{"normal reset", "14:00", "16:00", "15:00", "20:00", "17:00", "15:00", "insufficientData", 50, 51, &duration, true},
		{"early reset", "14:00", "14:30", "15:00", "15:01", "14:40", "14:00", "insufficientData", 50, 10, &duration, true},
		{"jitter", "14:00", "14:30", "15:00", "15:04", "14:40", "10:04", "insufficientData", 50, 51, &duration, false},
		{"exact tolerance", "14:00", "14:30", "15:00", "15:05", "14:40", "10:05", "insufficientData", 50, 51, &duration, false},
		{"elapsed", "14:00", "14:30", "15:00", "15:00", "15:01", "", "noCycle", 50, 51, &duration, false},
		{"without duration", "14:00", "14:30", "15:00", "15:01", "14:40", "14:00", "insufficientData", 50, 10, nil, true},
		{"missing duration", "14:00", "14:30", "15:00", "15:01", "14:40", "", "noCycle", 50, 51, nil, false},
		{"missing reset becomes present", "14:00", "14:30", "", "15:00", "14:40", "14:00", "insufficientData", 50, 51, nil, true},
		{"elapsed reset becomes present", "14:00", "14:30", "13:00", "15:00", "14:40", "14:00", "insufficientData", 50, 51, nil, true},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			previousReset := ""
			if fixture.previousReset != "" {
				previousReset = checked(cycleInstant(fixture.previousReset))
			}
			series := CycleSeries{Samples: []CycleSample{{checked(cycleInstant(fixture.previousAt)), fixture.previousUsed, previousReset, fixture.minutes}}}
			value := EstimateCycle(series, "codex", Window{Id: "session", UsedPercent: fixture.nextUsed, ResetsAt: checked(cycleInstant(fixture.nextReset)), WindowDurationMins: fixture.minutes}, checked(cycleInstant(fixture.nextAt)), NewCostHistory([]CostRecord{{cycleInstant(fixture.nextAt), 0, true}}, cycleInstant(fixture.now), time.Time{}), cycleInstant(fixture.now))
			if value.Status != fixture.status || value.IsResetDetected != fixture.isReset {
				test.Fatalf("unexpected cycle: %+v", value)
			}
			if fixture.start != "" && value.CycleStartedAt != checked(cycleInstant(fixture.start)) {
				test.Fatalf("start=%s, want %s", value.CycleStartedAt, fixture.start)
			}
			if fixture.isReset && len(value.Samples) != 1 {
				test.Fatal("previous cycle observations leaked into current cycle")
			}
		})
	}
}

// TestCycleSampleRetention checks first/last compression, two-cycle pruning and reset boundaries.
func TestCycleSampleRetention(test *testing.T) {
	for _, fixture := range []struct {
		name string
		used []float64
		want []int
	}{
		{"single", []float64{10}, []int{0}}, {"pair", []float64{10, 10}, []int{0, 1}}, {"run", []float64{10, 10, 10, 10}, []int{0, 3}}, {"ticks", []float64{10, 10, 10, 11, 11, 11}, []int{0, 2, 3, 5}},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			series := CycleSeries{}
			for index, used := range fixture.used {
				series = AppendCycleSample(series, CycleSample{CheckedAt: checked(cycleInstant("10:00").Add(time.Duration(index) * time.Minute)), UsedPercent: used, ResetsAt: checked(cycleInstant("15:00"))})
			}
			if len(series.Samples) != len(fixture.want) {
				test.Fatalf("retained %+v", series.Samples)
			}
			for index, original := range fixture.want {
				if series.Samples[index].CheckedAt != checked(cycleInstant("10:00").Add(time.Duration(original)*time.Minute)) {
					test.Fatal("run endpoints lost")
				}
			}
		})
	}
	series := CycleSeries{}
	for index := 0; index < 3; index++ {
		series = AppendCycleSample(series, CycleSample{CheckedAt: checked(cycleInstant("10:00").Add(time.Duration(index) * time.Hour)), UsedPercent: 10, ResetsAt: checked(cycleInstant("15:00").Add(time.Duration(index) * time.Hour))})
	}
	if len(series.Samples) != 2 || series.ResetFloorAt != checked(cycleInstant("11:00")) {
		test.Fatalf("cycle pruning failed: %+v", series)
	}
	for index := 0; index < 610; index++ {
		series = AppendCycleSample(series, CycleSample{CheckedAt: checked(cycleInstant("12:00").Add(time.Duration(index+1) * time.Second)), UsedPercent: 10 + float64(index)/100, ResetsAt: checked(cycleInstant("17:00"))})
	}
	if len(series.Samples) != 600 || series.ResetFloorAt != checked(cycleInstant("11:00")) {
		test.Fatal("cap erased the reset anchor")
	}
}

// TestCompressedJitterPreservesCycle checks that omitted samples cannot manufacture a reset.
func TestCompressedJitterPreservesCycle(test *testing.T) {
	duration := 300
	series := CycleSeries{}
	for index := 0; index < 8; index++ {
		series = AppendCycleSample(series, CycleSample{CheckedAt: checked(cycleInstant("11:00").Add(time.Duration(index) * time.Minute)), UsedPercent: 37, ResetsAt: checked(cycleInstant("15:00").Add(time.Duration(index) * time.Minute)), WindowDurationMins: &duration})
	}
	if len(series.Samples) != 2 || series.ResetFloorAt != "" || len(series.CycleBoundaries) != 0 {
		test.Fatalf("jitter became a stored reset: %+v", series)
	}
	latest := series.Samples[1]
	value := EstimateCycle(series, "codex", Window{Id: "session", UsedPercent: 37, ResetsAt: latest.ResetsAt, WindowDurationMins: &duration}, latest.CheckedAt, NewCostHistory([]CostRecord{{cycleInstant("10:10"), 10, false}}, cycleInstant("12:00"), cycleInstant("09:00")), cycleInstant("12:00"))
	if value.IsResetDetected || value.CycleStartedAt != checked(cycleInstant("10:07")) || len(value.Samples) != 2 {
		test.Fatalf("jitter became an estimated reset: %+v", value)
	}
}

// TestCycleThinningKeepsConstraints retains both defining samples beyond the display cap.
func TestCycleThinningKeepsConstraints(test *testing.T) {
	for _, fixture := range []struct{ lower, upper int }{{150, 175}, {0, 199}, {151, 151}} {
		samples := make([]CycleValueSample, 200)
		for index := range samples {
			samples[index].At = checked(cycleInstant("10:00").Add(time.Duration(index) * time.Second))
		}
		result := thinCycleSamples(samples, fixture.lower, fixture.upper)
		if len(result) != 120 || result[0].At != samples[0].At || result[119].At != samples[199].At {
			test.Fatal("display cap or endpoints invalid")
		}
		for _, required := range []int{fixture.lower, fixture.upper} {
			isPresent := false
			for _, sample := range result {
				isPresent = isPresent || sample.At == samples[required].At
			}
			if !isPresent {
				test.Fatal("lost defining constraint", required)
			}
		}
	}
}

// TestCycleCoverageAndFiniteCosts rejects incomplete or unpriced cumulative costs.
func TestCycleCoverageAndFiniteCosts(test *testing.T) {
	start, now := cycleInstant("10:00"), cycleInstant("12:00")
	samples := []CycleSample{{CheckedAt: checked(start.Add(30 * time.Minute)), UsedPercent: 10}, {CheckedAt: checked(start.Add(time.Hour)), UsedPercent: 20}}
	for _, fixture := range []struct {
		name              string
		covered, retained time.Time
		records           []CostRecord
		status            string
	}{
		{"covered", now, start, []CostRecord{{start, 10, false}, {start.Add(40 * time.Minute), 10, false}}, "estimated"},
		{"missing scan", time.Time{}, start, []CostRecord{{start, 10, false}}, "insufficientData"},
		{"late retention", now, start.Add(time.Minute), []CostRecord{{start, 10, false}}, "insufficientData"},
		{"partial scan", start.Add(45 * time.Minute), start, []CostRecord{{start, 10, false}}, "insufficientData"},
		{"nonfinite", now, start, []CostRecord{{start, math.NaN(), false}, {start, math.Inf(1), false}}, "insufficientData"},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			value := estimateConstraints(CycleValue{UsedPercent: 20}, samples, start, NewCostHistory(fixture.records, fixture.covered, fixture.retained), now, DefaultCycleLag)
			if value.Status != fixture.status {
				test.Fatal(value)
			}
			if _, err := json.Marshal(value); err != nil {
				test.Fatal(err)
			}
			if fixture.name == "nonfinite" && value.UnpricedRecords != 2 {
				test.Fatal(value)
			}
		})
	}
}

// TestCycleUnpricedSampleBounds preserves diagnostics without fitting unknown cumulative spend.
func TestCycleUnpricedSampleBounds(test *testing.T) {
	start, now := cycleInstant("10:00"), cycleInstant("12:00")
	samples := []CycleSample{{CheckedAt: checked(start.Add(20 * time.Minute)), UsedPercent: 10}, {CheckedAt: checked(start.Add(40 * time.Minute)), UsedPercent: 20}, {CheckedAt: checked(start.Add(time.Hour)), UsedPercent: 30}}
	records := []CostRecord{{start, 10, false}, {start.Add(30 * time.Minute), 10, false}, {start.Add(50 * time.Minute), 0, true}}
	result := estimateConstraints(CycleValue{UsedPercent: 30}, samples, start, NewCostHistory(records, now, start), now, DefaultCycleLag)
	if result.Status != "estimated" || result.UnpricedRecords != 1 || result.ConstraintCount != 2 || result.Samples[2].IsUsable || result.Samples[2].ImpliedHighUsd != nil {
		test.Fatal(result)
	}
	if math.Abs(*result.ValueUsd-100) > 1e-6 {
		test.Fatal("early fully priced ticks discarded", result)
	}
}
