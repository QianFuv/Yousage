package quota

import (
	"math"
	"sort"
	"time"
)

// CycleSample records a successful native quota observation.
type CycleSample struct {
	CheckedAt          string  `json:"checkedAt"`
	UsedPercent        float64 `json:"usedPercent"`
	ResetsAt           string  `json:"resetsAt,omitempty"`
	WindowDurationMins *int    `json:"windowDurationMins,omitempty"`
}

// CycleSeries retains two cycles and the reset boundary if the sample cap removes it.
type CycleSeries struct {
	InstanceId      string             `json:"instanceId"`
	WindowId        string             `json:"windowId"`
	ResetFloorAt    string             `json:"resetFloorAt,omitempty"`
	CycleBoundaries []string           `json:"cycleBoundaries"`
	Samples         []CycleSample      `json:"samples"`
	Observations    []CycleObservation `json:"observations,omitempty"`
}

// CycleValueSample exposes transition endpoints; implied bounds cover tick rounding and lag, not fit uncertainty.
type CycleValueSample struct {
	At             string   `json:"at"`
	UsedPercent    float64  `json:"usedPercent"`
	SpentUsd       float64  `json:"spentUsd"`
	ImpliedLowUsd  *float64 `json:"impliedLowUsd,omitempty"`
	ImpliedHighUsd *float64 `json:"impliedHighUsd,omitempty"`
	IsUsable       bool     `json:"usable"`
}

// CycleValue estimates full-cycle API-equivalent USD with a spend-constrained uncertainty band.
type CycleValue struct {
	InstanceId            string              `json:"instanceId"`
	Driver                string              `json:"driver"`
	WindowId              string              `json:"windowId"`
	Status                string              `json:"status"`
	CycleStartedAt        string              `json:"cycleStartedAt,omitempty"`
	ResetsAt              string              `json:"resetsAt,omitempty"`
	IsResetDetected       bool                `json:"resetDetected"`
	UsedPercent           float64             `json:"usedPercent"`
	UsedLowPercent        float64             `json:"usedLowPercent"`
	UsedHighPercent       float64             `json:"usedHighPercent"`
	SpentUsd              float64             `json:"spentUsd"`
	Records               int                 `json:"records"`
	UnpricedRecords       int                 `json:"unpricedRecords"`
	ValueUsd              *float64            `json:"valueUsd,omitempty"`
	ValueLowUsd           *float64            `json:"valueLowUsd,omitempty"`
	ValueHighUsd          *float64            `json:"valueHighUsd,omitempty"`
	RemainingLowUsd       *float64            `json:"remainingLowUsd,omitempty"`
	RemainingHighUsd      *float64            `json:"remainingHighUsd,omitempty"`
	ConstraintCount       int                 `json:"constraintCount"`
	Samples               []CycleValueSample  `json:"samples"`
	ObservationSource     string              `json:"observationSource"`
	IsPriorUsed           bool                `json:"priorUsed"`
	PriorCycleCount       int                 `json:"priorCycleCount"`
	IsAdjustmentSuspected bool                `json:"adjustmentSuspected"`
	History               []CycleValueHistory `json:"history"`
	confirmedSpentUsd     *float64
}

// CycleValueHistory exposes one completed cycle and its prior selection.
type CycleValueHistory struct {
	CycleStartedAt string   `json:"cycleStartedAt"`
	ResetsAt       string   `json:"resetsAt"`
	Status         string   `json:"status"`
	ValueUsd       *float64 `json:"valueUsd,omitempty"`
	ValueLowUsd    *float64 `json:"valueLowUsd,omitempty"`
	ValueHighUsd   *float64 `json:"valueHighUsd,omitempty"`
	PlanType       string   `json:"planType,omitempty"`
	IsUsedForPrior bool     `json:"usedForPrior"`
}

// PercentInterval covers both floor and nearest rounding of a reported percentage.
func PercentInterval(used float64) (float64, float64) {
	used = percent(used)
	granularity := 0.01
	if math.Abs(used-math.Round(used)) < 1e-9 {
		granularity = 1
	}
	return max(0, used-granularity/2), min(100, used+granularity)
}

// sampleTime parses native observation timestamps without accepting invalid instants.
func sampleTime(value string) time.Time {
	instant, _ := time.Parse(time.RFC3339Nano, value)
	return instant
}

