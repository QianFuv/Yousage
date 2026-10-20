// Package diagnostics writes bounded, allowlisted local metadata without a Wails
// dependency. Failures return fixed sentinel errors; callers must keep diagnostic
// failures separate from primary application results. One Store owns the Go
// writer for a directory. Java owns a separate native writer in that directory.
//
// Interop contract, schema version 1:
//
// App-private context.json is a single strict JSON object with exactly
// schemaVersion (integer 1) and runId (32 lowercase hex characters generated from
// 16 SecureRandom/crypto-rand bytes). Native startup initializes it before Go;
// desktop startup can call Initialize. Generate a fresh runId for each process.
// Publish atomically, reject unknown/duplicate fields, and bound the file to 512
// bytes. Context is app-owned process metadata, never user/bridge input. Never
// export the context file itself. No keys, credentials or application data belong
// in it. Open only reads context; it does not change the run identity.
//
// operationId is <writer>-<sequence>, where writer is g
// for Go or n for native, sequence is canonical decimal in [1,9007199254740991],
// with no leading zeroes. Generate IDs internally; never accept arbitrary IDs
// from JS/callers. IDs read from disk receive format validation, not an
// authenticity guarantee. Logs are untrusted metadata, not security evidence.
//
// JSONL records use these exact case-sensitive keys: schemaVersion (1), time
// (writer UTC RFC3339 with exactly millisecond precision, e.g.
// 2026-10-20T00:00:00.000Z), level, source, event, outcome, failure, status,
// durationMs, count, bytes, etagPresent, conditional, runId, and optional
// operationId. All keys other than operationId are required. status is an integer
// [0,599]; durationMs/count/bytes are integers [0,9007199254740991]. Booleans must
// be JSON booleans. Reject unknown/duplicate keys, null, unknown enum values,
// invalid timestamps, and malformed IDs. Reconstruct JSON from validated
// fields; never copy input lines directly into reports.
//
// Closed vocabulary (exact strings):
//
//   - level: info, warn, error
//   - source: native, mobile, frontend, desktop, sync, claude
//   - event: startup, lifecycle, lifecycle_create, lifecycle_start, lifecycle_resume,
//     lifecycle_pause, lifecycle_stop, lifecycle_destroy, lifecycle_low_memory,
//     renderer, frontend_error, refresh, validation,
//     stage, commit, rollback, cache, etag, pairing, quota, report
//   - outcome: started, succeeded, failed, rejected, skipped, retained, cancelled
//   - failure: none, unavailable, io, network, timeout, http, authentication,
//     credentials_missing, credentials_expired, unsupported, protocol,
//     invalid_data, stale, render, internal
//
// Writers use go.jsonl/go.1.jsonl/go.2.jsonl/go.3.jsonl and
// native.jsonl/native.1.jsonl/native.2.jsonl/native.3.jsonl respectively. Each
// file is at most 262144 bytes including newlines; each record including its LF
// is at most 2048 bytes. Rotate before appending when capacity would be exceeded.
// Drop an unterminated trailing record before the next append. Never follow
// symlinks or clean up similarly named files. Delete only these exact filenames
// when their modification time is older than seven days; also omit records older
// than seven days or more than five minutes in the future when reporting.
//
// A merged report reads only those eight files, ignoring partial/corrupt/oversize
// lines, validates ID formats, and emits the newest valid records
// first (time descending, stable file/line order for ties). Read at most 262144
// bytes from each file. Native readers must accept g operation IDs only in Go
// files and n IDs only in native files. Reports are UTF-8 JSONL at most 131072
// bytes INCLUDING the fixed first line "Yousage diagnostics v1\n" and reserved
// final line "[truncated]\n" when valid records or oversized file bytes are
// omitted for capacity. Missing files are normal. Other filesystem failures
// return a fixed unavailable error (a partial safe report may accompany it).
// Native read/view/export must implement the same allowlist without Go/React.
package diagnostics
