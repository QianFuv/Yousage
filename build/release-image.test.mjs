/** Test immutable registry version tags and fail-closed network/authentication behavior. */
import assert from "node:assert/strict";
import test from "node:test";
import { shouldPushImage } from "./release-image.mjs";

const IMAGE = {
  image: "ghcr.io/example/yousage-relay:0.1.9",
  localDigest: `sha256:${"a".repeat(64)}`,
  actor: "example",
  credential: "synthetic-credential",
};

/** Supply isolated synthetic registry responses without contacting or authenticating any service. */
function registry(status, body) {
  let calls = 0;
  return async () =>
    ++calls === 1
      ? { ok: true, json: async () => ({ token: "synthetic-token" }) }
      : { ok: status === 200, status, json: async () => body };
}

test("only a confirmed absent image tag permits a version push", async () => {
  assert.equal(
    await shouldPushImage(
      IMAGE,
      registry(404, { errors: [{ code: "MANIFEST_UNKNOWN" }] }),
    ),
    true,
  );
  assert.equal(
    await shouldPushImage(
      IMAGE,
      registry(200, {
        schemaVersion: 2,
        config: { digest: IMAGE.localDigest },
      }),
    ),
    false,
  );
  await assert.rejects(
    shouldPushImage(
      IMAGE,
      registry(200, {
        schemaVersion: 2,
        config: { digest: `sha256:${"b".repeat(64)}` },
      }),
    ),
    /refusing overwrite/,
  );
  await assert.rejects(
    shouldPushImage(IMAGE, registry(503, {})),
    /lookup failed/,
  );
  await assert.rejects(shouldPushImage(IMAGE, registry(404, {})), /absence/);
  await assert.rejects(
    shouldPushImage(IMAGE, async () => ({ ok: false, status: 403 })),
    /authorization failed/,
  );
  await assert.rejects(
    shouldPushImage(IMAGE, async () => {
      throw new Error("synthetic network error");
    }),
    /network/,
  );
});
