/** Relay envelope: version byte, GCM nonce and AES-256-GCM over gzip-compressed JSON. */

const ENVELOPE_VERSION = 1;
const NONCE_LENGTH = 12;
const KEY_LENGTH = 32;
const TAG_LENGTH = 16;

/** Why an envelope could not be opened. */
export type EnvelopeFailure = "key" | "version" | "decrypt" | "format";

/** An envelope that could not be opened, with the failing stage. */
export class EnvelopeError extends Error {
  readonly failure: EnvelopeFailure;

  /**
   * Create an envelope failure.
   *
   * @param failure - The stage that failed.
   */
  constructor(failure: EnvelopeFailure) {
    super(`Envelope ${failure} failure`);
    this.name = "EnvelopeError";
    this.failure = failure;
  }
}

/**
 * Decode an unpadded base64url string.
 *
 * @param text - Base64url text.
 * @returns The decoded bytes, or `null` when the text is not base64url.
 */
export function decodeBase64Url(text: string): Uint8Array<ArrayBuffer> | null {
  if (!/^[A-Za-z0-9_-]*$/.test(text)) return null;
  const padded = text.replaceAll("-", "+").replaceAll("_", "/");
  try {
    const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
    return Uint8Array.from(binary, (character) => character.charCodeAt(0));
  } catch {
    return null;
  }
}

/**
 * Inflate gzip bytes with the platform decompressor.
 *
 * @param compressed - Gzip stream bytes.
 * @returns The decompressed bytes.
 */
async function gunzip(
  compressed: Uint8Array<ArrayBuffer>,
): Promise<Uint8Array<ArrayBuffer>> {
  const stream = new Blob([compressed])
    .stream()
    .pipeThrough(new DecompressionStream("gzip"));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

/**
 * Decrypt and parse a relay envelope.
 *
 * @param envelope - Bytes downloaded from the relay.
 * @param keyText - Base64url key from the URL fragment.
 * @param associatedData - `space:<id>` or `share:<id>`, binding the data to its location.
 * @returns The parsed JSON payload.
 * @throws EnvelopeError when the key, version, authentication or content is invalid.
 */
export async function openEnvelope(
  envelope: Uint8Array<ArrayBuffer>,
  keyText: string,
  associatedData: string,
): Promise<unknown> {
  const keyBytes = decodeBase64Url(keyText);
  if (!keyBytes || keyBytes.length !== KEY_LENGTH)
    throw new EnvelopeError("key");
  if (envelope.length < 1 + NONCE_LENGTH + TAG_LENGTH)
    throw new EnvelopeError("format");
  if (envelope[0] !== ENVELOPE_VERSION) throw new EnvelopeError("version");
  let compressed: Uint8Array<ArrayBuffer>;
  try {
    const key = await crypto.subtle.importKey(
      "raw",
      keyBytes,
      "AES-GCM",
      false,
      ["decrypt"],
    );
    compressed = new Uint8Array(
      await crypto.subtle.decrypt(
        {
          name: "AES-GCM",
          iv: envelope.slice(1, 1 + NONCE_LENGTH),
          additionalData: new TextEncoder().encode(associatedData),
        },
        key,
        envelope.slice(1 + NONCE_LENGTH),
      ),
    );
  } catch {
    throw new EnvelopeError("decrypt");
  }
  try {
    return JSON.parse(new TextDecoder().decode(await gunzip(compressed)));
  } catch {
    throw new EnvelopeError("format");
  }
}
