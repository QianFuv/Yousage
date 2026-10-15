/** The browser opens exactly what the desktop seals, and rejects anything else. */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { decodeBase64Url, EnvelopeError, openEnvelope } from "./envelope";

/** The shared vector the Go sync package also opens. */
const VECTOR = JSON.parse(
  readFileSync(
    join(__dirname, "../../../internal/sync/testdata/envelope_vector.json"),
    "utf8",
  ),
) as {
  keyBase64Url: string;
  aad: string;
  plaintextJson: string;
  envelopeBase64: string;
};
const ENVELOPE = Uint8Array.from(Buffer.from(VECTOR.envelopeBase64, "base64"));

/**
 * Open an envelope and report the failure stage.
 *
 * @param envelope - Envelope bytes.
 * @param key - Base64url key.
 * @param aad - Associated data.
 * @returns The failure stage, or `"opened"`.
 */
async function failureOf(envelope: Uint8Array, key: string, aad: string) {
  try {
    await openEnvelope(new Uint8Array(envelope), key, aad);
    return "opened";
  } catch (cause) {
    return cause instanceof EnvelopeError ? cause.failure : "other";
  }
}

describe("relay envelopes", () => {
  it("opens the shared test vector to its JSON", async () => {
    await expect(
      openEnvelope(new Uint8Array(ENVELOPE), VECTOR.keyBase64Url, VECTOR.aad),
    ).resolves.toEqual(JSON.parse(VECTOR.plaintextJson));
  });

  it("rejects a wrong key, a moved location and tampered bytes", async () => {
    const otherKey = VECTOR.keyBase64Url.replace(/^A/, "B");
    const tampered = new Uint8Array(ENVELOPE);
    tampered[tampered.length - 1] ^= 1;
    expect(await failureOf(ENVELOPE, otherKey, VECTOR.aad)).toBe("decrypt");
    expect(
      await failureOf(
        ENVELOPE,
        VECTOR.keyBase64Url,
        "share:AAAAAAAAAAAAAAAAAAAAAA",
      ),
    ).toBe("decrypt");
    expect(await failureOf(tampered, VECTOR.keyBase64Url, VECTOR.aad)).toBe(
      "decrypt",
    );
  });

  it("separates malformed keys, versions and truncated data", async () => {
    const otherVersion = new Uint8Array(ENVELOPE);
    otherVersion[0] = 2;
    expect(await failureOf(ENVELOPE, "short", VECTOR.aad)).toBe("key");
    expect(await failureOf(ENVELOPE, "not base64!", VECTOR.aad)).toBe("key");
    expect(await failureOf(otherVersion, VECTOR.keyBase64Url, VECTOR.aad)).toBe(
      "version",
    );
    expect(
      await failureOf(ENVELOPE.slice(0, 20), VECTOR.keyBase64Url, VECTOR.aad),
    ).toBe("format");
  });

  it("decodes unpadded base64url", () => {
    expect([...decodeBase64Url("AAECAw")!]).toEqual([0, 1, 2, 3]);
    expect(decodeBase64Url("a+b")).toBeNull();
  });
});