// startsCycle distinguishes reset advances and drops from percent and reset jitter.
func startsCycle(previous, next CycleSample) bool {
	previousReset, nextReset := sampleTime(previous.ResetsAt), sampleTime(next.ResetsAt)
	return nextReset.Sub(previousReset) > 5*time.Minute && !previousReset.IsZero() && !nextReset.IsZero() ||
		previous.UsedPercent-next.UsedPercent >= 2 ||
		(previousReset.IsZero() || !previousReset.After(sampleTime(previous.CheckedAt))) && !nextReset.IsZero()
}

// SplitCycles partitions ordered observations at observed quota resets.
func SplitCycles(samples []CycleSample) [][]CycleSample {
	cycles := [][]CycleSample{}
	for index, sample := range samples {
		if index == 0 || startsCycle(samples[index-1], sample) {
			cycles = append(cycles, []CycleSample{})
		}
		last := len(cycles) - 1
		cycles[last] = append(cycles[last], sample)
	}
	return cycles
}

// retainedCycles preserves observed boundaries when run compression removes intermediate jitter.
func retainedCycles(samples []CycleSample, boundaries []string) [][]CycleSample {
	if boundaries == nil {
		return SplitCycles(samples)
	}
	cycles := [][]CycleSample{}
	previousGroup := -1
	for _, sample := range samples {
		group := 0
		for _, boundary := range boundaries {
			if sampleTime(sample.CheckedAt).After(sampleTime(boundary)) {
				group++
			}
		}
		if group != previousGroup {
			cycles = append(cycles, []CycleSample{})
			previousGroup = group
		}
		last := len(cycles) - 1
		cycles[last] = append(cycles[last], sample)
	}
	return cycles
}

// AppendCycleSample compresses equal-percent runs within cycles and bounds retained history.
func AppendCycleSample(series CycleSeries, sample CycleSample) CycleSeries {
	if sampleTime(sample.CheckedAt).IsZero() || math.IsNaN(sample.UsedPercent) || math.IsInf(sample.UsedPercent, 0) {
		return series
	}
	sample.UsedPercent = percent(sample.UsedPercent)
	samples := append(append([]CycleSample{}, series.Samples...), sample)
	sort.SliceStable(samples, func(first, second int) bool {
		return sampleTime(samples[first].CheckedAt).Before(sampleTime(samples[second].CheckedAt))
	})
	ordered := []CycleSample{}
	for _, value := range samples {
		if len(ordered) > 0 && sampleTime(ordered[len(ordered)-1].CheckedAt).Equal(sampleTime(value.CheckedAt)) {
			ordered[len(ordered)-1] = value
		} else {
			ordered = append(ordered, value)
		}
	}
	if series.CycleBoundaries != nil && len(series.Samples) > 0 {
		previous := series.Samples[len(series.Samples)-1]
		if sampleTime(sample.CheckedAt).After(sampleTime(previous.CheckedAt)) && startsCycle(previous, sample) {
			series.CycleBoundaries = append(append([]string{}, series.CycleBoundaries...), previous.CheckedAt)
		}
	}
	cycles := retainedCycles(ordered, series.CycleBoundaries)
	if series.CycleBoundaries == nil {
		series.CycleBoundaries = []string{}
		for _, cycle := range cycles[:max(0, len(cycles)-1)] {
			series.CycleBoundaries = append(series.CycleBoundaries, cycle[len(cycle)-1].CheckedAt)
		}
	}
	series.CycleBoundaries = series.CycleBoundaries[max(0, len(series.CycleBoundaries)-2):]
	if len(cycles) > 1 {
		previous := cycles[len(cycles)-2]
		series.ResetFloorAt = previous[len(previous)-1].CheckedAt
	}
	retained := []CycleSample{}
	for _, cycle := range cycles[max(0, len(cycles)-2):] {
		for first := 0; first < len(cycle); {
			last := first
			for last+1 < len(cycle) && cycle[last+1].UsedPercent == cycle[first].UsedPercent {
				last++
			}
			retained = append(retained, cycle[first])
			if last > first {
				retained = append(retained, cycle[last])
			}
			first = last + 1
		}
	}
	series.Samples = retained[max(0, len(retained)-600):]
	return series
}

// CostRecord supplies a timestamped API cost or an unpriced local record.
type CostRecord struct {
	At         time.Time
	CostUsd    float64
	IsUnpriced bool
}

// CostHistory indexes provider costs and the time through which all sources were scanned.
type CostHistory struct {
	CoveredThrough time.Time
	RetainedSince  time.Time
	records        []CostRecord
	costs          []float64
	unpriced       []int
	certainCosts   []float64
}

