package com.wails.app;

import android.system.Os;

import java.io.IOException;
import java.lang.reflect.Constructor;
import java.lang.reflect.Modifier;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.FileTime;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.Arrays;
import java.util.Comparator;
import java.util.List;
import java.util.stream.Stream;

/** Portable behavior tests for production diagnostics using synthetic data and temporary files only. */
public final class DiagnosticsRegression {
    private static final String CANARY = "SECRET-CANARY-password-token-chat-path-https://private.invalid";
    private static final String FIXED_TIME = "2026-10-20T00:00:00.000Z";
    private static final long FIXED_NOW = Instant.parse(FIXED_TIME).toEpochMilli();
    private static final String RUN_ID = "00112233445566778899aabbccddeeff";
    private static int passed;

    /** Isolate every test and clean only the temporary root created by this harness. */
    public static void main(String[] arguments) throws Exception {
        run("process context and Activity recreation", DiagnosticsRegression::processContext);
        run("cross-language fixtures and context-independent fallback", DiagnosticsRegression::compatibilityFixtures);
        run("secret canaries, injected fields and malformed IDs", DiagnosticsRegression::redaction);
        run("strict enums, scalars, dates and duplicate keys", DiagnosticsRegression::strictSchema);
        run("corrupt, oversized and partial records", DiagnosticsRegression::corruptRecords);
        run("capacity, native-only rotation and report truncation", DiagnosticsRegression::capacity);
        run("oversized native recovery and exact cleanup scope", DiagnosticsRegression::oversizedRecovery);
        run("retention and exact cleanup scope", DiagnosticsRegression::retention);
        run("disk failures, short writes and partial recovery", DiagnosticsRegression::diskFailure);
        run("fixed frontend intake and bounded rate", DiagnosticsRegression::frontendIntake);
        run("distinct native lifecycle vocabulary", DiagnosticsRegression::lifecycleVocabulary);
        run("no symlink traversal", DiagnosticsRegression::symlinks);
        System.out.println("PASS " + passed + " diagnostics regression groups");
    }

    /** Run one independent case within a disposable temporary directory. */
    private static void run(String name, TestCase test) throws Exception {
        Path root = Files.createTempDirectory("diagnostics-case-");
        try {
            test.run(root);
            passed++;
            System.out.println("PASS " + name);
        } finally {
            Os.maxWrite = Integer.MAX_VALUE;
            Os.bytesBeforeFailure = Integer.MAX_VALUE;
            try (Stream<Path> paths = Files.walk(root)) {
                for (Path path : (Iterable<Path>) paths.sorted(Comparator.reverseOrder())::iterator) Files.delete(path);
            }
        }
    }

    /** Keep one process identity across recreations without touching paired state. */
    private static void processContext(Path root) throws Exception {
        Path paired = root.resolve("paired-user.json");
        Files.writeString(paired, CANARY);
        DiagnosticsStore first = DiagnosticsStore.initialize(root.toFile());
        Path directory = root.resolve("diagnostics");
        Path context = directory.resolve("context.json");
        String initial = Files.readString(context);
        require(initial.matches("\\{\"schemaVersion\":1,\"runId\":\"[0-9a-f]{32}\"}\\n"), "context is not exact compact schema");
        require(Files.size(context) <= 512 && !initial.contains("key"), "context unexpectedly retains a key");
        require(DiagnosticsStore.initialize(root.toFile()) == first, "Activity recreation replaced process store");
        require(Files.readString(context).equals(initial), "Activity recreation changed run identity");
        DiagnosticsStore nextProcess = new DiagnosticsStore(directory.toFile());
        require(nextProcess.initializeProcess(), "new native process could not publish context");
        require(!Files.readString(context).equals(initial), "new process reused run identity");
        require(Files.readString(paired).equals(CANARY), "paired user state changed");
        require(!first.readReport().text.contains(CANARY), "paired user data leaked");
        Constructor<?>[] constructors = DiagnosticsStore.Operation.class.getDeclaredConstructors();
        require(Arrays.stream(constructors).allMatch(value -> Modifier.isPrivate(value.getModifiers())),
                "operation token accepts caller construction");
        require(!nextProcess.record(entry(), first.newOperation()), "foreign store operation token accepted");
    }

