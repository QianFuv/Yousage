/** Verify complete commit coverage, categorization and published-release boundaries. */
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";
import { generateReleaseNotes } from "./release-notes.mjs";

/** Create an isolated Git fixture with cleanup registered on the owning test. */
function fixture(context) {
  const cwd = mkdtempSync(join(tmpdir(), "yousage-release-notes-"));
  context.after(() => rmSync(cwd, { recursive: true, force: true }));
  /** Run Git only inside this disposable fixture. */
  function git(...argumentsList) {
    return execFileSync("git", argumentsList, { cwd, encoding: "utf8" }).trim();
  }
  git("init", "--quiet");
  /** Commit an empty fixture change without modifying the developer's identity. */
  function commit(subject) {
    git(
      "-c",
      "user.name=Release Fixture",
      "-c",
      "user.email=release-fixture@example.invalid",
      "-c",
      "commit.gpgsign=false",
      "commit",
      "--allow-empty",
      "--quiet",
      "-m",
      subject,
    );
    return git("rev-parse", "HEAD");
  }
  return { cwd, git, commit };
}

test("all commit types retain their subject and link under the matching category", (context) => {
  const repository = fixture(context);
  repository.commit("chore: initial baseline");
  repository.git("tag", "v0.1.1");
  const changes = [
    ["feat(ui)!: add a new view", "Features"],
    ["fix(data): preserve saved state", "Bug fixes"],
    ["perf: reduce loading time", "Performance"],
    ["refactor: simplify layout", "Refactoring"],
    ["test: cover migrations", "Tests"],
    ["build: package assets", "Build"],
    ["ci: publish grouped notes", "CI"],
    ["docs: document upgrades", "Documentation"],
    ["style: align formatting", "Style"],
    ["chore(release): bump version", "Maintenance"],
    ["revert: restore behavior", "Reverts"],
    ["Merge a useful change", "Other commits"],
  ];
  const hashes = changes.map(([subject]) => repository.commit(subject));
  const notes = generateReleaseNotes({
    cwd: repository.cwd,
    version: "0.1.2",
    commit: hashes.at(-1),
    repository: "QianFuv/Yousage",
    releases: [{ tag_name: "v0.1.1" }],
    relayImage: "ghcr.io/qianfuv/yousage-relay:0.1.2",
  });
  assert(notes.includes("## Commits (12)"));
  assert(!notes.includes("initial baseline"));
  for (const [index, [, category]] of changes.entries()) {
    const section = notes.split(`### ${category} (1)\n\n`)[1]?.split("\n\n")[0];
    assert(section?.includes(`/commit/${hashes[index]}`), category);
    const subject = changes[index][0];
    assert(section.includes(subject.split(": ")[1] ?? subject), category);
  }
  assert(notes.includes("compare/v0.1.1...v0.1.2"));
  assert(notes.includes("Relay image: `ghcr.io/qianfuv/yousage-relay:0.1.2`"));
});

test("baseline is the highest older reachable published stable version", (context) => {
  const repository = fixture(context);
  repository.commit("chore: old baseline");
  repository.git("tag", "v0.1.2");
  repository.commit("fix: older released change");
  repository.git("tag", "v0.1.9");
  repository.commit("fix: released change");
  repository.git("tag", "v0.1.10");
  const target = repository.commit("feat: included change");
  for (const tag of ["v0.1.11", "v0.1.12", "v0.1.13", "v0.1.16", "v0.1.17"])
    repository.git("tag", tag);
  repository.commit("fix: future branch change");
  repository.git("tag", "v0.1.14");
  const notes = generateReleaseNotes({
    cwd: repository.cwd,
    version: "0.1.16",
    commit: target,
    repository: "QianFuv/Yousage",
    releases: [
      { tag_name: "v0.1.2" },
      { tag_name: "v0.1.9" },
      { tag_name: "v0.1.10" },
      { tag_name: "v0.1.11", draft: true },
      { tag_name: "v0.1.12", prerelease: true },
      { tag_name: "v0.1.14" },
      { tag_name: "v0.1.16" },
      { tag_name: "v0.1.17" },
    ],
  });
  assert(notes.includes("Changes since v0.1.10."));
  assert(notes.includes("## Commits (1)"));
  assert(notes.includes("included change"));
  assert(!notes.includes("released change"));
  assert(!notes.includes("future branch change"));
});

test("shallow history cannot silently omit release commits", (context) => {
  const repository = fixture(context);
  repository.commit("feat: initial change");
  const commit = repository.commit("fix: latest change");
  const cwd = mkdtempSync(join(tmpdir(), "yousage-shallow-notes-"));
  context.after(() => rmSync(cwd, { recursive: true, force: true }));
  execFileSync("git", [
    "clone",
    "--quiet",
    "--depth=1",
    pathToFileURL(repository.cwd).href,
    cwd,
  ]);
  assert.throws(
    () =>
      generateReleaseNotes({
        cwd,
        version: "0.1.0",
        commit,
        repository: "QianFuv/Yousage",
        releases: [],
      }),
    /full Git history/,
  );
});

test("the final body size includes the image line and never truncates commits", (context) => {
  const repository = fixture(context);
  const commit = repository.commit("fix: retain the complete commit list");
  assert.throws(
    () =>
      generateReleaseNotes({
        cwd: repository.cwd,
        version: "0.1.0",
        commit,
        repository: "QianFuv/Yousage",
        releases: [],
        relayImage: "x".repeat(124000),
      }),
    /release body limit/,
  );
});

test("first release keeps unknown subjects literal and rejects missing release metadata", (context) => {
  const repository = fixture(context);
  const commit = repository.commit(
    "fix: escape <script>, [link](url), and ~~text~~",
  );
  const options = {
    cwd: repository.cwd,
    version: "0.1.0",
    commit,
    repository: "QianFuv/Yousage",
    releases: [],
  };
  const notes = generateReleaseNotes(options);
  assert(notes.includes("All commits in this first stable release."));
  assert(notes.includes("&lt;script&gt;"));
  assert(notes.includes("\\[link\\]\\(url\\)"));
  assert(!notes.includes("<script>"));
  assert(notes.includes("commits/v0.1.0"));
  assert.throws(
    () => generateReleaseNotes({ ...options, releases: null }),
    /published releases/,
  );
});
