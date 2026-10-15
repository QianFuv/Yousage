/** Locate and download relay envelopes for the page's own URL. */

/** Relay object kinds addressed by the viewer and share pages. */
export type RelayKind = "space" | "share";

/** The object a page URL points at, with the fragment key that opens it. */
export type RelayAddress = {
  kind: RelayKind;
  id: string;
  key: string;
};

/** Result of reading one relay object. */
export type RelayRead =
  | { status: "ok"; envelope: Uint8Array<ArrayBuffer>; etag: string }
  | { status: "unchanged" }
  | { status: "missing" };

const ID_PATTERN = /^[A-Za-z0-9_-]{22}$/;

/**
 * Parse `/v/<id>#<key>` or `/s/<id>#<key>`.
 *
 * @param pathname - URL path.
 * @param hash - URL fragment including the leading `#`.
 * @returns The addressed object, or `null` when the URL is not a relay link.
 */
export function parseRelayAddress(
  pathname: string,
  hash: string,
): RelayAddress | null {
  const match = /^\/([vs])\/([^/]+)\/?$/.exec(pathname);
  const key = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!match || !ID_PATTERN.test(match[2]) || key === "") return null;
  return { kind: match[1] === "v" ? "space" : "share", id: match[2], key };
}

/**
 * Bind ciphertext to its relay location, as the desktop does when encrypting.
 *
 * @param address - The addressed object.
 * @returns `space:<id>` or `share:<id>`.
 */
export function associatedData(address: RelayAddress): string {
  return `${address.kind}:${address.id}`;
}

/**
 * Download an object from the relay that served this page.
 *
 * @param address - The addressed object.
 * @param etag - Previous entity tag, to skip unchanged snapshots.
 * @returns The envelope, an unchanged marker, or a missing marker.
 * @throws Error on network failures and unexpected statuses.
 */
export async function readRelayObject(
  address: RelayAddress,
  etag = "",
): Promise<RelayRead> {
  const response = await fetch(
    `/api/${address.kind === "space" ? "spaces" : "shares"}/${address.id}`,
    {
      cache: "no-store",
      headers: etag ? { "If-None-Match": etag } : undefined,
    },
  );
  if (response.status === 304) return { status: "unchanged" };
  if (response.status === 404) return { status: "missing" };
  if (!response.ok) throw new Error(`Relay returned ${response.status}`);
  return {
    status: "ok",
    envelope: new Uint8Array(await response.arrayBuffer()),
    etag: response.headers.get("ETag") ?? "",
  };
}