    /** Match the documented synthetic Go/native schema without any keys or authenticity claim. */
    private static void compatibilityFixtures(Path root) throws Exception {
        DiagnosticsStore store = reader(root);
        String go = fixture("quota", "g-9007199254740991");
        String nativeLine = fixture("renderer", "n-1").replace("00:00:00.000Z", "00:00:01.000Z");
        Files.writeString(root.resolve("go.jsonl"), go + "\n");
        Files.writeString(root.resolve("native.jsonl"), nativeLine + "\n");
        String expected = DiagnosticsStore.HEADER + nativeLine + "\n" + go + "\n";
        require(store.readReport(FIXED_NOW).text.equals(expected), "cross-language fixtures were not reconstructed exactly");
        Files.writeString(root.resolve("context.json"), "{\"key\":\"" + CANARY + "\"}");
        require(store.readReport(FIXED_NOW).text.equals(expected), "broken context prevented safe fallback reading");
        String noOperation = fixture("report", null);
        Files.writeString(root.resolve("go.1.jsonl"), noOperation + "\n");
        require(store.readReport(FIXED_NOW).text.contains(noOperation), "optional operationId was required");
        String validShape = go.replace(RUN_ID, "ffffffffffffffffffffffffffffffff").replace("g-9007199254740991", "g-99");
        Files.writeString(root.resolve("go.2.jsonl"), validShape + "\n");
        require(store.readReport(FIXED_NOW).text.contains(validShape), "reader incorrectly claims ID authenticity");
    }

    /** Reject arbitrary fields/values and malformed IDs; never echo input bytes in an error. */
    private static void redaction(Path root) throws Exception {
        DiagnosticsStore store = reader(root);
        String valid = fixture("refresh", "g-1");
        List<String> invalid = Arrays.asList(
                valid.replace("\"schemaVersion\":1", "\"schemaVersion\":1,\"message\":\"" + CANARY + "\""),
                valid.replace("\"schemaVersion\":1", "\"schemaVersion\":1,\"stack\":\"" + CANARY + "\""),
                valid.replace("\"schemaVersion\":1", "\"schemaVersion\":1,\"url\":\"" + CANARY + "\""),
                valid.replace("\"schemaVersion\":1", "\"schemaVersion\":1,\"ETag\":\"" + CANARY + "\""),
                valid.replace("\"refresh\"", "\"" + CANARY + "\""),
                valid.replace(RUN_ID, CANARY), valid.replace("g-1", CANARY),
                valid.replace("g-1", "n-1"), valid.replace("g-1", "g-01"),
                valid.replace("g-1", "g-0"), valid.replace("g-1", "g-9007199254740992"),
                valid.replace("g-1", "g-12345678901234567"),
                valid.replace(RUN_ID, RUN_ID.toUpperCase()), valid.replace(RUN_ID, "00112233"));
        Files.writeString(root.resolve("go.jsonl"), String.join("\n", invalid) + "\n" + valid + "\n");
        Files.writeString(root.resolve("native.jsonl"), valid + "\n");
        String report = store.readReport(FIXED_NOW).text;
        require(report.equals(DiagnosticsStore.HEADER + valid + "\n"), "untrusted record survived allowlist");
        require(!report.contains(CANARY), "secret canary leaked");
    }