// NewCostHistory creates prefix sums once for reuse across every provider window.
func NewCostHistory(records []CostRecord, coveredThrough, retainedSince time.Time) CostHistory {
	records = append([]CostRecord{}, records...)
	sort.SliceStable(records, func(first, second int) bool { return records[first].At.Before(records[second].At) })
	history := CostHistory{CoveredThrough: coveredThrough, RetainedSince: retainedSince, records: records, costs: make([]float64, len(records)+1), unpriced: make([]int, len(records)+1)}
	for index, record := range records {
		history.costs[index+1], history.unpriced[index+1] = history.costs[index], history.unpriced[index]
		if record.IsUnpriced || record.CostUsd < 0 || math.IsNaN(record.CostUsd) || math.IsInf(record.CostUsd, 0) || math.IsInf(history.costs[index]+record.CostUsd, 0) {
			history.unpriced[index+1]++
		} else {
			history.costs[index+1] += record.CostUsd
		}
	}
	return history
}

// totals obtains inclusive cycle cost and counts with binary searches.
func (history CostHistory) totals(start, end time.Time) (float64, int, int) {
	if end.Before(start) || len(history.records) == 0 {
		return 0, 0, 0
	}
	first := sort.Search(len(history.records), func(index int) bool { return !history.records[index].At.Before(start) })
	last := sort.Search(len(history.records), func(index int) bool { return history.records[index].At.After(end) })
	return history.costs[last] - history.costs[first], last - first, history.unpriced[last] - history.unpriced[first]
}

// BucketCostRecord distinguishes known main spend from uncertain bucket attribution.
type BucketCostRecord struct {
	CostRecord
	Bucket string
}

// NewBucketCostHistory excludes other buckets and keeps unknown spend only in upper bounds.
func NewBucketCostHistory(records []BucketCostRecord, bucket string, coveredThrough, retainedSince time.Time) CostHistory {
	sort.SliceStable(records, func(first, second int) bool { return records[first].At.Before(records[second].At) })
	selected := []CostRecord{}
	certain := []float64{0}
	for _, record := range records {
		if record.Bucket != "" && record.Bucket != bucket {
			continue
		}
		selected = append(selected, record.CostRecord)
		cost := certain[len(certain)-1]
		if record.Bucket == bucket && !record.IsUnpriced && record.CostUsd >= 0 && !math.IsNaN(record.CostUsd) && !math.IsInf(record.CostUsd, 0) && !math.IsInf(cost+record.CostUsd, 0) {
			cost += record.CostUsd
		}
		certain = append(certain, cost)
	}
	history := NewCostHistory(selected, coveredThrough, retainedSince)
	history.certainCosts = certain
	return history
}

// lowerTotal includes only positively attributed priced spend.
func (history CostHistory) lowerTotal(start, end time.Time) float64 {
	if history.certainCosts == nil {
		cost, _, _ := history.totals(start, end)
		return cost
	}
	if end.Before(start) {
		return 0
	}
	first := sort.Search(len(history.records), func(index int) bool { return !history.records[index].At.Before(start) })
	last := sort.Search(len(history.records), func(index int) bool { return history.records[index].At.After(end) })
	return history.certainCosts[last] - history.certainCosts[first]
}

// usd rounds finite nonnegative amounts without overflowing during rounding.
func usd(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil
	}
	if value < math.MaxFloat64/1e6 {
		value = math.Round(value*1e6) / 1e6
	}
	return &value
}

// EstimateCycle fits transition endpoints using the live window as the latest authority.
func EstimateCycle(series CycleSeries, driver string, window Window, checkedAt string, history CostHistory, now time.Time) CycleValue {
	used := percent(window.UsedPercent)
	lowPercent, highPercent := PercentInterval(used)
	result := CycleValue{InstanceId: series.InstanceId, Driver: driver, WindowId: window.Id, Status: "noCycle", UsedPercent: used, UsedLowPercent: lowPercent, UsedHighPercent: highPercent, ResetsAt: window.ResetsAt, Samples: []CycleValueSample{}, History: []CycleValueHistory{}, ObservationSource: "probes"}
	latest := sampleTime(checkedAt)
	reset := sampleTime(window.ResetsAt)
	if latest.IsZero() || latest.After(now) || !reset.After(now) {
		return result
	}
	samples := []CycleSample{}
	for _, sample := range series.Samples {
		if instant := sampleTime(sample.CheckedAt); !instant.IsZero() && instant.Before(latest) {
			samples = append(samples, sample)
		}
	}
	samples = append(samples, CycleSample{checkedAt, used, window.ResetsAt, window.WindowDurationMins})
	sort.SliceStable(samples, func(first, second int) bool {
		return sampleTime(samples[first].CheckedAt).Before(sampleTime(samples[second].CheckedAt))
	})
	boundaries := series.CycleBoundaries
	if boundaries != nil && len(series.Samples) > 0 {
		previous := series.Samples[len(series.Samples)-1]
		if latest.After(sampleTime(previous.CheckedAt)) && startsCycle(previous, samples[len(samples)-1]) {
			boundaries = append(append([]string{}, boundaries...), previous.CheckedAt)
		}
	}
	cycles := retainedCycles(samples, boundaries)
	current := cycles[len(cycles)-1]
	floor := sampleTime(series.ResetFloorAt)
	if len(cycles) > 1 {
		previous := cycles[len(cycles)-2]
		floor = sampleTime(previous[len(previous)-1].CheckedAt)
	}
	result.IsResetDetected = !floor.IsZero()
	start := floor
	if window.WindowDurationMins != nil && *window.WindowDurationMins > 0 && int64(*window.WindowDurationMins) <= int64((1<<63-1)/time.Minute) {
		start = reset.Add(-time.Duration(*window.WindowDurationMins) * time.Minute)
		if start.Before(floor) {
			start = floor
		}
	}
	if start.IsZero() || start.After(latest) {
		return result
	}
	return estimateConstraints(result, current, start, history, now, DefaultCycleLag)
}

