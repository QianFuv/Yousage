/** Relay page URLs carry the object in the path and the key only in the fragment. */
import { describe, expect, it } from "vitest";
import { associatedData, parseRelayAddress } from "./relay";

const ID = "AbCdEfGhIjKlMnOpQrSt_-";

describe("relay addresses", () => {
  it("reads viewer and share links", () => {
    expect(parseRelayAddress(`/v/${ID}`, "#key")).toEqual({
      kind: "space",
      id: ID,
      key: "key",
    });
    expect(parseRelayAddress(`/s/${ID}/`, "#key")).toEqual({
      kind: "share",
      id: ID,
      key: "key",
    });
  });

  it("rejects links without a key, with a bad id or another path", () => {
    expect(parseRelayAddress(`/v/${ID}`, "")).toBeNull();
    expect(parseRelayAddress(`/v/${ID}`, "#")).toBeNull();
    expect(parseRelayAddress("/v/short", "#key")).toBeNull();
    expect(parseRelayAddress(`/x/${ID}`, "#key")).toBeNull();
    expect(parseRelayAddress(`/v/${ID}/extra`, "#key")).toBeNull();
  });

  it("binds ciphertext to the same location the desktop used", () => {
    expect(associatedData({ kind: "space", id: ID, key: "k" })).toBe(
      `space:${ID}`,
    );
    expect(associatedData({ kind: "share", id: ID, key: "k" })).toBe(
      `share:${ID}`,
    );
  });
});