    /** Reject schema coercion, duplicate/unknown fields and invalid timestamp spellings. */
    private static void strictSchema(Path root) throws Exception {
        DiagnosticsStore store = reader(root);
        String valid = fixture("validation", "g-2");
        List<String> invalid = Arrays.asList(
                valid.replace("\"status\":200", "\"status\":600"),
                valid.replace("\"status\":200", "\"status\":4294967296"),
                valid.replace("\"count\":3", "\"count\":-1"),
                valid.replace("\"count\":3", "\"count\":9007199254740992"),
                valid.replace("\"count\":3", "\"count\":3.0"),
                valid.replace("\"count\":3", "\"count\":3e0"),
                valid.replace("\"count\":3", "\"count\":03"),
                valid.replace("\"count\":3", "\"count\":\"3\""),
                valid.replace("\"count\":3", "\"count\":null"),
                valid.replace("\"count\":3", "\"count\":{}"),
                valid.replace("\"count\":3", "\"count\":[]"),
                valid.replace("\"conditional\":true", "\"conditional\":\"true\""),
                valid.replace("\"info\"", "\"INFO\""),
                valid.replace("\"native\"", "\"unknown\""),
                valid.replace("\"succeeded\"", "\"success\""),
                valid.replace("\"none\"", "\"" + CANARY + "\""),
                valid.replace("\"schemaVersion\":1", "\"schemaVersion\":2"),
                valid.replace("\"schemaVersion\":1", "\"schemaVersion\":1,\"schemaVersion\":1"),
                valid.replace("\"event\":", "\"event\":\"validation\",\"\\u0065vent\":"),
                valid.replace("\"count\":3,", ""),
                valid.replace(FIXED_TIME, "2026-02-30T00:00:00.000Z"),
                valid.replace(FIXED_TIME, "2026-10-20T00:00:00Z"),
                valid.replace(FIXED_TIME, "2026-10-20T00:00:00.000+00:00"),
                valid + "{}", valid.substring(0, valid.length() - 1) + ",}");
        Files.writeString(root.resolve("go.jsonl"), String.join("\n", invalid) + "\n");
        require(store.readReport(FIXED_NOW).text.equals(DiagnosticsStore.HEADER), "invalid schema record was accepted");
        String escaped = valid.replace("\"validation\"", "\"\\u0076alidation\"");
        Files.writeString(root.resolve("go.jsonl"), escaped + "\n");
        require(store.readReport(FIXED_NOW).text.equals(DiagnosticsStore.HEADER + valid + "\n"),
                "valid JSON escape was not reconstructed");
    }

    /** Skip invalid UTF-8, corrupt, oversized and interrupted lines, then recover at the next LF. */
    private static void corruptRecords(Path root) throws Exception {
        DiagnosticsStore store = reader(root);
        String valid = fixture("refresh", "g-1");
        byte[] invalidUtf8 = {(byte) 0xff, '\n'};
        Files.write(root.resolve("go.jsonl"), invalidUtf8);
        Files.writeString(root.resolve("go.jsonl"), "broken\n" + " ".repeat(2048) + valid + "\n" + valid + "\n" + valid,
                java.nio.file.StandardOpenOption.APPEND);
        require(store.readReport(FIXED_NOW).text.equals(DiagnosticsStore.HEADER + valid + "\n"),
                "corrupt/partial record was read or valid recovery was lost");
        String oversized = valid + "\n" + " ".repeat(DiagnosticsStore.FILE_LIMIT) + valid + "\n";
        Files.writeString(root.resolve("go.jsonl"), oversized);
        DiagnosticsStore.Report report = store.readReport(FIXED_NOW);
        require(report.isTruncated && report.text.equals(DiagnosticsStore.HEADER + valid + "\n" + DiagnosticsStore.TRUNCATED),
                "oversized file capacity omission was not marked");
    }

    /** Bound each native file and the full report, retaining only active plus three rotations. */
    private static void capacity(Path root) throws Exception {
        DiagnosticsStore store = initialized(root);
        Path go = root.resolve("go.jsonl");
        String goData = fixture("quota", "g-1") + "\n";
        Files.writeString(go, goData);
        DiagnosticsStore.Operation operation = store.newOperation();
        String nativeLine = fixture("lifecycle", "n-1").replace(FIXED_TIME,
                DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'")
                        .withZone(ZoneOffset.UTC).format(Instant.now())) + "\n";
        String almostFull = nativeLine.repeat(DiagnosticsStore.FILE_LIMIT / nativeLine.length());
        for (int index = 0; index < 5; index++) {
            Files.writeString(root.resolve("native.jsonl"), almostFull);
            require(store.record(new DiagnosticsStore.Entry(DiagnosticsStore.Level.INFO, DiagnosticsStore.Source.NATIVE,
                    DiagnosticsStore.Event.LIFECYCLE, DiagnosticsStore.Outcome.SUCCEEDED, DiagnosticsStore.Failure.NONE,
                    200, DiagnosticsStore.INTEGER_LIMIT, index, DiagnosticsStore.INTEGER_LIMIT, true, true), operation),
                    "bounded write failed");
        }
        long total = 0;
        for (int index = 0; index < 4; index++) {
            Path file = root.resolve(index == 0 ? "native.jsonl" : "native." + index + ".jsonl");
            long size = Files.size(file);
            total += size;
            require(size <= DiagnosticsStore.FILE_LIMIT, "native file exceeded 256 KiB");
            for (String line : Files.readAllLines(file)) {
                require(line.getBytes(StandardCharsets.UTF_8).length + 1 <= DiagnosticsStore.RECORD_LIMIT,
                        "record exceeded 2 KiB");
            }
        }
        require(total <= 1048576 && !Files.exists(root.resolve("native.4.jsonl")), "native writer exceeded four-file budget");
        require(Files.readString(go).equals(goData), "native rotation touched Go writer");
        DiagnosticsStore.Report report = store.readReport();
        require(report.isAvailable && report.isTruncated && report.text.endsWith(DiagnosticsStore.TRUNCATED),
                "report capacity omission missing");
        require(report.text.getBytes(StandardCharsets.UTF_8).length <= DiagnosticsStore.REPORT_LIMIT, "report exceeded 128 KiB");
    }

