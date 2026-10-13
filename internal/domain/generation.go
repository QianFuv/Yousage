package domain

import "time"

// MaxGenerationMs excludes intervals exceeding ten minutes, which may contain idle or tool waits.
const MaxGenerationMs = int64(10 * time.Minute / time.Millisecond)

// MaxGenerationTokensPerSecond rejects implausible per-request output rates.
const MaxGenerationTokensPerSecond = 10000.0

// ReliableGenerationMs accepts positive bounded intervals with nonzero inclusive output tokens.
// The elapsed interval includes time to first token when the provider records request creation.
func ReliableGenerationMs(durationMs, outputTokens int64) int64 {
	if durationMs <= 0 || durationMs > MaxGenerationMs || outputTokens <= 0 || float64(outputTokens)*1000/float64(durationMs) > MaxGenerationTokensPerSecond {
		return 0
	}
	return durationMs
}

// ReliableTtftMs accepts a positive bounded time to first token that does not exceed a measured generation.
// It stays independent of the generation interval, which may be unmeasured while the first token is not.
func ReliableTtftMs(ttftMs, generationMs int64) int64 {
	if ttftMs <= 0 || ttftMs > MaxGenerationMs || (generationMs > 0 && ttftMs > generationMs) {
		return 0
	}
	return ttftMs
}
