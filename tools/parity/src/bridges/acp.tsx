/** ACP registry is not part of the six-provider usage fixture; prohibit accidental use. */
export function AcpRegistryAgentIcon(): never { throw new Error('ACP is outside the reference slice'); }
/** No reference fixture requests an ACP image. */
export function officialAcpRegistryIconUrlForAgentId(): null { return null; }
/** No reference fixture requests an ACP image. */
export function resolveOfficialAcpRegistryIconUrl(): null { return null; }
