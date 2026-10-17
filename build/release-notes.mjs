/** Generate categorized commit notes between published stable releases. */
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const COMMIT_TYPES = new Map([
  ["feat", "Features"],
  ["fix", "Bug fixes"],
  ["perf", "Performance"],
  ["refactor", "Refactoring"],
  ["test", "Tests"],
  ["build", "Build"],
  ["ci", "CI"],
  ["docs", "Documentation"],
  ["style", "Style"],
  ["chore", "Maintenance"],
  ["revert", "Reverts"],
  ["other", "Other commits"],
]);
const VERSION_PATTERN = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

/**
 * Compare stable numeric versions to select a release baseline.
 *
 * @param first - First three-part version.
 * @param second - Second three-part version.
 * @returns A negative, zero or positive comparison result.
 */
function compareVersions(first, second) {
  const firstParts = first.split(".").map(Number);
  const secondParts = second.split(".").map(Number);
  for (let index = 0; index < firstParts.length; index++) {
    if (firstParts[index] !== secondParts[index])
      return firstParts[index] - secondParts[index];
  }
  return 0;
}

/**
 * Escape commit subjects so Markdown and HTML preserve their literal content.
 *
 * @param subject - A commit subject from Git.
 * @returns Safe Markdown text for a list entry.
 */
function escapeSubject(subject) {
  return subject
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/([\\`*_{}\[\]()#!|~])/g, "\\$1");
}

/**
 * Include every commit after the highest reachable, older published stable release.
 *
 * @param options - Target version, immutable source, repository and published releases.
 * @returns Categorized Markdown containing commit and comparison links.
 */
export function generateReleaseNotes({
  version,
  commit,
  repository,
  releases,
  relayImage,
  serverUrl = "https://github.com",
  cwd = process.cwd(),
}) {
  assert.match(version, VERSION_PATTERN);
  assert.match(commit, /^[a-f0-9]{40}$/);
  assert.match(repository, /^[\w.-]+\/[\w.-]+$/);
  assert(Array.isArray(releases), "Cannot list published releases");
  /** Run read-only Git commands against the exact release checkout. */
  function git(...argumentsList) {
    return execFileSync("git", argumentsList, {
      cwd,
      encoding: "utf8",
      maxBuffer: 16 * 1024 * 1024,
    }).trim();
  }
  assert.equal(
    git("rev-parse", "--is-shallow-repository"),
    "false",
    "Release notes require full Git history",
  );
  const reachableTags = new Set(git("tag", "--merged", commit).split(/\r?\n/));
  const previous = releases
    .filter(
      (release) =>
        !release.draft &&
        !release.prerelease &&
        VERSION_PATTERN.test(release.tag_name?.slice(1) ?? "") &&
        release.tag_name.startsWith("v") &&
        reachableTags.has(release.tag_name) &&
        compareVersions(release.tag_name.slice(1), version) < 0,
    )
    .sort((first, second) =>
      compareVersions(second.tag_name.slice(1), first.tag_name.slice(1)),
    )[0];
  const range = previous ? `${previous.tag_name}..${commit}` : commit;
  const log = git("log", "--reverse", "--format=%H%x00%s", range, "--");
  const commits = log
    ? log.split(/\r?\n/).map((line) => {
        const separator = line.indexOf("\0");
        const hash = line.slice(0, separator);
        assert.match(hash, /^[a-f0-9]{40}$/);
        return { hash, subject: line.slice(separator + 1) };
      })
    : [];
  const baseUrl = `${serverUrl.replace(/\/$/, "")}/${repository}`;
  const groups = new Map([...COMMIT_TYPES.keys()].map((type) => [type, []]));
  for (const { hash, subject } of commits) {
    const type = /^([a-z]+)(?:\([^\r\n)]+\))?!?:\s+/.exec(subject)?.[1];
    groups
      .get(COMMIT_TYPES.has(type) ? type : "other")
      .push(
        `- ${escapeSubject(subject)} ([${hash.slice(0, 7)}](${baseUrl}/commit/${hash}))`,
      );
  }
  const sections = [
    `## Commits (${commits.length})`,
    previous
      ? `Changes since ${previous.tag_name}.`
      : "All commits in this first stable release.",
  ];
  for (const [type, entries] of groups) {
    if (entries.length)
      sections.push(
        `### ${COMMIT_TYPES.get(type)} (${entries.length})\n\n${entries.join("\n")}`,
      );
  }
  const comparison = previous
    ? `compare/${previous.tag_name}...v${version}`
    : `commits/v${version}`;
  sections.push(
    `**Full changelog:** [${previous ? `${previous.tag_name}...v${version}` : `v${version}`}](${baseUrl}/${comparison})`,
  );
  if (relayImage) sections.push(`Relay image: \`${relayImage}\``);
  const notes = sections.join("\n\n") + "\n";
  assert(
    Buffer.byteLength(notes, "utf8") <= 124000,
    "Complete commit list exceeds the release body limit",
  );
  return notes;
}

/** Read all published release pages and print notes for the workflow's exact source. */
function main() {
  const repository = process.env.GITHUB_REPOSITORY;
  assert.match(repository, /^[\w.-]+\/[\w.-]+$/);
  const pages = JSON.parse(
    execFileSync(
      "gh",
      [
        "api",
        "--paginate",
        "--slurp",
        `repos/${repository}/releases?per_page=100`,
      ],
      { encoding: "utf8", maxBuffer: 16 * 1024 * 1024 },
    ),
  );
  assert(Array.isArray(pages) && pages.every(Array.isArray));
  process.stdout.write(
    generateReleaseNotes({
      version: process.env.VERSION,
      commit: process.env.COMMIT,
      repository,
      serverUrl: process.env.GITHUB_SERVER_URL,
      releases: pages.flat(),
      relayImage: process.env.RELAY_IMAGE,
    }),
  );
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
