/**
 * Fetch the declared public source slice and verify each pinned Git blob.
 * Node entry points use .mjs so synchronization can run before dependencies exist.
 * @module syncUpstream
 */
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const manifest = JSON.parse(await readFile(resolve(ROOT, "sources.json"), "utf8"));
const pendingEntries = [...manifest.files];

/** Download one declared file, validating content before publishing it locally. */
async function downloadSource(entry) {
  const url = `https://raw.githubusercontent.com/pingdotgg/t3code/${manifest.commit}/${entry.path}`;
  const response = await fetch(url, { signal: AbortSignal.timeout(30_000) });
  if (!response.ok) throw new Error(`${entry.path}: HTTP ${response.status}`);
  const bytes = Buffer.from(await response.arrayBuffer());
  const blobHash = createHash("sha1")
    .update(`blob ${bytes.length}\0`)
    .update(bytes)
    .digest("hex");
  if (blobHash !== entry.gitBlob) throw new Error(`${entry.path}: pinned blob mismatch`);
  const destination = resolve(ROOT, "upstream", entry.path);
  await mkdir(dirname(destination), { recursive: true });
  await writeFile(destination, bytes);
  entry.sha256 = createHash("sha256").update(bytes).digest("hex");
  entry.bytes = bytes.length;
}

/** Process bounded independent public downloads. */
async function downloadWorker() {
  while (pendingEntries.length > 0) {
    const entry = pendingEntries.shift();
    await downloadSource(entry);
  }
}

await Promise.all(Array.from({ length: 6 }, downloadWorker));
await writeFile(resolve(ROOT, "sources.json"), `${JSON.stringify(manifest, null, 2)}\n`);
process.stdout.write(`Verified ${manifest.files.length} files at ${manifest.commit}\n`);
