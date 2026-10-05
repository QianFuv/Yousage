/** Synthetic, public reference records. No user history or credentials are read. */
export const TOTALS = { uncachedInputTokens: 1000, cachedInputTokens: 2000, cacheCreationTokens: 500, outputTokens: 100, reasoningTokens: 40 };
export const RECORD = { provider: 'claude', timestampMs: Date.parse('2026-10-01T12:00:00Z'), model: 'fixture-model', sessionId: 'fixture-session', totals: TOTALS, reportedCostUsd: null, speed: 'standard', dedupeKey: 'fixture-1' } as const;
export const RATES = new Map([['fixture-model', { inputCostPerToken: 0.000002, outputCostPerToken: 0.00001, cacheReadCostPerToken: 0.0000002, cacheCreationCostPerToken: 0.0000025, fast: null, ultrafast: null }]]);
export const DAYS = ['2026-10-01', '2026-10-02', '2026-10-03', '2026-10-04', '2026-10-05', '2026-10-06'];
export const PROVIDERS = ['codex', 'claude', 'grok', 'cursor', 'opencode', 'antigravity'] as const;
export const DAILY = DAYS.map((day, dayIndex) => {
  const byProvider = new Map(PROVIDERS.map((provider, providerIndex) => [provider, { costUsd: ((dayIndex + 2) * (providerIndex + 3) % 13 + 1) * 0.37, totalTokens: ((dayIndex + 2) * (providerIndex + 3) % 13 + 1) * 45000 }]));
  return { day, byProvider, costUsd: [...byProvider.values()].reduce((sum, value) => sum + value.costUsd, 0), totalTokens: [...byProvider.values()].reduce((sum, value) => sum + value.totalTokens, 0) };
});
