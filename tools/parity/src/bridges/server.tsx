/** No server environment publishes themes into the reference slice. */
import { Atom } from 'effect/reactivity';
import type { EnvironmentTheme } from '@t3tools/contracts';

export const primaryServerEnvironmentThemesAtom = Atom.make<ReadonlyArray<EnvironmentTheme>>([]);
