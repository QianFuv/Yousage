/** Verify the unmodified source slice before treating it as an oracle. @module verifySources */
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const manifest = JSON.parse(await readFile(resolve(ROOT, "sources.json"), "utf8"));
for (const entry of manifest.files) {
  const bytes = await readFile(resolve(ROOT, "upstream", entry.path));
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  const gitBlob = createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
  if (sha256 !== entry.sha256 || gitBlob !== entry.gitBlob) {
    throw new Error(`Reference source changed: ${entry.path}`);
  }
}
process.stdout.write(`Reference integrity passed: ${manifest.files.length} files\n`);