    /** Recover oversized native files while preserving exact-limit rotations and other owners' files. */
    private static void oversizedRecovery(Path root) throws Exception {
        DiagnosticsStore store = initialized(root);
        String atLimit = " ".repeat(DiagnosticsStore.FILE_LIMIT - 1) + "\n";
        String oversized = atLimit + CANARY;
        List<String> preserved = Arrays.asList("go.jsonl", "go.1.jsonl", "go.2.jsonl", "go.3.jsonl",
                "native.4.jsonl", "native.01.jsonl", "native.jsonl.backup", "paired-user.json", "data.db");
        for (String name : preserved) Files.writeString(root.resolve(name), oversized);
        String context = Files.readString(root.resolve("context.json"));
        for (int index = 1; index < 4; index++) {
            Files.writeString(root.resolve("native." + index + ".jsonl"), atLimit);
        }
        Files.writeString(root.resolve("native.jsonl"), oversized);
        require(store.record(entry()), "oversized active recovery failed");
        require(Files.size(root.resolve("native.jsonl")) <= DiagnosticsStore.FILE_LIMIT,
                "oversized active file survived recovery");
        for (int index = 1; index < 4; index++) {
            require(Files.readString(root.resolve("native." + index + ".jsonl")).equals(atLimit),
                    "exact-limit native rotation was changed");
        }
        for (int index = 0; index < 4; index++) {
            Path file = root.resolve(index == 0 ? "native.jsonl" : "native." + index + ".jsonl");
            Files.writeString(file, oversized);
            Files.setLastModifiedTime(file, FileTime.fromMillis(System.currentTimeMillis()));
        }
        require(store.record(entry()), "oversized native cleanup prevented a valid record");
        long total = 0;
        for (int index = 0; index < 4; index++) {
            Path file = root.resolve(index == 0 ? "native.jsonl" : "native." + index + ".jsonl");
            if (!Files.exists(file)) continue;
            long size = Files.size(file);
            require(size <= DiagnosticsStore.FILE_LIMIT, "fresh oversized native file survived: " + file.getFileName());
            total += size;
        }
        require(total <= 4L * DiagnosticsStore.FILE_LIMIT, "native recovery exceeded its four-file budget");
        require(store.record(entry()) && Files.readAllLines(root.resolve("native.jsonl")).size() == 2,
                "native writer did not resume complete records after oversized cleanup");
        for (String name : preserved) {
            require(Files.readString(root.resolve(name)).equals(oversized), "oversized cleanup changed another owner's file");
        }
        require(Files.readString(root.resolve("context.json")).equals(context), "oversized cleanup changed process context");
    }

