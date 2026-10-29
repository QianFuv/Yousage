/** Generate distributable notices from locked, installed production dependencies. */
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { readVersion } from "./version.mjs";

const entries = new Map();
const licensePattern = /^(licen[cs]e|copying|notice|copyright)([.\-_]|$)/i;

/** Retain actual license and attribution text, including nested native runtime notices. */
function licenseFiles(directory, relative = "", isRecursive = false) {
  const files = [];
  for (const name of readdirSync(join(directory, relative)).sort()) {
    const file = join(relative, name);
    const details = statSync(join(directory, file));
    if (details.isFile() && licensePattern.test(name)) files.push(file);
    else if (
      details.isDirectory() &&
      isRecursive &&
      !["node_modules", ".git"].includes(name)
    ) {
      files.push(...licenseFiles(directory, file, true));
    }
  }
  return files;
}

/** Collect each unique resolved package once without publishing installation paths. */
function collectNode(dependencies) {
  for (const [name, dependency] of Object.entries(dependencies ?? {})) {
    const metadata = JSON.parse(
      readFileSync(join(dependency.path, "package.json"), "utf8"),
    );
    const identity = `npm:${metadata.name ?? name}@${metadata.version}`;
    if (!entries.has(identity)) {
      let directory = dependency.path;
      let files = licenseFiles(directory);
      if (
        !files.length &&
        metadata.name === "@wailsio/runtime" &&
        metadata.version === "3.0.0"
      ) {
        directory = execFileSync(
          "go",
          ["list", "-m", "-f", "{{.Dir}}", "github.com/wailsapp/wails/v3"],
          { encoding: "utf8" },
        ).trim();
        files = ["LICENSE"];
      }
      if (!files.length) {
        files = readdirSync(directory).filter(
          (file) =>
            /^readme/i.test(file) &&
            /Permission is hereby granted|Redistribution and use in source/i.test(
              readFileSync(join(directory, file), "utf8"),
            ),
        );
      }
      if (!files.length) throw new Error(`Missing license text: ${identity}`);
      entries.set(identity, {
        ecosystem: "npm",
        name: metadata.name ?? name,
        version: metadata.version,
        license: metadata.license ?? "See notice text",
        source: dependency.resolved ?? `https://www.npmjs.com/package/${name}`,
        files: files.map((file) => ({
          file: file.replaceAll("\\", "/"),
          text: readFileSync(join(directory, file), "utf8"),
        })),
      });
    }
    collectNode(dependency.dependencies);
    collectNode(dependency.optionalDependencies);
  }
}

for (const graphPath of process.argv.slice(2)) {
  const graph = JSON.parse(
    readFileSync(resolve(graphPath), "utf8").replace(/^\uFEFF/, ""),
  );
  for (const project of graph) collectNode(project.dependencies);
}
if (!process.argv.slice(2).length)
  throw new Error("Pass both pnpm production dependency graphs.");

const moduleGraph = JSON.parse(
  execFileSync("go", ["mod", "edit", "-json"], { encoding: "utf8" }),
);
for (const requirement of moduleGraph.Require) {
  const metadata = JSON.parse(
    execFileSync("go", ["list", "-m", "-json", requirement.Path], {
      encoding: "utf8",
    }),
  );
  if (!metadata.Dir) throw new Error(`Run go mod download: ${metadata.Path}`);
  const files = licenseFiles(metadata.Dir, "", true);
  if (!files.length) throw new Error(`Missing license text: ${metadata.Path}`);
  entries.set(`go:${metadata.Path}@${metadata.Version}`, {
    ecosystem: "go",
    name: metadata.Path,
    version: metadata.Version,
    license: "See notice text",
    source: `https://pkg.go.dev/${metadata.Path}@${metadata.Version}`,
    files: files.map((file) => ({
      file: file.replaceAll("\\", "/"),
      text: readFileSync(join(metadata.Dir, file), "utf8"),
    })),
  });
}
const goRoot = execFileSync("go", ["env", "GOROOT"], {
  encoding: "utf8",
}).trim();
entries.set("go:standard-library", {
  ecosystem: "go",
  name: "Go standard library",
  version: execFileSync("go", ["env", "GOVERSION"], {
    encoding: "utf8",
  }).trim(),
  license: "BSD-3-Clause",
  source: "https://go.dev/LICENSE",
  files: [
    { file: "LICENSE", text: readFileSync(join(goRoot, "LICENSE"), "utf8") },
  ],
});
const referenceLicense = readFileSync("build/t3code.LICENSE.txt", "utf8");
let notices = `# Third-party notices\n\nYousage 0.1.0 is an independent project. No T3 endorsement is implied.\n\nThis conservative inventory includes the locked production package graphs and direct/indirect Go requirements; not every listed package is necessarily emitted by tree shaking or each platform build. Development-only packages are excluded. System WebView runtimes are supplied by the operating system.\n\n## t3code\n\nSource: https://github.com/pingdotgg/t3code/tree/51f7f77f87318a5329d3bab15d513fda5654200b\n\n${referenceLicense.trim()}\n`;
const manifest = [];
for (const [identity, entry] of [...entries].sort(([first], [second]) =>
  first.localeCompare(second, "en"),
)) {
  notices += `\n## ${identity}\n\nSource: ${entry.source}\n\n`;
  const licenses = [];
  for (const { file, text } of entry.files) {
    const normalized = text.replaceAll("\r\n", "\n").replaceAll("\r", "\n");
    notices += `### ${file}\n\n\`\`\`text\n${normalized.trim()}\n\`\`\`\n\n`;
    licenses.push({
      file,
      sha256: createHash("sha256").update(normalized).digest("hex"),
    });
  }
  manifest.push({
    ecosystem: entry.ecosystem,
    name: entry.name,
    version: entry.version,
    license: entry.license,
    source: entry.source,
    licenses,
  });
}
writeFileSync("THIRD_PARTY_NOTICES.md", notices.trimEnd() + "\n");
writeFileSync(
  "frontend/public/THIRD_PARTY_NOTICES.md",
  notices.trimEnd() + "\n",
);
writeFileSync(
  "build/dependencies.json",
  JSON.stringify(
    {
      version: readVersion(),
      sourceCommit: "51f7f77f87318a5329d3bab15d513fda5654200b",
      dependencies: manifest,
    },
    null,
    2,
  ) + "\n",
);
console.log(
  `Generated ${manifest.length} dependency notices, including nested native licenses.`,
);
