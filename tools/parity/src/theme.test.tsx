/** Original generator and version-one parser form the later Go parity oracle. */
import { expect, it } from 'vitest';
import { THEME_COLOR_ROLES, BUILT_IN_THEMES } from '../upstream/packages/shared/src/themePalettes';
import { createVividThemeColors, parseThemeFile, serializeThemeFile } from '../upstream/apps/web/src/themePalette';
import { readFileSync, writeFileSync } from 'node:fs';

it('generates every one of the 57 roles for both appearances', () => {
  expect(THEME_COLOR_ROLES).toHaveLength(57);
  for (const appearance of ['light', 'dark'] as const) {
    const colors = createVividThemeColors(appearance, '#172554', '#f97316');
    expect(Object.keys(colors).sort()).toEqual([...THEME_COLOR_ROLES].sort());
    expect(Object.values(colors).every((value) => typeof value === 'string' && value.length > 0)).toBe(true);
  }
});
it('round-trips all five paired builtin palettes through the original v1 format', () => {
  expect(BUILT_IN_THEMES).toHaveLength(5);
  for (const theme of BUILT_IN_THEMES) {
    const custom = { ...theme, id: `fixture-${theme.id}` };
    const parsed = parseThemeFile(JSON.parse(serializeThemeFile(custom)));
    expect(parsed.colors).toEqual(custom.colors);
    expect(parsed.variants).toEqual(custom.variants);
  }
});

it('matches committed golden palettes for hex, alpha, oklch, and grayscale seeds', () => {
  const inputs = [
    { background: '#172554', accent: '#f97316' },
    { background: '#ffffff', accent: '#000000' },
    { background: 'oklch(0.22 0.04 260)', accent: '#ff006680' },
  ];
  const cases = inputs.flatMap((input) => (['light', 'dark'] as const).map((appearance) => ({ ...input, appearance, colors: createVividThemeColors(appearance, input.background, input.accent) })));
  const file = new URL('../fixtures/theme-golden.json', import.meta.url);
  if (process.env.UPDATE_REFERENCE === '1') writeFileSync(file, JSON.stringify(cases, null, 2) + '\n');
  expect(cases).toEqual(JSON.parse(readFileSync(file, 'utf8')));
});