    /** Retain only seven days of records and delete only the native writer's exact stale files. */
    private static void retention(Path root) throws Exception {
        DiagnosticsStore store = initialized(root);
        long old = System.currentTimeMillis() - DiagnosticsStore.RETENTION_MS - 60000;
        String stale = fixture("cache", "n-1");
        for (String name : Arrays.asList("native.1.jsonl", "native.2.jsonl", "native.3.jsonl", "native.4.jsonl",
                "native.jsonl.backup", "paired-user.json", "go.3.jsonl")) {
            Path file = root.resolve(name);
            Files.writeString(file, stale + CANARY);
            Files.setLastModifiedTime(file, FileTime.fromMillis(old));
        }
        require(store.record(entry()), "retention write failed");
        require(!Files.exists(root.resolve("native.1.jsonl")) && !Files.exists(root.resolve("native.3.jsonl")), "stale native rotations survived");
        for (String name : Arrays.asList("native.4.jsonl", "native.jsonl.backup", "paired-user.json", "go.3.jsonl")) {
            require(Files.readString(root.resolve(name)).contains(CANARY), "cleanup exceeded exact native ownership");
        }
        String valid = fixture("etag", "g-1");
        String expired = valid.replace(FIXED_TIME, "2026-10-12T23:59:59.999Z");
        String future = valid.replace(FIXED_TIME, "2026-10-20T00:05:00.001Z");
        Files.delete(root.resolve("native.jsonl"));
        Files.writeString(root.resolve("go.jsonl"), expired + "\n" + future + "\n" + valid + "\n");
        Files.writeString(root.resolve("go.2.jsonl"), valid + "\n");
        Files.setLastModifiedTime(root.resolve("go.2.jsonl"), FileTime.fromMillis(FIXED_NOW - DiagnosticsStore.RETENTION_MS - 1));
        require(store.readReport(FIXED_NOW).text.equals(DiagnosticsStore.HEADER + valid + "\n"), "record timestamp retention failed");
    }

    /** Keep ordinary app results independent of diagnostic IO failures and repair partial writes. */
    private static void diskFailure(Path root) throws Exception {
        Path blocked = root.resolve("blocked");
        Files.writeString(blocked, CANARY);
        DiagnosticsStore disabled = new DiagnosticsStore(blocked.toFile());
        require(!disabled.initializeProcess() && !disabled.record(entry()) && !disabled.readReport().isAvailable,
                "filesystem blocker did not fail safely");
        require(Files.readString(blocked).equals(CANARY), "blocker was overwritten");
        Path directory = Files.createDirectory(root.resolve("store"));
        DiagnosticsStore store = initialized(directory);
        Os.maxWrite = 7;
        require(store.record(entry()), "short write loop lost record");
        int completeLength = Files.readString(directory.resolve("native.jsonl")).length();
        Os.bytesBeforeFailure = 37;
        require(!store.record(entry()), "injected full-disk write unexpectedly succeeded");
        Os.bytesBeforeFailure = Integer.MAX_VALUE;
        require(store.record(entry()), "writer did not recover after disk failure");
        String content = Files.readString(directory.resolve("native.jsonl"));
        require(content.substring(completeLength).startsWith("{\"schemaVersion\":1,")
                && content.substring(completeLength).lines().count() == 1, "partial tail was not removed before recovery");
        Files.delete(directory.resolve("native.jsonl"));
        Files.createDirectory(directory.resolve("native.jsonl"));
        require(!store.record(entry()) && !store.readReport().isAvailable, "nonregular active file did not fail safely");
        require(!store.record(new DiagnosticsStore.Entry(DiagnosticsStore.Level.INFO, DiagnosticsStore.Source.NATIVE,
                DiagnosticsStore.Event.REFRESH, DiagnosticsStore.Outcome.FAILED, DiagnosticsStore.Failure.IO,
                600, 0, 0, 0, false, false)), "invalid metric was stored");
    }

    /** Intake consumes only fixed literal categories and drops excess calls without parsing payloads. */
    private static void frontendIntake(Path root) throws Exception {
        DiagnosticsStore store = initialized(root);
        require(!store.frontendFailure(CANARY) && !store.frontendFailure(null)
                && !store.frontendFailure("internal" + CANARY), "free text frontend failure accepted");
        int accepted = 0;
        for (int index = 0; index < 100; index++) if (store.frontendFailure(index % 2 == 0 ? "render" : "internal")) accepted++;
        require(accepted == 10, "frontend intake is not rate limited");
        String report = store.readReport().text;
        require(report.split("\"event\":\"frontend_error\"", -1).length - 1 == 10 && !report.contains(CANARY),
                "frontend report contains unexpected input");
    }

