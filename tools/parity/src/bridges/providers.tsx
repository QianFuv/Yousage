/** Package-provided drivers are outside the usage reference slice; built-in icons cover its providers. */
export const providerClients: ReadonlyMap<string, { readonly label: string; readonly icon?: { readonly fill: { readonly light: string; readonly dark: string } } }> = new Map();
/** No reference fixture renders a package-provided provider glyph. */
export function ProviderPackageIcon(): never { throw new Error('Provider packages are outside the reference slice'); }
