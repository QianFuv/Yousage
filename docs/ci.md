# CI execution and releases

The workflow organization follows LitRadar's main revision
`49609249721c610294bd378cebb9775982d514ae`, with Yousage's own platform and
toolchain contracts. The public presentation follows LitRadar v0.2.4.

## Stages and reusable workflows

`ci.yml` is the only push, pull-request and manual entry point:

1. **detect-changes** (`Release rules`) checks version consistency, release
   contracts and the version change across the entire pushed commit range.
2. **quality** invokes `frontend.yml` (`Reusable Frontend Checks`), then
   `backend.yml` (`Reusable Go Checks`). The frontend exports source-bound build
   bytes by immutable same-run artifact ID. Backend verifies the source, complete
   file inventory and SHA-256 hashes before restoring embedded frontend assets.
3. **build-acceptance** invokes `release.yml` (`Release`) only for an increased
   main version or a matching explicit main-only retry, after both quality
   workflows succeed. It builds Windows amd64, signed Android arm64, and the
   Relay image, including the existing HTTP smoke test.
4. **publish** consumes only the three explicitly named platform artifacts,
   validates their source/platform/membership/hashes, prepares a draft with the
   exact public attachments, publishes the tested version image and release,
   updates latest only for the newest stable version, and confirms publication.

The intentional organization change produces nested check names under
`Frontend`, `Backend` and `Version release`. Repository required-check/security
settings are not changed. At rollout, main was unprotected with no rulesets.

Each started job uses `always()` to report its stage, platform, source SHA and
individual check outcomes. Summaries describe status before the summary step;
the final Actions job conclusion remains authoritative. Skipped jobs have no
summary. Artifact manifests bind successful build bytes to their source SHA,
platform and checksums; they contain no environment dumps or runtime user data.

## Preserved quality and platform contracts

| Work | Required checks |
| --- | --- |
| Release rules | Version consistency, categorized notes, detection/retry/identity rules, artifact corruption and exact-public-membership regressions, workflow dependency contracts. |
| Frontend | Frozen installs for both lockfiles; parity source/tests, TypeScript, ESLint, Prettier, frontend tests, desktop and relay frontend builds. |
| Windows Backend | Version consistency, release-note tests, portable layout/private-data exclusion/upgrade test, verified frontend restoration, full gofmt, `go test -count=1 -p 1 -timeout=10m ./...`, separate `go vet ./...`. |
| Linux Backend | Existing relay, relay-command and sync tests, uncached with a five-minute timeout. |
| Windows release | Existing production executable and portable ZIP build. Internal package layout and upgrade behavior are unchanged. |
| Android release | Existing arm64 release build, four explicit signing secrets, mandatory signature verification and debug-key rejection. Dependency preparation and immutable Go module checks remain. |
| Relay release | Existing Docker build, health/auth/share HTTP smoke with disposable data, and transport of the tested image to publication. No running service is deployed. |

Windows Go integration tests compile the main package, which embeds
`frontend/dist`; verified same-run frontend restoration must happen first. No
existing test is replaced by a narrower test, path filter, failure exemption or
cache hit. There was no dedicated race-detector gate to migrate.

## Release presentation and retries

The title is `Yousage v<version>`. Public assets are exactly
`yousage_<version>_windows_amd64.zip`, `yousage_<version>_android_arm64.apk` and
`SHA256SUMS`. The checksum manifest covers exactly the ZIP and APK, excluding
itself, the Relay transport tar and CI evidence. The portable ZIP still contains
`data/package/SHA256SUMS.txt`. Release notes use the existing categorized complete
commit list and changelog links, with the Yousage Relay image reference appended.

Normal unchanged-version pushes run quality only. Manual **CI** dispatch without
a version does the same; an explicit version retries only the checked-in version
on `main`. GitHub metadata errors fail closed. A conflicting source tag fails;
published releases are verified without rewriting them. A draft retry skips
byte-identical assets and refuses different bytes, without `--clobber`.

All platform acceptance completes before external publication writes. A draft
and/or version image may remain if a later external write fails; the writes are
not atomic. After any draft attachment has been uploaded, use **Re-run failed
jobs** on the original workflow run: its successful build jobs retain their exact
immutable artifact IDs. A new dispatch rejects a partial draft from another run
before rebuilding; timestamp-bearing archives cannot be recreated with guaranteed
identical bytes. Accepted platform artifacts are retained for fourteen days;
expired evidence requires an explicit recovery decision, never automatic replacement.

Registry lookup distinguishes a confirmed missing tag from lookup/authentication
errors. An existing image with the same config/content digest is retained; a
different image fails before draft upload. Release serialization does not
cancel in-flight publication, queues up to 100 pending candidates, and protects latest pointers
from older retries. No signing key, credential or repository security permission
is created or changed by the pipeline.

## Caches, concurrency and artifact boundaries

