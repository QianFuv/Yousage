/** Append the common LitRadar stage/source/check summary without exposing environment data. */
import assert from "node:assert/strict";
import fs from "node:fs";

const stage = process.env.CI_STAGE;
assert(
  ["detect-changes", "quality", "build-acceptance", "publish"].includes(stage),
);
const checks = JSON.parse(process.env.CI_CHECKS || "{}");
const lines = [
  `## ${stage}: ${process.env.CI_PLATFORM || "independent"}`,
  "",
  `Source: \`${process.env.GITHUB_SHA}\``,
  `Result before summary: ${process.env.CI_STATUS}`,
  "",
  "| Check | Result |",
  "| --- | --- |",
  ...Object.entries(checks).map(
    ([name, check]) => `| ${name} | ${check.outcome ?? "skipped"} |`,
  ),
  "",
];
fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, lines.join("\n"));
