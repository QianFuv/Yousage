/** Fixture branding only. This bridge does not implement upstream schema validation. */
export const USAGE_MERGE_COMPATIBLE_SINCE = 4;
export const UsageDay = { make: (value: string) => value };
export const ProviderDriverKind = { make: (value: string) => value };
/** Original server.ts availability predicate, including omitted-field behavior. */
export const isProviderAvailable = (snapshot: { availability?: string }): boolean => snapshot.availability !== 'unavailable';
/** Project provider policy is outside the reference slice; only the pinned fallback selection calls it. */
export const resolveProjectProviderInstanceEnabled = (): boolean => { throw new Error('Project provider policy is not bridged.'); };
