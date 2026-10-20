# Local diagnostics

Yousage keeps a small local diagnostic history to investigate startup, rendering,
refresh, cache, sync and Claude quota failures. It does not upload logs or add a
telemetry service.

## Android: view and share

Open **Settings → Diagnostics**, or use the native **Diagnostics** button below
the WebView. The native recovery screen also offers **View diagnostics**. The
native entries work independently of React and the Go service, including when
the page fails to load or its renderer stops.

The dialog shows a read-only report. **Refresh** reads it again. **Share** reads
and filters the files again, then opens Android's share chooser with plain text.
Choose a recipient or destination yourself. Opening the chooser does not mean a
recipient has saved the report. No storage or other sensitive permission is
required.

## Desktop

The existing diagnostics panel provides an explicit view/export action. Reports
include only whitelisted application metadata and recent redacted events. The
save dialog controls the export destination. Existing lifecycle evidence used by
native smoke tests remains separate; old application files are not swept into an
export or removed by the new retention policy.

## What is recorded

Records contain a writer-generated UTC timestamp, severity, a fixed subsystem and
event, a fixed outcome/failure category, and bounded status codes, durations,
counts or sizes. A random process run ID connects native, Go and frontend events.
Internally generated operation sequence IDs connect steps of an operation.
These identifiers provide correlation, not cryptographic authenticity.

Events cover Android lifecycle and renderer recovery; uncaught frontend/render
failures; refresh, snapshot validation, staging, commit and rollback; cache and
conditional-request state; sync; and Claude quota/credential failure categories.
An ETag is represented only by presence/conditional booleans. An HTTP rejection
from the quota endpoint does not establish that Claude Code itself is logged out.

Records do not contain exception messages or stacks, URLs, paths, ETags or their
hashes, request headers, credentials, tokens, pairing keys, account identities,
usage snapshots, chat content or raw CLI output. Frontend intake accepts only
fixed categories and is rate limited. Arbitrary console output is not captured.
Android bridge/framework payload logging is disabled for debug and release APKs.
The Android build also uses a temporary Go overlay to replace three reviewed
Wails beta.30 JNI log calls with fixed text: unknown system events, asset startup
timeouts, and asset failures. The build checks the exact framework version,
source fingerprint and original calls, and fails explicitly if they change.
Go disallows overlays inside its module cache, so the build copies the already
cached framework into a disposable directory and uses a temporary modfile to
resolve only that same locked version to the copy. A byte-for-byte check of the
temporary module files preserves the original `go.mod` bytes, appending only the
exact version-qualified replacement. The copied `go.sum` and framework `go.mod`
must match their originals byte for byte. This avoids querying unrelated module
graph metadata on a fresh runner. Compilation uses read-only module mode and
cannot acquire dependencies or toolchains. All full source checksum entries in
`go.sum` are checked for archive-only or incomplete cache entries before
compilation, preventing extraction into the original source cache. Missing
required cached inputs fail the offline build explicitly.
The Android Release dependency-install phase first runs ordinary `go mod download`
for the modules already locked by the project, without `all` or version queries,
and fails if `go.mod` or `go.sum` changes. This normal dependency preparation is
separate from the immutable, offline overlay and compiler phases. Temporary files
are removed on success or failure. The offline phases leave the repository's module
files and module cache unchanged; dependency versions and request handling remain
unchanged throughout.

## Storage and failure behavior

The app-private `diagnostics` directory contains independent Go and native JSONL
writers. Each writer has one active file and three rotations, each at most
256 KiB including newlines: at most 2 MiB of log files in total. Records are at
most 2 KiB. Only the exact diagnostic filenames are subject to seven-day
retention. Reports contain the newest retained events first, are limited to
128 KiB including their header and truncation indicator, and are rebuilt from
validated fields whenever viewed or exported. Partial, malformed and unknown
records are skipped.

`context.json` contains only the schema version and random run ID. It is neither
a credential nor part of an export. There is no persistent integrity key or
cross-language authentication protocol. A report can be read without this file
or a running Go service.

Diagnostics failures do not reset pairing, delete user data, advance a snapshot
ETag or turn a failed refresh into a successful one. A failed log write must not
change the result of the action being diagnosed.

## Verification limits

Synthetic Go and portable JVM tests exercise filtering, capacity, retention,
partial writes, concurrency and failure paths. Browser fault injection exercises
render recovery separately. These checks do not establish reproduction of a
particular user's incident, physical-device behavior, or successful delivery to a
share recipient. Record device/emulator validation explicitly when it is run.
