/** Compare shared hand-calculated Codex fixtures with the unmodified pinned native reader. */
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { codexUsageFormat } from '../upstream/apps/server/src/provider/Drivers/codexUsage';
import type { UsageRecord } from '../upstream/packages/provider-core/src/server/usage';

const FIXTURE = JSON.parse(readFileSync(new URL('../fixtures/codex-usage.json', import.meta.url), 'utf8'));

/** Reduce native records to the shared, independently expected token categories. */
function tokenFields(record: UsageRecord) {
  const { uncachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, reasoningTokens } = record.totals;
  return { uncachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, reasoningTokens };
}

describe('Codex upstream accounting', () => {
  it.each(FIXTURE.cases)('$name', (fixtureCase) => {
    const state = codexUsageFormat.state!.initial();
    const records = fixtureCase.entries.flatMap((entry: unknown) => codexUsageFormat.parseLine(JSON.stringify(entry), state));
    expect(records.map(tokenFields)).toEqual(fixtureCase.expected);
    expect(codexUsageFormat.readMalformedRecords?.(state)).toBe(fixtureCase.malformedRecords);
  });
});
