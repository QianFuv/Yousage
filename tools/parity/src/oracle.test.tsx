/** Hand-calculated acceptance cases executed against unmodified pinned upstream functions. */
import { describe, expect, it } from 'vitest';
import { priceUsage, cacheSavingsUsd, createOverrideRateTable } from '../upstream/apps/server/src/usage/usagePricing';
import { totalTokens } from '../upstream/packages/provider-core/src/server/usage';
import { UsageAggregator, resolveModelAliases } from '../upstream/apps/server/src/usage/usageAggregation';
import { enumerateHourStarts } from '../upstream/packages/shared/src/usageFormat';
import { remainingPercent, elapsedShare, paceOf } from '../upstream/packages/shared/src/usageLimits';
import { buildPeriodColumns } from '../upstream/apps/web/src/components/usage/UsageProviderChart';
import { mergeUsage } from '../upstream/packages/shared/src/usageMerge';
import { collectLimitAccounts, collectLimitPools } from '../upstream/packages/shared/src/usageLimits';
import { RECORD, RATES, TOTALS } from './fixtures';

describe('billing and time semantics', () => {
  it('resolves exact alias chains and leaves cyclic aliases unpriced', () => {
    expect([...resolveModelAliases({ a: 'b', b: 'fixture-model', c: 'd', d: 'c' })]).toEqual([['a', 'fixture-model'], ['b', 'fixture-model']]);
  });
  it('does not count reasoning twice and splits standard cost', () => {
    expect(totalTokens(TOTALS)).toBe(3600);
    const result = priceUsage(RATES, RECORD);
    expect(result.costUsd).toBeCloseTo(0.00465, 12);
    expect(cacheSavingsUsd(RATES, RECORD)).toBeCloseTo(0.0036, 12);
  });
  it('keeps provider-reported costs and unknown category provenance', () => {
    expect(priceUsage(RATES, { ...RECORD, reportedCostUsd: 0.0093 }).costUsd).toBe(0.0093);
    expect(priceUsage(new Map(), { ...RECORD, reportedCostUsd: 0.0093 }).categoryCostUsd).toBeNull();
    expect(priceUsage(new Map(), RECORD).costSource).toBe('unpriced');
  });
  it('lets explicit prices override reported cost with blank cache fallback', () => {
    const overrides = createOverrideRateTable({ 'fixture-model': { inputCostPerMillionTokens: 2, outputCostPerMillionTokens: 10 } });
    expect(priceUsage(RATES, { ...RECORD, reportedCostUsd: 99 }, overrides).costUsd).toBeCloseTo(0.008, 12);
  });
  it('buckets the same instant by UTC, Shanghai, and Los Angeles calendar days', () => {
    const observed = ['UTC', 'Asia/Shanghai', 'America/Los_Angeles'].map((timeZone) => {
      const aggregator = new UsageAggregator({ rates: RATES, timeZone, sinceDay: '2026-09-30', untilDay: '2026-10-02' });
      aggregator.add({ ...RECORD, timestampMs: Date.parse('2026-10-01T02:00:00Z') });
      return aggregator.finish().buckets[0].day;
    });
    expect(observed).toEqual(['2026-10-01', '2026-10-01', '2026-09-30']);
  });
  it('uses fixed 24 hour slots across DST', () => {
    expect(enumerateHourStarts('2026-11-01T04:30:00Z', '2026-11-02T04:30:00Z')).toHaveLength(24);
  });
  it('deduplicates records and excludes the exact end instant', () => {
    const aggregator = new UsageAggregator({ rates: RATES, timeZone: 'UTC', sinceDay: '2026-10-01', untilDay: '2026-10-02', resolution: 'hour', sinceTimeMs: RECORD.timestampMs, untilTimeMs: RECORD.timestampMs + 86400000 });
    aggregator.add(RECORD, '/fixture');
    aggregator.add(RECORD, '/fixture');
    aggregator.add({ ...RECORD, dedupeKey: 'end', timestampMs: RECORD.timestampMs + 86400000 }, '/fixture');
    const result = aggregator.finish();
    expect(result.buckets).toHaveLength(1);
    expect(result.buckets[0].records).toBe(1);
  });
  it('uses remaining quota and strict five-point pace boundaries', () => {
    const now = Date.parse('2026-10-01T00:00Z');
    const window = { usedPercent: 55, windowDurationMins: 300, resetsAt: '2026-10-01T02:30Z' };
    expect(remainingPercent(window)).toBe(45);
    expect(elapsedShare(window, now)).toBe(0.5);
    expect(paceOf(window, now)).toBe('on');
    expect(paceOf({ ...window, usedPercent: 55.1 }, now)).toBe('ahead');
    expect(paceOf({ ...window, usedPercent: 44.9 }, now)).toBe('under');
  });
  it('uses independent provider bands', () => {
    const columns = buildPeriodColumns(['2026-10-01'], new Map([['2026-10-01', { byProvider: new Map([['codex', { costUsd: 2, totalTokens: 4 }], ['claude', { costUsd: 3, totalTokens: 6 }]]) }]]), 'cost');
    expect(columns[0].total).toBe(5);
    expect(columns[0].bands.filter((band) => band.value > 0).map((band) => band.value)).toEqual([2, 3]);
  });
});