Frontend/dependency-building jobs retain the pinned pnpm and Node setup, both
lockfiles and frozen installs. Go jobs retain the pinned setup action with both
`go.mod` and `go.sum` as cache inputs. Windows Backend preserves runner-temporary
Go cache/module/test paths. Backend Node-only setup no longer restores pnpm data.
The Docker image keeps its existing independent dependency/build environment.

Only ordinary quality jobs share cancellation groups for a ref. Release
candidates use run-specific quality groups; the reusable Release workflow uses
one non-cancelling publication group. There is no workflow-wide cancellation
that could interrupt publication after a later main push.

The `frontend-quality-<attempt>` artifact carries only desktop/relay frontend build trees
and their manifest. `windows-release-<attempt>`, `android-release-<attempt>` and `relay-release-<attempt>` each
carry only the intended package/image and a source/platform/hash manifest.
Publication downloads their immutable same-run IDs individually, so quality and diagnostic files
cannot leak into public assets. `release-publication-<attempt>` retains notes/checksums as
CI evidence for fourteen days. Public uploads use an explicit three-file list.

## Historical performance baseline

The following successful GitHub runs were read on 2026-10-23 (Asia/Singapore),
before this optimization. Durations come from Actions timestamps, rounded to
whole seconds. Workflow elapsed time includes orchestration; job/step durations
are measured separately and should not be summed across parallel jobs.

| CI run | Workflow | Windows job | Go tests | Go setup/cache | Dependency install | Linux job |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| [39639545249](https://github.com/QianFuv/Yousage/actions/runs/39639545249) | 189 s | 184 s | 74 s | 33 s | 18 s | 20 s |
| [39582080460](https://github.com/QianFuv/Yousage/actions/runs/39582080460) | 394 s | 389 s | 243 s | 47 s | 20 s | 17 s |
| [39581332356](https://github.com/QianFuv/Yousage/actions/runs/39581332356) | 227 s | 222 s | 80 s | 54 s | 19 s | 20 s |

The median workflow duration was 227 seconds. The latest Windows Go cache was a
hit, approximately 181 MiB, with about 21 seconds between the cache-hit and
restoration-success messages. Cache restoration already costs time; adding a
second overlapping Go cache or disabling caching without measurement is not an
established improvement.

The latest successful [release run](https://github.com/QianFuv/Yousage/actions/runs/39640032725)
took 213 seconds: Android 167 s, Windows 157 s, relay 59 s, and publish 26 s.
Those build jobs overlap. Workflow normalization preserves release triggers,
build commands, signing, smoke tests, artifact names, and publication behavior.

Expected benefits are fewer repeated pnpm downloads and less Windows cache/temp
I/O on the default system paths. Cache misses still install and compile normally;
the first run with new cache inputs will repopulate caches. No cloud speedup has
been measured for the revised workflow. Validate both cold and warm runs after a
separately authorized push before claiming a time saving or changing concurrency
further.

A local attempt to establish a serial/parallel comparison used Windows amd64,
Go 1.26.10, and `CGO_ENABLED=0`; the cloud baseline used Go 1.26.2. The initial
unchanged `-p 1` run encountered Claude and Codex process-fixture initialization
timeouts, including `TestControlFailuresPreserveSafeEvidence/unknown-method` and
quota reset tests. Consequently, package parallelism remains `-p 1`, and there
is no valid paired local speedup measurement.
Do not attribute that baseline failure to the workflow changes or weaken the
fixture's assertions to obtain a faster pass.

## Primary references

- [GoReleaser's actual workflow](https://github.com/goreleaser/goreleaser/blob/4762c79733dde55c7f63b3cdb4ef08d249df562d/.github/workflows/build.yml): uncached test execution, platform-specific test flags, Windows cache/temp placement, and separate cache inputs for different test workloads.
- [Caddy's actual workflow](https://github.com/caddyserver/caddy/blob/f355b8687d5582ea9fcc99f71598109d1e5b6ced/.github/workflows/ci.yml): build/start-stop smoke checks alongside package tests with the race detector. Its flags and permission settings are not copied into Yousage.
- [Hugo's actual workflow](https://github.com/gohugoio/hugo/blob/ed22b85c192dd750cf0bdfb2d2005d31ab3bc0a7/.github/workflows/test.yml): runner-temp placement and project-specific Windows cache/race tradeoffs. Yousage retains its Go cache.
- [Pinned setup-node pnpm example](https://github.com/actions/setup-node/blob/a0853c24544627f65ddf259abe73b1d18a591444/docs/advanced-usage.md#caching-packages-data): install pnpm before cache restoration; use lockfile inputs for dependency data.
- [Pinned setup-go caching documentation](https://github.com/actions/setup-go/blob/924ae3a1cded613372ab5595356fb5720e22ba16/README.md#caching-dependency-files-and-build-outputs): default module/build caching and explicit dependency paths.
- [GitHub concurrency documentation](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency): cancellation and queued-run semantics.
- [GitHub workflow_run documentation](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_run) and [artifact documentation](https://docs.github.com/en/actions/tutorials/store-and-share-data): privilege boundaries and artifact sharing.
