/** Verify source binding, exact artifact membership and corruption rejection. */
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { verifyManifest, writeManifest } from "./ci-artifacts.mjs";

const SOURCE = "a".repeat(40);

test("artifact consumers reject changed source, platform, extra files and corrupted bytes", (context) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "yousage-artifact-"));
  context.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  fs.writeFileSync(
    path.join(directory, "package.zip"),
    "synthetic package bytes",
  );
  writeManifest(directory, SOURCE, "windows/amd64");
  assert.equal(
    verifyManifest(directory, SOURCE, "windows/amd64", ["package.zip"]).files
      .length,
    1,
  );
  assert.throws(
    () => verifyManifest(directory, "b".repeat(40), "windows/amd64"),
    /source/,
  );
  assert.throws(
    () => verifyManifest(directory, SOURCE, "android/arm64"),
    /platform/,
  );
  assert.throws(
    () => verifyManifest(directory, SOURCE, "windows/amd64", ["different.zip"]),
    /Unexpected artifact/,
  );
  fs.writeFileSync(
    path.join(directory, "private.sqlite"),
    "disposable sentinel",
  );
  assert.throws(
    () => verifyManifest(directory, SOURCE, "windows/amd64"),
    /membership/,
  );
  fs.unlinkSync(path.join(directory, "private.sqlite"));
  fs.writeFileSync(path.join(directory, "package.zip"), "corrupted package");
  assert.throws(
    () => verifyManifest(directory, SOURCE, "windows/amd64"),
    /checksums/,
  );
});

test("empty artifacts cannot produce acceptance evidence", (context) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "yousage-empty-"));
  context.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  assert.throws(() => writeManifest(directory, SOURCE, "frontend"), /empty/);
});
