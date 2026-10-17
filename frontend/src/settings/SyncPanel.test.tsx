/** The relay warning appears only when the token would cross the network unencrypted. */
import { describe, expect, it } from "vitest";
import { State } from "../../bindings/yousage/internal/sync/models";
import { isInsecureRelay, nextSyncState } from "./SyncPanel";

describe("relay address warning", () => {
  it("warns for plain HTTP to another machine", () => {
    expect(isInsecureRelay("http://192.168.1.20:8080")).toBe(true);
    expect(isInsecureRelay("http://relay.example.com")).toBe(true);
  });

  it("accepts HTTPS, loopback and unfinished input", () => {
    expect(isInsecureRelay("https://relay.example.com")).toBe(false);
    expect(isInsecureRelay("http://127.0.0.1:8090")).toBe(false);
    expect(isInsecureRelay("http://localhost:8080")).toBe(false);
    expect(isInsecureRelay("http://[::1]:8080")).toBe(false);
    expect(isInsecureRelay("https://")).toBe(false);
    expect(isInsecureRelay("")).toBe(false);
  });
});

describe("sync state after an action", () => {
  it("keeps the shown state when an action has no result", () => {
    const current = State.createFrom({ isEnabled: true, pairingUrl: "u" });
    expect(nextSyncState(current, {})).toBe(current);
    expect(nextSyncState(current, undefined)).toBe(current);
  });

  it("shows the state an action returns", () => {
    const next = State.createFrom({ isEnabled: false });
    expect(nextSyncState(null, next)).toBe(next);
  });
});
