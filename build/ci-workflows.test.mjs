/** Protect the release/quality dependency graph and existing platform check contracts. */
import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

const WORKFLOWS = Object.fromEntries(
  ["ci", "frontend", "backend", "release"].map((name) => [
    name,
    fs.readFileSync(
      new URL(`../.github/workflows/${name}.yml`, import.meta.url),
      "utf8",
    ),
  ]),
);

test("only main version increases or explicit retries reach a successful quality-gated release", () => {
  assert.match(WORKFLOWS.ci, /branches: \[main\]/);
  assert.match(WORKFLOWS.ci, /needs: \[release-rules, frontend, backend\]/);
  assert.match(
    WORKFLOWS.ci,
    /if: needs.release-rules.outputs.release == 'true'/,
  );
  assert.match(WORKFLOWS.ci, /uses: \.\/.github\/workflows\/release.yml/);
  assert.doesNotMatch(WORKFLOWS.release, /workflow_run:|workflow_dispatch:/);
  assert.match(WORKFLOWS.release, /workflow_call:/);
  assert.match(
    WORKFLOWS.release,
    /needs: \[context, desktop, android, relay\]/,
  );
  assert.match(WORKFLOWS.release, /cancel-in-progress: false/);
  assert.doesNotMatch(WORKFLOWS.ci.split("jobs:")[0], /cancel-in-progress/);
  assert.match(WORKFLOWS.ci, /format\('release-\{0\}', github.run_id\)/);
});

test("frontend gates and same-run source verification precede full desktop Go tests", () => {
  for (const command of [
    "pnpm --dir tools/parity test",
    ...["typecheck", "lint", "format:check", "test", "build", "build:web"].map(
      (command) => `pnpm --dir frontend ${command}`,
    ),
  ])
    assert(WORKFLOWS.frontend.includes(command), command);
  for (const command of [
    "go test -count=1 -p 1 -timeout=10m ./...",
    "go vet ./...",
    "gofmt -l .",
    "./build/test-package-windows.ps1",
    "go test -count=1 -timeout=5m ./internal/relay ./cmd/yousage-relay ./internal/sync",
  ])
    assert(WORKFLOWS.backend.includes(command), command);
  assert.match(
    WORKFLOWS.backend,
    /artifact-ids: \$\{\{ inputs.frontend_artifact_id \}\}/,
  );
  assert(
    WORKFLOWS.backend.indexOf("restore-frontend") <
      WORKFLOWS.backend.indexOf("go test -count=1 -p 1"),
  );
  assert.doesNotMatch(
    Object.values(WORKFLOWS).join("\n"),
    /continue-on-error: true/,
  );
});

test("official Android signing and Relay smoke precede exact named artifact publication", () => {
  for (const secret of [
    "ANDROID_KEYSTORE_BASE64",
    "ANDROID_KEYSTORE_PASSWORD",
    "ANDROID_KEY_ALIAS",
    "ANDROID_KEY_PASSWORD",
  ]) {
    assert(WORKFLOWS.ci.includes(`${secret}: \${{ secrets.${secret} }}`));
    assert(WORKFLOWS.release.includes(`${secret}:\n        required: true`));
  }
  assert.match(WORKFLOWS.release, /-mode release -arch arm64/);
  assert.match(WORKFLOWS.release, /apksigner.*verify --print-certs/);
  assert.match(WORKFLOWS.release, /CN=Android Debug/);
  assert.match(WORKFLOWS.release, /Relay image smoke checks passed/);
  assert.match(WORKFLOWS.release, /name: windows-release/);
  assert.match(WORKFLOWS.release, /name: android-release/);
  assert.match(WORKFLOWS.release, /name: relay-release/);
  assert.doesNotMatch(WORKFLOWS.release, /--clobber/);
  assert(
    WORKFLOWS.release.indexOf("release-contract.mjs assets") <
      WORKFLOWS.release.indexOf("release-contract.mjs prepare"),
  );
  assert(
    WORKFLOWS.release.indexOf("release-contract.mjs prepare") <
      WORKFLOWS.release.indexOf("docker push"),
  );
  assert.match(WORKFLOWS.release, /release-contract.mjs verify/);
});
