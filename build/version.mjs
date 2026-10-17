/**
 * Read, check or change the application version.
 *
 * `build/config.yml` is the source of truth; every other file that carries the
 * version must match it. Usage, from the repository root:
 *
 *   node build/version.mjs              print the version
 *   node build/version.mjs check        fail when any copy disagrees
 *   node build/version.mjs set 1.2.3    write a new version everywhere
 */
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

/** Matches a release version: three numeric parts, minor and patch below 100. */
const VERSION_PATTERN = /^(0|[1-9]\d{0,3})\.(0|[1-9]\d?)\.(0|[1-9]\d?)$/;

/**
 * Every file that carries the version, with each place it appears.
 *
 * Each pattern captures the text before the version, the version, and the text after it.
 * `format` turns the release version into the form that file stores.
 */
const LOCATIONS = [
  {
    path: "build/config.yml",
    patterns: [/^(\s+version:\s*["']?)([^\s"']+)(["']?)(?=\r?$)/m],
  },
  {
    path: "build/windows/info.json",
    format: (version) => `${version}.0`,
    patterns: [
      /("file_version":")([^"]+)(")/,
      /("product_version":")([^"]+)(")/,
      /("FileVersion":")([^"]+)(")/,
    ],
  },
  {
    path: "build/windows/info.json",
    patterns: [/("ProductVersion":")([^"]+)(")/],
  },
  {
    path: "build/windows/wails.exe.manifest",
    format: (version) => `${version}.0`,
    patterns: [
      /(<assemblyIdentity type="win32" name="app\.yousage\.desktop" version=")([^"]+)(")/,
    ],
  },
  {
    path: "build/darwin/Info.plist",
    patterns: [
      /(<key>CFBundleVersion<\/key><string>)([^<]+)(<\/string>)/,
      /(<key>CFBundleShortVersionString<\/key><string>)([^<]+)(<\/string>)/,
    ],
  },
  {
    path: "internal/sync/snapshot.go",
    patterns: [/(const AppVersion = ")([^"]+)(")/],
  },
  {
    path: "build/dependencies.json",
    patterns: [/^(\{\s*"version": ")([^"]+)(")/],
  },
];

/**
 * Read a repository file as text.
 *
 * @param path - Path relative to the repository root.
 * @returns The file content.
 */
function readText(path) {
  return readFileSync(path, "utf8");
}

/**
 * Read the source-of-truth version from `build/config.yml`.
 *
 * @returns The current release version.
 */
export function readVersion() {
  const match = LOCATIONS[0].patterns[0].exec(readText("build/config.yml"));
  if (match === null) throw new Error("build/config.yml has no info.version");
  return match[2];
}

/**
 * Convert a release version to the increasing integer Android requires for upgrades.
 *
 * @param version - Release version such as `1.2.3`.
 * @returns The Android version code, for example 10203.
 */
export function androidVersionCode(version) {
  const match = VERSION_PATTERN.exec(version);
  if (match === null) throw new Error(`invalid version: ${version}`);
  return Number(match[1]) * 10000 + Number(match[2]) * 100 + Number(match[3]);
}

/**
 * List every copy of the version that differs from the expected value.
 *
 * @param version - The version every file should carry.
 * @returns One message per mismatch or missing copy.
 */
function findMismatches(version) {
  const problems = [];
  for (const { path, patterns, format } of LOCATIONS) {
    const expected = format ? format(version) : version;
    const text = readText(path);
    for (const pattern of patterns) {
      const match = pattern.exec(text);
      if (match === null) problems.push(`${path}: version not found`);
      else if (match[2] !== expected)
        problems.push(`${path}: ${match[2]} (expected ${expected})`);
    }
  }
  return problems;
}

/**
 * Write a new version into every file that carries it.
 *
 * @param version - The new release version.
 */
function setVersion(version) {
  androidVersionCode(version);
  for (const { path, patterns, format } of LOCATIONS) {
    const value = format ? format(version) : version;
    let text = readText(path);
    for (const pattern of patterns) {
      if (!pattern.test(text)) throw new Error(`${path}: version not found`);
      text = text.replace(
        pattern,
        (_match, before, _old, after) => `${before}${value}${after}`,
      );
    }
    writeFileSync(path, text);
  }
}

/**
 * Run the command named by the process arguments.
 *
 * @param args - Command-line arguments after the script path.
 */
function main(args) {
  const [command, value] = args;
  if (command === undefined) {
    console.log(readVersion());
    return;
  }
  if (command === "check") {
    const version = readVersion();
    androidVersionCode(version);
    const problems = findMismatches(version);
    if (problems.length > 0) {
      console.error(`Version ${version} is not applied everywhere:`);
      for (const problem of problems) console.error(`  ${problem}`);
      console.error("Run: node build/version.mjs set <version>");
      process.exit(1);
    }
    console.log(`Version ${version} is consistent.`);
    return;
  }
  if (command === "set" && value !== undefined) {
    setVersion(value);
    console.log(`Version set to ${value}.`);
    return;
  }
  if (command === "android-code") {
    console.log(androidVersionCode(readVersion()));
    return;
  }
  console.error(
    "Usage: node build/version.mjs [check | set <version> | android-code]",
  );
  process.exit(2);
}

if (process.argv[1] === fileURLToPath(import.meta.url))
  main(process.argv.slice(2));
