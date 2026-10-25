/** Detect, validate and publish the source-bound Yousage release contract. */
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { verifyManifest } from "./ci-artifacts.mjs";
import { generateReleaseNotes } from "./release-notes.mjs";

/** Validate the application version supported by the existing Android version code. */
export function parseVersion(version) {
  assert.match(version, /^(0|[1-9]\d{0,3})\.(0|[1-9]\d?)\.(0|[1-9]\d?)$/);
  return version;
}

/** Compare stable versions numerically for release detection and latest protection. */
export function compareVersions(first, second) {
  const firstParts = parseVersion(first).split(".").map(Number);
  const secondParts = parseVersion(second).split(".").map(Number);
  for (let index = 0; index < firstParts.length; index++) {
    if (firstParts[index] !== secondParts[index])
      return firstParts[index] - secondParts[index];
  }
  return 0;
}

/** Read the configured version at an exact commit without modifying the checkout. */
function versionAt(commit, cwd) {
  assert.match(commit, /^[a-f0-9]{40}$/);
  const config = execFileSync("git", ["show", `${commit}:build/config.yml`], {
    cwd,
    encoding: "utf8",
  });
  return parseVersion(
    /^\s+version:\s*["']?([^\s"']+)/m.exec(config)?.[1] ?? "",
  );
}

/** Release only increased push versions or an explicit matching main-only retry. */
export function detectRelease(
  { event, before, source, ref, requested },
  cwd = process.cwd(),
) {
  if (event === "pull_request") return { release: false };
  assert.equal(ref, "refs/heads/main", "Release detection requires main");
  const version = versionAt(source, cwd);
  if (event === "push") {
    assert.match(before, /^[a-f0-9]{40}$/);
    assert(
      !/^0+$/.test(before),
      "Release detection requires an existing branch baseline",
    );
    const difference = compareVersions(version, versionAt(before, cwd));
    assert(difference >= 0, "Release version must not decrease");
    return { release: difference > 0, version };
  }
  assert.equal(event, "workflow_dispatch", "Unsupported release event");
  if (!requested) return { release: false, version };
  assert.equal(
    parseVersion(requested),
    version,
    "Retry version must match the selected main commit",
  );
  return { release: true, version };
}

/** Define the entire public attachment list, excluding CI and relay transport files. */
export function releaseAssetNames(version) {
  parseVersion(version);
  return [
    `yousage_${version}_windows_amd64.zip`,
    `yousage_${version}_android_arm64.apk`,
    "SHA256SUMS",
  ];
}

/** Reject conflicting or incomplete identities without overwriting existing assets. */
export function validateIdentity(release, taggedSource, source) {
  assert(
    !taggedSource || taggedSource === source,
    "Release tag belongs to another commit",
  );
  assert(
    !release || taggedSource || release.target_commitish === source,
    "Release belongs to another commit",
  );
  return Boolean(release && !release.draft);
}

/** Preserve original accepted bytes when resuming a partially uploaded release. */
export function validateDraftRun(release, run) {
  if (!release?.draft || release.assets.length === 0) return;
  const originalRun = /<!-- yousage-release-run: (\d+) -->/.exec(
    release.body ?? "",
  )?.[1];
  assert.equal(
    originalRun,
    run,
    `Draft assets belong to accepted workflow run ${originalRun ?? "unknown"}; re-run failed jobs on that original run to preserve exact accepted bytes`,
  );
}

/** Assemble the exact verified platform files and write a checksum manifest. */
export function assembleAssets(directory, version, source) {
  const [windows, android] = releaseAssetNames(version);
  const assets = path.join(directory, "assets");
  fs.mkdirSync(assets, { recursive: true });
  const checksums = [];
  for (const [platform, name] of [
    ["windows", windows],
    ["android", android],
  ]) {
    const platformRoot = path.join(directory, platform);
    verifyManifest(
      platformRoot,
      source,
      `${platform}/${platform === "windows" ? "amd64" : "arm64"}`,
      [name],
    );
    const bytes = fs.readFileSync(path.join(platformRoot, name));
    fs.writeFileSync(path.join(assets, name), bytes);
    checksums.push(
      `${createHash("sha256").update(bytes).digest("hex")}  ${name}`,
    );
  }
  fs.writeFileSync(
    path.join(assets, "SHA256SUMS"),
    checksums.join("\n") + "\n",
  );
  return validateAssets(assets, version);
}

/** Require an exact, nonempty attachment set and independently verify every digest. */
export function validateAssets(directory, version) {
  const names = releaseAssetNames(version);
  assert.deepEqual(
    fs.readdirSync(directory).sort(),
    [...names].sort(),
    "Unexpected public assets",
  );
  const manifest = fs.readFileSync(path.join(directory, "SHA256SUMS"), "utf8");
  const checksums =
    names
      .slice(0, 2)
      .map((name) => {
        const filename = path.join(directory, name);
        assert(
          fs.lstatSync(filename).isFile() && fs.statSync(filename).size > 0,
          "Missing release bytes",
        );
        return `${createHash("sha256").update(fs.readFileSync(filename)).digest("hex")}  ${name}`;
      })
      .join("\n") + "\n";
  assert.equal(manifest, checksums, "Public checksums differ");
  return names;
}

/** Read GitHub metadata without confusing service/authentication errors with absence. */
async function github(resource) {
  const response = await fetch(
    `${process.env.GITHUB_API_URL ?? "https://api.github.com"}/repos/${process.env.GITHUB_REPOSITORY}/${resource}`,
    {
      headers: {
        Authorization: `Bearer ${process.env.GH_TOKEN}`,
        Accept: "application/vnd.github+json",
      },
      signal: AbortSignal.timeout(30000),
    },
  );
  if (response.status === 404) return null;
  assert(response.ok, `GitHub request failed: ${response.status}`);
  return response.json();
}

/** List all releases, including drafts, to preserve exact identity and latest ordering. */
async function releases() {
  const results = [];
  for (let page = 1; ; page++) {
    const batch = await github(`releases?per_page=100&page=${page}`);
    assert(Array.isArray(batch), "Cannot list releases");
    results.push(...batch);
    if (batch.length < 100) return results;
  }
}

/** Execute fixed GitHub CLI arguments with a bounded operation lifetime. */
function gh(...args) {
  return execFileSync("gh", args, {
    encoding: "utf8",
    timeout: 300000,
    stdio: ["ignore", "pipe", "inherit"],
  });
}

/** Require public metadata to match the final release and all expected asset digests. */
export function validatePublished(release, version) {
  assert(
    release && !release.draft && !release.prerelease,
    "Release must be public and stable",
  );
  assert.equal(release.name, `Yousage v${version}`);
  assert.deepEqual(
    release.assets.map((asset) => asset.name).sort(),
    releaseAssetNames(version).sort(),
  );
  for (const asset of release.assets) {
    assert(asset.size > 0, `Empty asset: ${asset.name}`);
    assert.match(
      asset.digest,
      /^sha256:[a-f0-9]{64}$/,
      `Missing digest: ${asset.name}`,
    );
  }
}

/** Validate source-aware release context and perform only the requested publication step. */
async function main() {
  const mode = process.argv[2];
  if (mode === "detect") {
    const result = detectRelease({
      event: process.env.GITHUB_EVENT_NAME,
      before: process.env.BEFORE_SHA,
      source: process.env.GITHUB_SHA,
      ref: process.env.GITHUB_REF,
      requested: process.env.REQUESTED_VERSION,
    });
    fs.appendFileSync(
      process.env.GITHUB_OUTPUT,
      Object.entries(result)
        .map(([key, value]) => `${key}=${value}\n`)
        .join(""),
    );
    console.log(result);
    return;
  }
  const version = parseVersion(process.env.VERSION);
  const source = process.env.GITHUB_SHA;
  assert.match(source, /^[a-f0-9]{40}$/);
  const directory = ".task/release";
  if (mode === "assets") {
    assembleAssets(directory, version, source);
    verifyManifest(path.join(directory, "relay"), source, "linux/amd64", [
      "relay-image.tar.gz",
    ]);
    return;
  }
  assert(
    ["context", "prepare", "publish", "latest", "verify"].includes(mode),
    "Unknown release operation",
  );
  assert.equal(
    versionAt(source),
    version,
    "Requested version differs from source",
  );
  const tag = `v${version}`;
  const allReleases = await releases();
  const release = allReleases.find((candidate) => candidate.tag_name === tag);
  const reference = await github(`git/ref/tags/${tag}`);
  const tagged = reference ? await github(`commits/${tag}`) : null;
  const published = validateIdentity(release, tagged?.sha, source);
  if (mode === "context") {
    if (published) validatePublished(release, version);
    validateDraftRun(release, process.env.GITHUB_RUN_ID);
    fs.appendFileSync(
      process.env.GITHUB_OUTPUT,
      `build=${!published}\npublished=${published}\nversion=${version}\n`,
    );
    return;
  }
  if (mode === "verify") {
    assert.equal(tagged?.sha, source, "Published tag differs from source");
    validatePublished(release, version);
    return;
  }
  if (mode === "latest") {
    assert.equal(tagged?.sha, source, "Published tag differs from source");
    validatePublished(release, version);
    const isLatest = !allReleases.some(
      (candidate) =>
        !candidate.draft &&
        !candidate.prerelease &&
        /^v\d+\.\d+\.\d+$/.test(candidate.tag_name) &&
        compareVersions(candidate.tag_name.slice(1), version) > 0,
    );
    fs.appendFileSync(process.env.GITHUB_OUTPUT, `latest=${isLatest}\n`);
    return;
  }
  assert(!published, "Public release is immutable");
  const names = validateAssets(path.join(directory, "assets"), version);
  if (mode === "prepare") {
    assert(
      !release || release.assets.every((asset) => names.includes(asset.name)),
      "Existing draft has unexpected assets; refusing publication",
    );
    fs.writeFileSync(
      path.join(directory, "notes.md"),
      generateReleaseNotes({
        version,
        commit: source,
        repository: process.env.GITHUB_REPOSITORY,
        releases: allReleases,
        relayImage: process.env.RELAY_IMAGE,
      }) + `\n<!-- yousage-release-run: ${process.env.GITHUB_RUN_ID} -->\n`,
    );
    if (!release)
      gh(
        "release",
        "create",
        tag,
        "--target",
        source,
        "--title",
        `Yousage ${tag}`,
        "--draft",
        "--notes-file",
        path.join(directory, "notes.md"),
      );
    else
      assert.equal(
        release.name,
        `Yousage ${tag}`,
        "Existing draft title differs",
      );
    for (const name of names) {
      const filename = path.join(directory, "assets", name);
      const existing = release?.assets.find((asset) => asset.name === name);
      if (existing) {
        assert.equal(
          existing.digest,
          `sha256:${createHash("sha256").update(fs.readFileSync(filename)).digest("hex")}`,
          "Existing draft asset differs; refusing overwrite",
        );
      } else gh("release", "upload", tag, filename);
    }
    return;
  }
  assert(release?.draft, "Prepare the draft before publication");
  assert.deepEqual(
    release.assets.map((asset) => asset.name).sort(),
    [...names].sort(),
  );
  for (const asset of release.assets) {
    const bytes = fs.readFileSync(path.join(directory, "assets", asset.name));
    assert.equal(
      asset.digest,
      `sha256:${createHash("sha256").update(bytes).digest("hex")}`,
    );
  }
  const isLatest = !allReleases.some(
    (candidate) =>
      !candidate.draft &&
      !candidate.prerelease &&
      /^v\d+\.\d+\.\d+$/.test(candidate.tag_name) &&
      compareVersions(candidate.tag_name.slice(1), version) > 0,
  );
  gh("release", "edit", tag, "--draft=false", `--latest=${isLatest}`);
  fs.appendFileSync(process.env.GITHUB_OUTPUT, `latest=${isLatest}\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href)
  await main();
