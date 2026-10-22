# CI execution and performance

CI runs on pushes to `main`, pull requests, and manual dispatch. All existing
platforms, architectures, build tasks, package formats, and release outputs remain
supported. Performance changes must preserve the checks below.

## Workflow conventions

Use two spaces for each YAML nesting level and preserve the indentation inside
embedded scripts. Step names use `Setup /`, `Verify /`, `Build /`, `Package /`,
`Publish /`, and `Report /` prefixes. These labels describe the existing execution
order; they do not introduce new jobs or reorder checks.

Keep workflow names, job IDs, and job display names stable for required-check
compatibility. In particular, CI still reports `Verify desktop and frontend` and
`Test relay on Linux`. Preserve native PowerShell on Windows and Bash on Linux,
Android signing and dependency preparation, and the relay image smoke test.

Each started job appends a summary with `always()`, showing its status immediately
before the summary step. This includes earlier failures, but excludes later
post-action results; the Actions job conclusion remains authoritative. Skipped
jobs do not run summaries. The release preparation job also retains its existing
version decision summary. See the [GitHub job summary documentation](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands#adding-a-job-summary).

CI and release jobs that install frontend dependencies use the same pinned pnpm
setup and both lockfiles as cache inputs. Every `setup-go` step explicitly hashes
`go.mod` and `go.sum`. Release preparation/publication jobs only need Node.js, so
they do not restore a pnpm store. Relay builds keep their Docker-specific setup.
CI's Windows temporary Go paths remain specific to its verification workload.

## Quality gates and ordering

| Work | Execution policy |
| --- | --- |
| Version and packaging checks | Run version consistency, categorized release-note tests, and Windows portable layout/upgrade tests on every CI run. |
| Frontend | Install both dependency trees with frozen lockfiles; run parity verification/tests, TypeScript, ESLint, Prettier, and frontend tests. Build desktop and relay assets before Windows Go tests. |
| Go unit and integration tests | Run the entire package suite with `-count=1 -p 1` and a ten-minute test timeout. Integration checks remain in the ordinary suite, including process fixtures and the packaged Windows credential helper. |
| Go static checks | Keep `gofmt` and the separate full `go vet ./...` check. The vet subset inside `go test` is not a replacement. |
| Linux relay | Run the existing relay, relay command, and sync tests in parallel with the Windows job. |
| Race detector | There is no dedicated `-race` CI gate at this baseline. Concurrency-behavior tests are not equivalent to the Go race detector. This optimization does not remove an existing race check or claim race-detector coverage. |
| Release | Keep the existing version-gated Windows, signed Android, and relay builds, relay image smoke test, and publish dependencies. Local desktop tasks for other platforms are unchanged. |

Windows Go integration tests compile the application, which embeds
`frontend/dist`. Keep frontend generation ahead of those tests. Splitting jobs
without transferring the correct assets would weaken that coverage. No new path
filters, skipped checks, shortened tests, or `continue-on-error` exemptions are
introduced.

## Caches, concurrency, and artifacts

- Install the pnpm version declared in `frontend/package.json` before
  `setup-node` restores the pnpm store. Hash both lockfiles because frontend
  scripts also use the Vite installation in `tools/parity`. Cache package data,
  not `node_modules`; always perform both frozen installs, including on a hit.
- Keep the existing `setup-go` module/build caching, with both `go.mod` and
  `go.sum` explicitly included in the cache key inputs. On Windows, create Go
  build/module caches and compiler/test temporary directories under
  `RUNNER_TEMP` before invoking `setup-go` so restoration and compilation use
  the same paths. Never clear the developer's caches or user data.
- Retain the CI concurrency group and `cancel-in-progress: true`, which cancel
  superseded runs for the same ref. Retain release serialization without
  cancelling an in-flight publication.
- Release already transfers the built Windows/Android packages and the
  smoke-tested relay image to the publish job. Keep that same-run artifact reuse.
  Do not add privileged CI-to-release artifact consumption merely to save a few
  seconds of frontend bundling; provenance and exact build inputs would need
  separate validation.

## Observed baseline

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
