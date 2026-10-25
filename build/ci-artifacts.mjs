/** Bind same-run build artifacts to their source, platform and exact file hashes. */
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

/** Return regular files beneath a directory, rejecting symbolic links. */
function inventory(directory, prefix = "") {
  return fs
    .readdirSync(directory)
    .sort()
    .flatMap((name) => {
      const relative = prefix ? `${prefix}/${name}` : name;
      if (relative === "ci-manifest.json") return [];
      const filename = path.join(directory, name);
      const entry = fs.lstatSync(filename);
      assert(
        !entry.isSymbolicLink(),
        "Artifact must not contain symbolic links",
      );
      if (entry.isDirectory()) return inventory(filename, relative);
      assert(entry.isFile(), "Artifact must contain regular files");
      return [
        {
          path: relative,
          bytes: entry.size,
          sha256: createHash("sha256")
            .update(fs.readFileSync(filename))
            .digest("hex"),
        },
      ];
    });
}

/** Record only successful build bytes and their immutable product source. */
export function writeManifest(directory, source, platform) {
  assert.match(source, /^[a-f0-9]{40}$/);
  const files = inventory(directory);
  assert(
    files.length > 0 && files.every((file) => file.bytes > 0),
    "Artifact is empty",
  );
  const manifest = { schema: 1, source, platform, files };
  fs.writeFileSync(
    path.join(directory, "ci-manifest.json"),
    JSON.stringify(manifest, null, 2) + "\n",
  );
  return manifest;
}

/** Verify exact source, platform, membership and hashes before consuming an artifact. */
export function verifyManifest(directory, source, platform, expectedFiles) {
  const manifest = JSON.parse(
    fs.readFileSync(path.join(directory, "ci-manifest.json"), "utf8"),
  );
  assert.equal(manifest.schema, 1);
  assert.equal(
    manifest.source,
    source,
    "Artifact source differs from the caller commit",
  );
  assert.equal(manifest.platform, platform, "Artifact platform differs");
  const files = inventory(directory);
  assert(
    files.length > 0 && files.every((file) => file.bytes > 0),
    "Artifact is empty",
  );
  assert.deepEqual(
    files,
    manifest.files,
    "Artifact membership or checksums differ",
  );
  if (expectedFiles)
    assert.deepEqual(
      files.map((file) => file.path).sort(),
      [...expectedFiles].sort(),
      "Unexpected artifact files",
    );
  return manifest;
}

/** Stage or restore tested frontend bytes without copying unrelated workspace data. */
function frontend(mode, directory, source) {
  const roots = ["frontend/dist", "internal/relay/web/dist"];
  if (mode === "stage-frontend") {
    fs.mkdirSync(directory, { recursive: true });
    for (const root of roots)
      fs.cpSync(root, path.join(directory, root), { recursive: true });
    writeManifest(directory, source, "frontend");
  } else {
    const manifest = verifyManifest(directory, source, "frontend");
    assert(
      manifest.files.every((file) =>
        roots.some((root) => file.path.startsWith(`${root}/`)),
      ),
      "Unexpected frontend path",
    );
    for (const root of roots) {
      assert(
        manifest.files.some((file) => file.path.startsWith(`${root}/`)),
        `Missing ${root}`,
      );
      fs.cpSync(path.join(directory, root), root, { recursive: true });
    }
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const [mode, directory, platform] = process.argv.slice(2);
  if (["stage-frontend", "restore-frontend"].includes(mode))
    frontend(mode, directory, process.env.GITHUB_SHA);
  else {
    assert.equal(mode, "manifest");
    writeManifest(directory, process.env.GITHUB_SHA, platform);
  }
}
