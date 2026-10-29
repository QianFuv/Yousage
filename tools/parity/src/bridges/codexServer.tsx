/** Codex home discovery and service-tier history need the original server; transcript parsing never calls them. */
export const CodexUsageHistory = {};
export const resolveCodexHomeLayout = (): never => { throw new Error('Codex home discovery is not bridged.'); };
