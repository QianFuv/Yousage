package quota

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Cycle fitting thresholds bound lag, reset identity, prior age and regime changes.
const (
	CycleFitHorizon          = 7 * 24 * time.Hour * (max(LongCyclePriorCap, CycleHistoryCap) + 1)
	DefaultCycleLag          = time.Minute
	CycleResetTolerance      = 5 * time.Minute
	LongCyclePriorCap        = 4
	LongCycleMinimumMinutes  = 24 * 60
	ShortCyclePriorCap       = 12
	ShortCyclePriorAge       = 7 * 24 * time.Hour
	CycleAdjustmentThreshold = 0.90
	CycleHistoryCap          = 6
	CycleSampleCap           = 120
)

// CycleObservation adds provenance and bucket structure to a native sample.
type CycleObservation struct {
	CycleSample
	WindowId        string `json:"windowId"`
	PlanType        string `json:"planType,omitempty"`
	WindowStructure string `json:"windowStructure,omitempty"`
	Source          string `json:"source,omitempty"`
}

// WindowStructure identifies the complete set of windows in one bucket.
func WindowStructure(windows []Window) string {
	parts := make([]string, 0, len(windows))
	for _, window := range windows {
		minutes := 0
		if window.WindowDurationMins != nil {
			minutes = *window.WindowDurationMins
		}
		parts = append(parts, window.Id+":"+strconv.Itoa(minutes))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// AppendCycleObservation bounds probe history while preserving recent-cycle constraints.
func AppendCycleObservation(observations []CycleObservation, next CycleObservation) []CycleObservation {
	observations = append(observations, next)
	groups := [][]CycleObservation{}
	for _, observation := range observations {
		if len(groups) == 0 || sampleTime(observation.ResetsAt).Sub(sampleTime(groups[len(groups)-1][0].ResetsAt)).Abs() > CycleResetTolerance {
			groups = append(groups, []CycleObservation{})
		}
		last := len(groups) - 1
		group := groups[last]
		if len(group) >= 2 && group[len(group)-1].UsedPercent == observation.UsedPercent && group[len(group)-2].UsedPercent == observation.UsedPercent && group[len(group)-1].PlanType == observation.PlanType && group[len(group)-1].WindowStructure == observation.WindowStructure {
			group[len(group)-1] = observation
		} else {
			group = append(group, observation)
		}
		groups[last] = group
	}
	retained := []CycleObservation{}
	for _, group := range groups[max(0, len(groups)-ShortCyclePriorCap-2):] {
		retained = append(retained, group...)
	}
	return retained
}

// FittedCycle retains calibration metadata and an independent robust cycle fit.
type FittedCycle struct {
	Value            CycleValue
	PlanType         string
	WindowStructure  string
	WindowMinutes    int
	ObservationCount int
}

// FitCycles fits reset identities independently, most recent first.
func FitCycles(observations []CycleObservation, instanceId, driver, windowId string, history CostHistory, now time.Time, lag time.Duration) []FittedCycle {
	return fitCycles(observations, nil, instanceId, driver, windowId, history, now, lag)
}

// fitCycles reuses known completed fits before estimating new groups.
func fitCycles(observations []CycleObservation, fitted []FittedCycle, instanceId, driver, windowId string, history CostHistory, now time.Time, lag time.Duration) []FittedCycle {
	type timedObservation struct {
		CycleObservation
		at, reset time.Time
	}
	matchingCount := 0
	for _, observation := range observations {
		if observation.WindowId == windowId {
			matchingCount++
		}
	}
	selected := make([]timedObservation, 0, matchingCount)
	for _, observation := range observations {
		if observation.WindowId != windowId {
			continue
		}
		at, reset := sampleTime(observation.CheckedAt), sampleTime(observation.ResetsAt)
		if !at.IsZero() && !at.After(now) && reset.After(at) && observation.WindowDurationMins != nil && *observation.WindowDurationMins > 0 && int64(*observation.WindowDurationMins) <= int64((1<<63-1)/time.Minute) && !math.IsNaN(observation.UsedPercent) && !math.IsInf(observation.UsedPercent, 0) {
			selected = append(selected, timedObservation{observation, at, reset})
		}
	}
	sort.SliceStable(selected, func(first, second int) bool {
		return selected[first].reset.Before(selected[second].reset)
	})
	groups := [][]timedObservation{}
	for index, observation := range selected {
		if len(groups) == 0 || observation.reset.Sub(groups[len(groups)-1][0].reset) > CycleResetTolerance {
			groups = append(groups, selected[index:index+1])
			continue
		}
		last := len(groups) - 1
		groups[last] = selected[index-len(groups[last]) : index+1]
	}
	result := make([]FittedCycle, 0, len(groups))
	for groupIndex, group := range groups {
		sort.SliceStable(group, func(first, second int) bool {
			return group[first].at.Before(group[second].at)
		})
		latest := group[len(group)-1]
		reset := sampleTime(latest.ResetsAt)
		isKnown := false
		if !reset.After(now) {
			for _, previous := range fitted {
				if previous.Value.WindowId == windowId && sampleTime(previous.Value.ResetsAt).Sub(reset).Abs() <= CycleResetTolerance {
					result = append(result, previous)
					isKnown = true
					break
				}
			}
		}
		if isKnown {
			continue
		}
		start := reset.Add(-time.Duration(*latest.WindowDurationMins) * time.Minute)
		low, high := PercentInterval(latest.UsedPercent)
		value := CycleValue{InstanceId: instanceId, Driver: driver, WindowId: windowId, Status: "insufficientData", UsedPercent: latest.UsedPercent, UsedLowPercent: low, UsedHighPercent: high, ResetsAt: latest.ResetsAt, Samples: []CycleValueSample{}, History: []CycleValueHistory{}, ObservationSource: "none"}
		value.IsResetDetected = groupIndex > 0
		samples := make([]CycleSample, 0, len(group))
		hasLogs, hasProbes := false, false
		for _, observation := range group {
			samples = append(samples, observation.CycleSample)
			hasLogs = hasLogs || observation.Source == "logs"
			hasProbes = hasProbes || observation.Source == "probes"
		}
		if hasLogs && hasProbes {
			value.ObservationSource = "both"
		} else if hasLogs {
			value.ObservationSource = "logs"
		} else if hasProbes {
			value.ObservationSource = "probes"
		}
		value = estimateConstraints(value, samples, start, history, minTime(now, reset.Add(-time.Nanosecond)), lag)
		result = append(result, FittedCycle{Value: value, PlanType: latest.PlanType, WindowStructure: latest.WindowStructure, WindowMinutes: *latest.WindowDurationMins, ObservationCount: len(group)})
	}
	for first, last := 0, len(result)-1; first < last; first, last = first+1, last-1 {
		result[first], result[last] = result[last], result[first]
	}
	return result
}

// minTime keeps completed-cycle spend before its reset boundary.
func minTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}

// regimeChanged stops at metadata changes or disjoint jumps beyond observed log variation.
func regimeChanged(newer, older FittedCycle) bool {
	if newer.PlanType != "" && older.PlanType != "" && newer.PlanType != older.PlanType || newer.WindowMinutes != older.WindowMinutes || newer.WindowStructure != "" && older.WindowStructure != "" && newer.WindowStructure != older.WindowStructure {
		return true
	}
	if newer.Value.ValueUsd == nil || older.Value.ValueUsd == nil || *newer.Value.ValueUsd <= 0 || *older.Value.ValueUsd <= 0 || newer.Value.ValueLowUsd == nil || older.Value.ValueLowUsd == nil || newer.Value.ValueHighUsd == nil || older.Value.ValueHighUsd == nil {
		return false
	}
	return (*newer.Value.ValueHighUsd < *older.Value.ValueLowUsd || *older.Value.ValueHighUsd < *newer.Value.ValueLowUsd) && math.Abs(math.Log(*newer.Value.ValueUsd / *older.Value.ValueUsd)) > CycleAdjustmentThreshold
}

// CombineCyclePrior shrinks thin fits using predictive log uncertainty, preserving regime boundaries.
func CombineCyclePrior(current FittedCycle, completed []FittedCycle, now time.Time) CycleValue {
	result := current.Value
	result.History = []CycleValueHistory{}
	cap := LongCyclePriorCap
	if current.WindowMinutes < LongCycleMinimumMinutes {
		cap = ShortCyclePriorCap
	}
	centers := []float64{}
	selected := map[int]bool{}
	previous := current
	for index, cycle := range completed {
		if sampleTime(cycle.Value.ResetsAt).After(now) {
			continue
		}
		if cap == ShortCyclePriorCap && sampleTime(cycle.Value.ResetsAt).Before(now.Add(-ShortCyclePriorAge)) {
			break
		}
		if regimeChanged(previous, cycle) && (len(centers) > 0 || previous.PlanType != cycle.PlanType || previous.WindowMinutes != cycle.WindowMinutes || previous.WindowStructure != cycle.WindowStructure) {
			break
		}
		previous = cycle
		if cycle.Value.Status != "estimated" || cycle.Value.ValueUsd == nil || *cycle.Value.ValueUsd <= 0 {
			continue
		}
		centers = append(centers, math.Log(*cycle.Value.ValueUsd))
		selected[index] = true
		if len(centers) == cap {
			break
		}
	}
	result.PriorCycleCount = len(centers)
	for index, cycle := range completed[:min(len(completed), CycleHistoryCap)] {
		value := cycle.Value
		result.History = append(result.History, CycleValueHistory{CycleStartedAt: value.CycleStartedAt, ResetsAt: value.ResetsAt, Status: value.Status, ValueUsd: value.ValueUsd, ValueLowUsd: value.ValueLowUsd, ValueHighUsd: value.ValueHighUsd, PlanType: cycle.PlanType, IsUsedForPrior: selected[index]})
	}
	if len(centers) == 0 || result.Status == "inconsistent" || result.Status == "noCycle" || result.Status == "noLocalUsage" {
		for index := range result.History {
			result.History[index].IsUsedForPrior = false
		}
		return result
	}
	center := median(centers)
	deviations := []float64{}
	for _, value := range centers {
		deviations = append(deviations, math.Abs(value-center))
	}
	priorRadius := max(0.45, 1.28*1.4826*median(deviations))
	point, radius := center, priorRadius
	if result.ValueUsd != nil && *result.ValueUsd > 0 && result.ValueLowUsd != nil && *result.ValueLowUsd > 0 && result.ValueHighUsd != nil {
		ownCenter := math.Log(*result.ValueUsd)
		ownRadius := max(math.Log(*result.ValueUsd / *result.ValueLowUsd), math.Log(*result.ValueHighUsd / *result.ValueUsd))
		difference := math.Abs(ownCenter - center)
		if difference > ownRadius+priorRadius && difference > CycleAdjustmentThreshold {
			result.IsAdjustmentSuspected = true
			for index := range result.History {
				result.History[index].IsUsedForPrior = false
			}
			return result
		}
		weight := ownRadius * ownRadius / (ownRadius*ownRadius + priorRadius*priorRadius)
		point = ownCenter*(1-weight) + center*weight
		radius = max(cycleBandFloor(result.UsedPercent), math.Sqrt(1/(1/(ownRadius*ownRadius)+1/(priorRadius*priorRadius))))
	}
	result.IsPriorUsed, result.Status = true, "estimated"
	for index := range selected {
		result.ObservationSource = mergeObservationSource(result.ObservationSource, completed[index].Value.ObservationSource)
	}
	setCycleBand(&result, math.Exp(point), radius)
	return result
}

// mergeObservationSource includes the provenance of constraints used by a prior.
func mergeObservationSource(first, second string) string {
	if first == "" || first == "none" {
		return second
	}
	if second == "" || second == "none" || first == second {
		return first
	}
	return "both"
}

// EstimateCycles adds completed-cycle priors to log and probe observations.
func EstimateCycles(series CycleSeries, logs []CycleObservation, driver string, window Window, checkedAt, planType, structure string, history CostHistory, now time.Time) CycleValue {
	return EstimateCyclesWithHistory(series, logs, nil, driver, window, checkedAt, planType, structure, history, now)
}

// EstimateCyclesWithHistory reuses completed log fits when their history inputs are unchanged.
func EstimateCyclesWithHistory(series CycleSeries, logs []CycleObservation, fitted []FittedCycle, driver string, window Window, checkedAt, planType, structure string, history CostHistory, now time.Time) CycleValue {
	observations := append([]CycleObservation{}, logs...)
	observations = append(observations, series.Observations...)
	if len(series.Observations) == 0 {
		for _, sample := range series.Samples {
			observations = append(observations, CycleObservation{CycleSample: sample, WindowId: window.Id, Source: "probes"})
		}
	}
	hasLive := false
	for _, observation := range observations {
		hasLive = hasLive || observation.Source == "probes" && observation.WindowId == window.Id && sampleTime(observation.CheckedAt).Equal(sampleTime(checkedAt))
	}
	if !hasLive && !sampleTime(checkedAt).IsZero() {
		observations = append(observations, CycleObservation{CycleSample: CycleSample{checkedAt, window.UsedPercent, window.ResetsAt, window.WindowDurationMins}, WindowId: window.Id, Source: "probes", PlanType: planType, WindowStructure: structure})
	}
	cycles := fitCycles(observations, fitted, series.InstanceId, driver, window.Id, history, now, DefaultCycleLag)
	current := FittedCycle{Value: CycleValue{InstanceId: series.InstanceId, Driver: driver, WindowId: window.Id, Status: "noCycle", Samples: []CycleValueSample{}, History: []CycleValueHistory{}, ObservationSource: "none"}, PlanType: planType, WindowStructure: structure}
	if window.WindowDurationMins != nil {
		current.WindowMinutes = *window.WindowDurationMins
	}
	completed := []FittedCycle{}
	latestObservation := time.Time{}
	for _, cycle := range fitted {
		if cycle.Value.WindowId == window.Id {
			completed = append(completed, cycle)
		}
	}
	for _, cycle := range cycles {
		last := cycle.Value.Samples[len(cycle.Value.Samples)-1]
		if at := sampleTime(last.At); at.After(latestObservation) {
			latestObservation = at
			if sampleTime(cycle.Value.ResetsAt).After(now) {
				current = cycle
			} else {
				current.Value = CycleValue{InstanceId: series.InstanceId, Driver: driver, WindowId: window.Id, Status: "noCycle", Samples: []CycleValueSample{}, History: []CycleValueHistory{}, ObservationSource: "none"}
			}
		}
		if !sampleTime(cycle.Value.ResetsAt).After(now) {
			isKnown := false
			for _, previous := range completed {
				if sampleTime(previous.Value.ResetsAt).Sub(sampleTime(cycle.Value.ResetsAt)).Abs() <= CycleResetTolerance {
					isKnown = true
					break
				}
			}
			if !isKnown {
				completed = append(completed, cycle)
			}
		}
	}
	if len(logs) == 0 && (window.WindowDurationMins == nil || *window.WindowDurationMins <= 0) {
		current.Value = EstimateCycle(series, driver, window, checkedAt, history, now)
	}
	sort.SliceStable(completed, func(first, second int) bool {
		return sampleTime(completed[first].Value.ResetsAt).After(sampleTime(completed[second].Value.ResetsAt))
	})
	return CombineCyclePrior(current, completed, now)
}
