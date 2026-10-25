/** Test release decisions and the public attachment contract with disposable bytes. */
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { writeManifest } from "./ci-artifacts.mjs";
import {
  assembleAssets,
  detectRelease,
  releaseAssetNames,
  validateAssets,
  validateIdentity,
  validatePublished,
} from "./release-contract.mjs";

const SOURCE = "a".repeat(40);
const VERSION = "0.1.9";

test("public release requires the LitRadar title and exactly three nonempty digest-bearing assets", () => {
  const release = {
    name: "Yousage v0.1.9",
    draft: false,
    prerelease: false,
    assets: releaseAssetNames(VERSION).map((name) => ({
      name,
      size: 1,
      digest: `sha256:${"a".repeat(64)}`,
    })),
  };
  validatePublished(release, VERSION);
  assert.throws(() =>
    validatePublished({ ...release, name: "Yousage 0.1.9" }, VERSION),
  );
  assert.throws(() =>
    validatePublished(
      {
        ...release,
        assets: [...release.assets, { name: "relay-image.tar.gz" }],
      },
      VERSION,
    ),
  );
  assert.throws(() => validatePublished({ ...release, draft: true }, VERSION));
  assert.throws(() =>
    validatePublished(
      {
        ...release,
        assets: release.assets.map((asset) => ({ ...asset, size: 0 })),
      },
      VERSION,
    ),
  );
  assert.throws(() =>
    validatePublished(
      {
        ...release,
        assets: release.assets.map((asset) => ({
          ...asset,
          digest: undefined,
        })),
      },
      VERSION,
    ),
  );
});

test("conflicting tags or draft sources are rejected while an exact public identity is recognized", () => {
  assert.equal(validateIdentity(null, null, SOURCE), false);
  assert.equal(
    validateIdentity({ draft: true, target_commitish: SOURCE }, null, SOURCE),
    false,
  );
  assert.equal(validateIdentity({ draft: false }, SOURCE, SOURCE), true);
  assert.throws(
    () => validateIdentity(null, "b".repeat(40), SOURCE),
    /another commit/,
  );
  assert.throws(
    () => validateIdentity({ target_commitish: "main" }, null, SOURCE),
    /another commit/,
  );
});

test("only source-bound Windows and Android bytes enter the public checksums", (context) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "yousage-release-"));
  context.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const [windows, android] = releaseAssetNames(VERSION);
  for (const [platform, architecture, name] of [
    ["windows", "amd64", windows],
    ["android", "arm64", android],
  ]) {
    const root = path.join(directory, platform);
    fs.mkdirSync(root);
    fs.writeFileSync(path.join(root, name), `synthetic ${platform} bytes`);
    writeManifest(root, SOURCE, `${platform}/${architecture}`);
  }
  assert.deepEqual(assembleAssets(directory, VERSION, SOURCE), [
    windows,
    android,
    "SHA256SUMS",
  ]);
  const assets = path.join(directory, "assets");
  assert.equal(
    fs
      .readFileSync(path.join(assets, "SHA256SUMS"), "utf8")
      .split("\n")
      .filter(Boolean).length,
    2,
  );
  fs.writeFileSync(path.join(assets, "SHA256SUMS"), "invalid checksum\n");
  assert.throws(() => validateAssets(assets, VERSION), /checksums/);
  assembleAssets(directory, VERSION, SOURCE);
  fs.writeFileSync(path.join(assets, "relay-image.tar.gz"), "not public");
  assert.throws(() => validateAssets(assets, VERSION), /Unexpected public/);
  fs.writeFileSync(path.join(directory, "android", android), "corrupt");
  assert.throws(() => assembleAssets(directory, VERSION, SOURCE), /checksums/);
});

test("push ranges release only version increases, and manual retries require matching main", (context) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "yousage-version-"));
  context.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  /** Run isolated fixture Git operations without touching the product index. */
  function git(...args) {
    return execFileSync("git", args, {
      cwd: directory,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  }
  git("init", "--initial-branch=main");
  git("config", "user.name", "Release fixture");
  git("config", "user.email", "fixture@example.invalid");
  fs.mkdirSync(path.join(directory, "build"));
  /** Commit only a disposable version fixture. */
  function commitVersion(version) {
    fs.writeFileSync(
      path.join(directory, "build/config.yml"),
      `info:\n  version: ${version}\n`,
    );
    git("add", "build/config.yml");
    git("-c", "commit.gpgsign=false", "commit", "-m", `version ${version}`);
    return git("rev-parse", "HEAD");
  }
  const before = commitVersion("0.1.8");
  const source = commitVersion(VERSION);
  const event = { event: "push", before, source, ref: "refs/heads/main" };
  assert.deepEqual(detectRelease(event, directory), {
    release: true,
    version: VERSION,
  });
  assert.equal(
    detectRelease({ ...event, before: source }, directory).release,
    false,
  );
  assert.throws(
    () =>
      detectRelease({ ...event, before: source, source: before }, directory),
    /decrease/,
  );
  assert.equal(
    detectRelease({ event: "pull_request" }, directory).release,
    false,
  );
  assert.equal(
    detectRelease({ ...event, event: "workflow_dispatch" }, directory).release,
    false,
  );
  assert.equal(
    detectRelease(
      { ...event, event: "workflow_dispatch", requested: VERSION },
      directory,
    ).release,
    true,
  );
  assert.throws(
    () =>
      detectRelease(
        { ...event, event: "workflow_dispatch", requested: "0.1.8" },
        directory,
      ),
    /match/,
  );
  assert.throws(
    () => detectRelease({ ...event, ref: "refs/heads/topic" }, directory),
    /main/,
  );
  assert.throws(
    () => detectRelease({ ...event, before: "0".repeat(40) }, directory),
    /baseline/,
  );
});