    /** Keep each native lifecycle transition distinguishable using only the shared closed vocabulary. */
    private static void lifecycleVocabulary(Path root) throws Exception {
        DiagnosticsStore store = initialized(root);
        for (DiagnosticsStore.Event event : Arrays.asList(DiagnosticsStore.Event.LIFECYCLE_CREATE,
                DiagnosticsStore.Event.LIFECYCLE_START, DiagnosticsStore.Event.LIFECYCLE_RESUME,
                DiagnosticsStore.Event.LIFECYCLE_PAUSE, DiagnosticsStore.Event.LIFECYCLE_STOP,
                DiagnosticsStore.Event.LIFECYCLE_DESTROY, DiagnosticsStore.Event.LIFECYCLE_LOW_MEMORY)) {
            require(store.record(new DiagnosticsStore.Entry(DiagnosticsStore.Level.INFO, DiagnosticsStore.Source.NATIVE,
                    event, DiagnosticsStore.Outcome.SUCCEEDED, DiagnosticsStore.Failure.NONE)), "lifecycle event was rejected");
            require(store.readReport().text.contains("\"event\":\"" + event.name().toLowerCase(java.util.Locale.ROOT) + "\""),
                    "lifecycle event was not reconstructed");
        }
    }

    /** Verify supported host symlinks are neither read, appended nor cleaned up. */
    private static void symlinks(Path root) throws Exception {
        Path directory = Files.createDirectory(root.resolve("store"));
        DiagnosticsStore store = initialized(directory);
        Path secret = root.resolve("outside.jsonl");
        Files.writeString(secret, CANARY);
        Path link = directory.resolve("native.jsonl");
        Files.delete(link);
        try {
            Files.createSymbolicLink(link, secret);
        } catch (UnsupportedOperationException | IOException failure) {
            System.out.println("SKIP symlink creation unavailable on this host; Android O_NOFOLLOW compiled, device test remains");
            return;
        }
        require(!store.record(entry()) && !store.readReport().isAvailable, "symlink was followed");
        require(Files.readString(secret).equals(CANARY) && Files.isSymbolicLink(link), "symlink target or link modified");
        Files.delete(link);
        Files.createSymbolicLink(link, root.resolve("missing-target"));
        require(!store.record(entry()) && !Files.exists(root.resolve("missing-target")), "dangling symlink was followed");
    }

    /** Construct a reader with no context dependency. */
    private static DiagnosticsStore reader(Path root) {
        return new DiagnosticsStore(root.toFile());
    }

    /** Create one synthetic process store in an already-created temporary directory. */
    private static DiagnosticsStore initialized(Path root) {
        DiagnosticsStore store = reader(root);
        require(store.initializeProcess(), "synthetic process initialization failed");
        return store;
    }

    /** Use only a fixed event when testing native write behavior. */
    private static DiagnosticsStore.Entry entry() {
        return new DiagnosticsStore.Entry(DiagnosticsStore.Level.INFO, DiagnosticsStore.Source.NATIVE,
                DiagnosticsStore.Event.LIFECYCLE, DiagnosticsStore.Outcome.STARTED, DiagnosticsStore.Failure.NONE);
    }

    /** Shared-schema literal fixture: all identifiers and metrics are synthetic and public. */
    private static String fixture(String event, String operationId) {
        return "{\"schemaVersion\":1,\"time\":\"" + FIXED_TIME
                + "\",\"level\":\"info\",\"source\":\"native\",\"event\":\"" + event
                + "\",\"outcome\":\"succeeded\",\"failure\":\"none\",\"status\":200,\"durationMs\":12,\"count\":3,\"bytes\":42,"
                + "\"etagPresent\":true,\"conditional\":true,\"runId\":\"" + RUN_ID + "\""
                + (operationId == null ? "" : ",\"operationId\":\"" + operationId + "\"") + "}";
    }

    /** Assert externally observable storage/report behavior. */
    private static void require(boolean isSatisfied, String message) {
        if (!isSatisfied) throw new AssertionError(message);
    }

    /** One isolated temporary-file regression case. */
    private interface TestCase {
        /** Exercise production diagnostics using only this test's temporary root. */
        void run(Path root) throws Exception;
    }
}