// estimateConstraints robustly weights covered transition endpoints by percentage precision and bounds lag.
func estimateConstraints(result CycleValue, current []CycleSample, start time.Time, history CostHistory, now time.Time, lag time.Duration) CycleValue {
	result.CycleStartedAt = checked(start)
	result.Status = "insufficientData"
	spentNow, records, unpriced := history.totals(start, now)
	result.SpentUsd, result.Records, result.UnpricedRecords = *usd(spentNow), records, unpriced
	result.confirmedSpentUsd = usd(history.lowerTotal(start, now))
	ratios := []float64{}
	usedTicks := map[float64]bool{}
	fitSamples := []CycleValueSample{}
	maxUsed := 0.0
	lagRadius := 0.0
	for index, sample := range current {
		if index > 0 && index+1 < len(current) && current[index-1].UsedPercent == sample.UsedPercent && current[index+1].UsedPercent == sample.UsedPercent {
			continue
		}
		instant := sampleTime(sample.CheckedAt)
		spent, _, sampleUnpriced := history.totals(start, instant)
		observation := CycleValueSample{At: sample.CheckedAt, UsedPercent: percent(sample.UsedPercent), SpentUsd: *usd(spent)}
		effective := instant.Add(-lag)
		isCovered := !history.CoveredThrough.IsZero() && !history.CoveredThrough.Before(instant) && !start.Before(history.RetainedSince) && !effective.Before(start) && !instant.After(now)
		if isCovered && observation.UsedPercent > 0 && observation.UsedPercent < 100 {
			certain := history.lowerTotal(start, effective)
			laggedCost, _, _ := history.totals(start, effective)
			cost := laggedCost/2 + spent/2
			low, high := PercentInterval(sample.UsedPercent)
			observation.ImpliedLowUsd = usd(certain / high * 100)
			if low > 0 && sampleUnpriced == 0 {
				observation.ImpliedHighUsd = usd(spent / low * 100)
			}
			observation.IsUsable = certain > 0 && sampleUnpriced == 0 && cost > 0
			if observation.IsUsable {
				lagRadius = max(lagRadius, math.Log(cost/laggedCost), math.Log(spent/cost))
				ratios = append(ratios, sample.UsedPercent/cost)
				usedTicks[observation.UsedPercent] = true
				fitSamples = append(fitSamples, CycleValueSample{UsedPercent: observation.UsedPercent, SpentUsd: cost})
				maxUsed = max(maxUsed, observation.UsedPercent)
				result.ConstraintCount++
			}
		}
		result.Samples = append(result.Samples, observation)
	}
	if records == 0 && result.UsedPercent > 0 {
		result.Status = "noLocalUsage"
	} else if len(usedTicks) >= 2 {
		slope := robustCycleSlope(ratios, fitSamples)
		residuals, logResiduals := []float64{}, []float64{}
		for _, sample := range fitSamples {
			residuals = append(residuals, math.Abs(sample.UsedPercent-slope*sample.SpentUsd))
			logResiduals = append(logResiduals, math.Abs(math.Log(sample.UsedPercent/sample.SpentUsd/slope)))
		}
		result.Status = "estimated"
		if len(usedTicks) >= 4 && median(residuals)/max(5, maxUsed) > 0.30 {
			result.Status = "inconsistent"
		}
		radius := max(cycleBandFloor(maxUsed), 1/max(1, maxUsed), 1.28*median(logResiduals), lagRadius)
		certain := history.lowerTotal(start, now)
		if certain > 0 && spentNow > certain {
			radius += math.Log(spentNow / certain)
		}
		setCycleBand(&result, 100/slope, radius)
	}
	result.Samples = thinCycleSamples(result.Samples, -1, -1)
	return result
}

