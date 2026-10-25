/** Refuse to replace a different existing Relay version image during publication retries. */
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import { pathToFileURL } from "node:url";

/** Determine whether the version tag is absent, equal, or conflicting; all lookup errors fail closed. */
export async function shouldPushImage(
  { image, localDigest, actor, credential },
  request = fetch,
) {
  assert.match(image, /^ghcr\.io\/[a-z0-9-]+\/yousage-relay:\d+\.\d+\.\d+$/);
  assert.match(localDigest, /^sha256:[a-f0-9]{64}$/);
  const [repository, version] = image.slice("ghcr.io/".length).split(":");
  const authorization = await request(
    `https://ghcr.io/token?service=ghcr.io&scope=repository:${repository}:pull`,
    {
      headers: {
        Authorization: `Basic ${Buffer.from(`${actor}:${credential}`).toString("base64")}`,
      },
      signal: AbortSignal.timeout(30000),
    },
  );
  assert(
    authorization.ok,
    `Registry authorization failed: ${authorization.status}`,
  );
  const { token } = await authorization.json();
  assert(
    typeof token === "string" && token.length > 0,
    "Registry authorization returned no token",
  );
  const response = await request(
    `https://ghcr.io/v2/${repository}/manifests/${version}`,
    {
      headers: {
        Authorization: `Bearer ${token}`,
        Accept:
          "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json",
      },
      signal: AbortSignal.timeout(30000),
    },
  );
  const manifest = await response.json();
  if (response.status === 404) {
    assert(
      manifest.errors?.length > 0 &&
        manifest.errors.every((error) =>
          ["MANIFEST_UNKNOWN", "NAME_UNKNOWN"].includes(error.code),
        ),
      "Registry absence could not be established",
    );
    return true;
  }
  assert(response.ok, `Registry manifest lookup failed: ${response.status}`);
  assert.equal(
    manifest.schemaVersion,
    2,
    "Unsupported registry image manifest",
  );
  assert.equal(
    manifest.config?.digest,
    localDigest,
    "Existing Relay version image differs; refusing overwrite",
  );
  return false;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const localDigest = execFileSync(
    "docker",
    ["image", "inspect", "yousage-relay:ci", "--format", "{{.Id}}"],
    { encoding: "utf8" },
  ).trim();
  const shouldPush = await shouldPushImage({
    image: process.env.RELAY_IMAGE,
    localDigest,
    actor: process.env.GITHUB_ACTOR,
    credential: process.env.GH_TOKEN,
  });
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `push=${shouldPush}\n`);
  console.log(
    shouldPush
      ? "Relay version tag is absent; publishing accepted image"
      : "Relay version image already matches; preserving it",
  );
}