it('keeps complete source cells, supplements only absent partial cells, and counts distinct sessions', () => {
  const fingerprint = { hostId: 'fixture-host', provider: 'claude', resolvedHomePath: '/fixture', volumeId: 'fixture-volume' };
  const bucket = { day: '2026-10-01', provider: 'claude', model: 'fixture-model', sourcePath: '/fixture', totals: TOTALS, costUsd: 0.00465, cacheSavingsUsd: 0.0036, costSource: 'modelPriced', records: 1, unpricedRecords: 0, sessions: 1 };
  const summary = { contractVersion: 6, readAt: '2026-10-01T13:00:00Z', buckets: [bucket], sources: [{ fingerprint, status: 'ok', distinctSessions: 1 }] };
  const older = { environmentId: 'a', label: 'Complete', summary };
  const newer = { environmentId: 'b', label: 'Partial', summary: { ...summary, readAt: '2026-10-02T13:00:00Z', sources: [{ fingerprint, status: 'partial', distinctSessions: 1 }], buckets: [{ ...bucket, costUsd: 99 }, { ...bucket, day: '2026-10-02' }] } };
  const merged = mergeUsage([newer, older], 6);
  expect(merged.totalTokens).toBe(7200);
  expect(merged.costUsd).toBeCloseTo(0.0093, 12);
  expect(merged.sessions).toBe(1);
  expect(mergeUsage([older, { ...older, environmentId: 'c' }], 6).totalTokens).toBe(3600);
  expect(mergeUsage([{ ...older, summary: { ...summary, contractVersion: 3 } }], 6).contractMismatches[0].direction).toBe('serverBehind');
});

it('deduplicates normalized account emails and pools equal accounts with sparse columns', () => {
  const provider = { driver: 'codex', instanceId: 'fixture', displayName: 'Fixture', enabled: true, installed: true, auth: { status: 'authenticated', email: 'Alice@Example.com' }, usageLimits: { checkedAt: '2026-10-01T00:00Z', windows: [{ id: 'primary', kind: 'session', label: '5h', usedPercent: 20, resetsAt: '2026-10-01T01:00Z' }] } };
  const presentation = { entry: { target: { label: 'Fixture environment' } }, serverConfig: { providers: [provider] } };
  const accounts = collectLimitAccounts(new Map([['a', presentation], ['b', { ...presentation, serverConfig: { providers: [{ ...provider, auth: { ...provider.auth, email: ' alice@example.com ' } }] } }]]));
  expect(accounts).toHaveLength(1);
  expect(accounts[0].environments).toHaveLength(2);
  const pools = collectLimitPools([accounts[0], { ...accounts[0], key: 'second', limits: { ...accounts[0].limits, windows: [{ ...provider.usageLimits.windows[0], usedPercent: 80 }] } }, { ...accounts[0], key: 'sparse', limits: { ...accounts[0].limits, windows: [{ id: 'weekly', kind: 'weekly', label: 'Weekly', usedPercent: 10 }] } }], Date.parse('2026-10-01T00:00Z'));
  expect(pools[0].windows[0].remainingPercent).toBe(50);
  expect(pools[0].windows[0].columns.filter((column) => column.window === null)).toHaveLength(1);
  expect(pools[0].windows[0].resets.map((reset) => reset.restoresPercent).sort()).toEqual([10, 40]);
});