// robustCycleSlope favors mature percentages while limiting the influence of isolated ratio outliers.
func robustCycleSlope(ratios []float64, samples []CycleValueSample) float64 {
	center := median(ratios)
	deviations := make([]float64, len(ratios))
	for index, ratio := range ratios {
		deviations[index] = math.Abs(math.Log(ratio / center))
	}
	threshold := max(0.25, 3*1.4826*median(deviations))
	type weightedRatio struct {
		ratio, weight float64
	}
	weighted := make([]weightedRatio, len(ratios))
	total := 0.0
	for index, ratio := range ratios {
		weight := samples[index].UsedPercent * samples[index].UsedPercent
		if deviations[index] > threshold {
			weight *= math.Pow(threshold/deviations[index], 2)
		}
		weighted[index] = weightedRatio{ratio, weight}
		total += weight
	}
	sort.Slice(weighted, func(first, second int) bool { return weighted[first].ratio < weighted[second].ratio })
	cumulative := 0.0
	for _, sample := range weighted {
		cumulative += sample.weight
		if cumulative >= total/2 {
			return sample.ratio
		}
	}
	return center
}

// median returns the middle of a copied, ordered sample.
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 0 {
		return ordered[middle-1]/2 + ordered[middle]/2
	}
	return ordered[middle]
}

// cycleBandFloor accounts for extrapolation drift in chronological cycle backtests.
func cycleBandFloor(used float64) float64 {
	if used <= 10 {
		return 0.25
	}
	if used <= 25 {
		return 0.25 - (used-10)*0.13/15
	}
	if used <= 50 {
		return 0.12 - (used-25)*0.04/25
	}
	return 0.08
}

// setCycleBand respects observed spend and compatible mature evidence after fitting or prior shrinkage.
func setCycleBand(result *CycleValue, point, logRadius float64) {
	confirmedSpent := result.SpentUsd
	if result.confirmedSpentUsd != nil {
		confirmedSpent = *result.confirmedSpentUsd
	}
	if point > 0 && point < confirmedSpent {
		logRadius += math.Log(confirmedSpent / point)
		point = confirmedSpent
	}
	if point > 0 && len(result.Samples) > 0 {
		latest := result.Samples[len(result.Samples)-1]
		if latest.IsUsable && latest.UsedPercent >= 25 && latest.UsedPercent == result.UsedPercent && latest.ImpliedLowUsd != nil && latest.ImpliedHighUsd != nil {
			calibrated := max(confirmedSpent, min(max(point, *latest.ImpliedLowUsd), *latest.ImpliedHighUsd))
			difference := math.Abs(math.Log(calibrated / point))
			if difference <= min(logRadius, 0.25) {
				point = calibrated
				logRadius += difference
			}
		}
	}
	result.ValueUsd = usd(point)
	result.ValueLowUsd, result.ValueHighUsd = usd(max(confirmedSpent, point*math.Exp(-logRadius))), usd(max(result.SpentUsd, point*math.Exp(logRadius)))
	if result.ValueLowUsd != nil {
		result.RemainingLowUsd = usd(max(0, *result.ValueLowUsd-result.SpentUsd))
	}
	if result.ValueHighUsd != nil {
		result.RemainingHighUsd = usd(max(0, *result.ValueHighUsd-result.SpentUsd))
	}
}

// thinCycleSamples preserves endpoints and requested diagnostic observations.
func thinCycleSamples(samples []CycleValueSample, lowerIndex, upperIndex int) []CycleValueSample {
	if len(samples) <= CycleSampleCap {
		return samples
	}
	keep := map[int]bool{0: true, len(samples) - 1: true}
	if lowerIndex >= 0 {
		keep[lowerIndex] = true
	}
	if upperIndex >= 0 {
		keep[upperIndex] = true
	}
	for index := 1; len(keep) < CycleSampleCap && index < len(samples)-1; index++ {
		position := index * (len(samples) - 1) / (CycleSampleCap - 1)
		keep[position] = true
	}
	for index := 1; len(keep) < CycleSampleCap && index < len(samples)-1; index++ {
		keep[index] = true
	}
	result := make([]CycleValueSample, 0, CycleSampleCap)
	for index, sample := range samples {
		if keep[index] {
			result = append(result, sample)
		}
	}
	return result
}
