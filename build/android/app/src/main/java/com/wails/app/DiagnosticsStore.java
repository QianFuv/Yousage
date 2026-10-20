package com.wails.app;

import android.system.ErrnoException;
import android.system.Os;
import android.system.OsConstants;
import android.system.StructStat;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileDescriptor;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.text.ParsePosition;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Comparator;
import java.util.Date;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.TimeZone;

/** Bounded, allowlisted native metadata and independent Go/native report reading. */
public final class DiagnosticsStore {
    static final int FILE_LIMIT = 262144;
    static final int RECORD_LIMIT = 2048;
    static final int REPORT_LIMIT = 131072;
    static final long INTEGER_LIMIT = 9007199254740991L;
    static final long RETENTION_MS = 7L * 24 * 60 * 60 * 1000;
    static final String HEADER = "Yousage diagnostics v1\n";
    static final String TRUNCATED = "[truncated]\n";
    private static final String[] WRITERS = {"go", "native"};
    private static final Set<String> REQUIRED_KEYS = Collections.unmodifiableSet(new HashSet<>(Arrays.asList(
            "schemaVersion", "time", "level", "source", "event", "outcome", "failure", "status",
            "durationMs", "count", "bytes", "etagPresent", "conditional", "runId")));
    private static DiagnosticsStore processStore;

    /** Closed severity vocabulary; no caller text is stored. */
    public enum Level { INFO, WARN, ERROR }
    /** Closed origin vocabulary shared with Go. */
    public enum Source { NATIVE, MOBILE, FRONTEND, DESKTOP, SYNC, CLAUDE }
    /** Closed event vocabulary shared with Go. */
    public enum Event {
        STARTUP, LIFECYCLE, LIFECYCLE_CREATE, LIFECYCLE_START, LIFECYCLE_RESUME, LIFECYCLE_PAUSE,
        LIFECYCLE_STOP, LIFECYCLE_DESTROY, LIFECYCLE_LOW_MEMORY, RENDERER, FRONTEND_ERROR, REFRESH, VALIDATION, STAGE, COMMIT,
        ROLLBACK, CACHE, ETAG, PAIRING, QUOTA, REPORT
    }
    /** Closed result vocabulary shared with Go. */
    public enum Outcome { STARTED, SUCCEEDED, FAILED, REJECTED, SKIPPED, RETAINED, CANCELLED }
    /** Fixed failure categories; exceptions, messages and thrown objects are never accepted. */
    public enum Failure {
        NONE, UNAVAILABLE, IO, NETWORK, TIMEOUT, HTTP, AUTHENTICATION, CREDENTIALS_MISSING,
        CREDENTIALS_EXPIRED, UNSUPPORTED, PROTOCOL, INVALID_DATA, STALE, RENDER, INTERNAL
    }

    /** Immutable allowlisted record input, deliberately excluding time and correlation strings. */
    public static final class Entry {
        final Level level;
        final Source source;
        final Event event;
        final Outcome outcome;
        final Failure failure;
        final int status;
        final long durationMs;
        final long count;
        final long bytes;
        final boolean etagPresent;
        final boolean conditional;

        /** Construct an event with zero metrics and false flags. */
        public Entry(Level level, Source source, Event event, Outcome outcome, Failure failure) {
            this(level, source, event, outcome, failure, 0, 0, 0, 0, false, false);
        }

        /** Construct an event using only bounded numeric metrics, fixed enums and booleans. */
        public Entry(Level level, Source source, Event event, Outcome outcome, Failure failure,
                int status, long durationMs, long count, long bytes, boolean etagPresent, boolean conditional) {
            this.level = level;
            this.source = source;
            this.event = event;
            this.outcome = outcome;
            this.failure = failure;
            this.status = status;
            this.durationMs = durationMs;
            this.count = count;
            this.bytes = bytes;
            this.etagPresent = etagPresent;
            this.conditional = conditional;
        }
    }

    /** Internally generated correlation token, usable only by its originating store. */
    public static final class Operation {
        private final DiagnosticsStore owner;
        private final long sequence;

        /** Bind an internal sequence to the store that generated it. */
        private Operation(DiagnosticsStore owner, long sequence) {
            this.owner = owner;
            this.sequence = sequence;
        }
    }

    /** A safe report plus a fixed availability indicator, with no raw filesystem errors. */
    public static final class Report {
        public final String text;
        public final boolean isAvailable;
        public final boolean isTruncated;

        /** Capture bounded filtered output and fixed status flags. */
        private Report(String text, boolean isAvailable, boolean isTruncated) {
            this.text = text;
            this.isAvailable = isAvailable;
            this.isTruncated = isTruncated;
        }
    }

    private final File directory;
    private String runId;
    private long sequence;
    private long frontendWindowNanos;
    private int frontendCount;

    /** Bind a diagnostics directory without changing files or requiring Go. */
    DiagnosticsStore(File directory) {
        this.directory = directory;
    }

    /**
     * Initialize once per native process before loading Go; Activity recreations reuse the same run.
     * The argument is Android Context.getFilesDir(), not a caller-supplied diagnostics path.
     * Failure disables writing without throwing or modifying paired application data.
     */
    public static synchronized DiagnosticsStore initialize(File filesDirectory) {
        if (processStore != null) return processStore;
        File diagnosticsDirectory = null;
        try {
            if (filesDirectory != null) diagnosticsDirectory = new File(filesDirectory.getCanonicalFile(), "diagnostics");
        } catch (IOException | RuntimeException ignored) {
        }
        processStore = new DiagnosticsStore(diagnosticsDirectory);
        processStore.initializeProcess();
        return processStore;
    }

    /** Read the process store without filesystem work or starting another native run. */
    static synchronized DiagnosticsStore current() {
        return processStore;
    }

    /** Publish only schemaVersion/runId atomically; never read or remove paired-user state. */
    synchronized boolean initializeProcess() {
        if (runId != null) return true;
        File temporary = null;
        try {
            requireDirectory(true);
            byte[] random = new byte[16];
            new SecureRandom().nextBytes(random);
            StringBuilder identity = new StringBuilder(32);
            for (byte value : random) identity.append(String.format(Locale.ROOT, "%02x", value & 255));
            String nextRunId = identity.toString();
            File context = file("context.json");
            regularOrMissing(context);
            temporary = File.createTempFile("context-", ".tmp", directory);
            writeFile(temporary, ("{\"schemaVersion\":1,\"runId\":\"" + nextRunId + "\"}\n")
                    .getBytes(StandardCharsets.UTF_8));
            Os.rename(temporary.getPath(), context.getPath());
            temporary = null;
            runId = nextRunId;
            record(new Entry(Level.INFO, Source.NATIVE, Event.STARTUP, Outcome.STARTED, Failure.NONE));
            return true;
        } catch (IOException | ErrnoException | RuntimeException ignored) {
            return false;
        } finally {
            try {
                if (temporary != null) temporary.delete();
            } catch (RuntimeException ignored) {
            }
        }
    }

    /** Allocate an opaque native sequence without accepting caller-generated strings. */
    public synchronized Operation newOperation() {
        if (runId == null || sequence >= INTEGER_LIMIT) return null;
        return new Operation(this, ++sequence);
    }

    /** Append fixed metadata; a diagnostic failure never changes the caller's result. */
    public boolean record(Entry entry) {
        return record(entry, null);
    }

    /** Append a record, optionally correlated with a token generated by this store. */
    public synchronized boolean record(Entry entry, Operation operation) {
        if (runId == null || !isValidEntry(entry) || (operation != null && operation.owner != this)) return false;
        try {
            requireDirectory(false);
            long now = System.currentTimeMillis();
            expireNativeFiles(now);
            Map<String, Object> fields = fields(entry, now);
            fields.put("runId", runId);
            if (operation != null) fields.put("operationId", "n-" + operation.sequence);
            byte[] line = (serialize(fields) + "\n").getBytes(StandardCharsets.UTF_8);
            if (line.length > RECORD_LIMIT) return false;
            File active = file("native.jsonl");
            int completeLength = completeLength(active);
            if (completeLength + line.length > FILE_LIMIT) {
                rotate();
                completeLength = 0;
            }
            append(active, completeLength, line);
            return true;
        } catch (IOException | ErrnoException | RuntimeException ignored) {
            return false;
        }
    }

    /**
     * Accept only the two fixed frontend failure categories; at most ten records per minute.
     * Pass a literal category, never a thrown object, message, stack or serialized error.
     */
    public synchronized boolean frontendFailure(String category) {
        if (category == null || category.length() > 8) return false;
        Failure failure;
        if ("render".equals(category)) failure = Failure.RENDER;
        else if ("internal".equals(category)) failure = Failure.INTERNAL;
        else return false;
        long now = System.nanoTime();
        if (frontendWindowNanos == 0 || now - frontendWindowNanos >= 60000000000L) {
            frontendWindowNanos = now;
            frontendCount = 0;
        }
        if (frontendCount >= 10) return false;
        frontendCount++;
        return record(new Entry(Level.ERROR, Source.FRONTEND, Event.FRONTEND_ERROR, Outcome.FAILED, failure));
    }

    /** Read and reconstruct a safe report independently of context.json, JNI, React and WebView. */
    public synchronized Report readReport() {
        return readReport(System.currentTimeMillis());
    }

    /** Read with a fixed clock for deterministic retention and cross-language fixture verification. */
    synchronized Report readReport(long now) {
        List<Map<String, Object>> records = new ArrayList<>();
        boolean isAvailable = true;
        boolean isTruncated = false;
        try {
            if (!requireDirectory(false)) return new Report(HEADER, true, false);
            for (String writer : WRITERS) {
                for (int index = 0; index < 4; index++) {
                    try {
                        File candidate = file(filename(writer, index));
                        StructStat metadata = regularOrMissing(candidate);
                        if (metadata == null) continue;
                        if (candidate.lastModified() < now - RETENTION_MS) continue;
                        if (metadata.st_size > FILE_LIMIT) isTruncated = true;
                        byte[] content = readFile(candidate, FILE_LIMIT);
                        if (content == null) continue;
                        int start = 0;
                        for (int offset = 0; offset < content.length; offset++) {
                            if (content[offset] != '\n') continue;
                            if (offset - start + 1 <= RECORD_LIMIT) {
                                Map<String, Object> record = validatedRecord(content, start, offset, writer, now);
                                if (record != null) records.add(record);
                            }
                            start = offset + 1;
                        }
                    } catch (IOException | ErrnoException | RuntimeException ignored) {
                        isAvailable = false;
                    }
                }
            }
        } catch (IOException | ErrnoException | RuntimeException ignored) {
            isAvailable = false;
        }
        records.sort(Comparator.comparing((Map<String, Object> record) -> (String) record.get("time")).reversed());
        StringBuilder report = new StringBuilder(HEADER);
        int size = HEADER.length();
        for (Map<String, Object> record : records) {
            String line = serialize(record) + "\n";
            int length = line.getBytes(StandardCharsets.UTF_8).length;
            if (size + length + TRUNCATED.length() > REPORT_LIMIT) {
                isTruncated = true;
                break;
            }
            report.append(line);
            size += length;
        }
        if (isTruncated) report.append(TRUNCATED);
        return new Report(report.toString(), isAvailable, isTruncated);
    }

    /** Check enums and integer bounds before any input can reach disk. */
    private static boolean isValidEntry(Entry entry) {
        return entry != null && entry.level != null && entry.source != null && entry.event != null
                && entry.outcome != null && entry.failure != null && entry.status >= 0 && entry.status <= 599
                && isBounded(entry.durationMs) && isBounded(entry.count) && isBounded(entry.bytes);
    }

    /** Check the shared JSON-safe integer range. */
    private static boolean isBounded(long value) {
        return value >= 0 && value <= INTEGER_LIMIT;
    }

    /** Build fixed schema fields without exposing a free-text input API. */
    private static Map<String, Object> fields(Entry entry, long now) {
        Map<String, Object> result = new HashMap<>();
        result.put("schemaVersion", 1L);
        result.put("time", dateFormat().format(new Date(now)));
        result.put("level", entry.level.name().toLowerCase(Locale.ROOT));
        result.put("source", entry.source.name().toLowerCase(Locale.ROOT));
        result.put("event", entry.event.name().toLowerCase(Locale.ROOT));
        result.put("outcome", entry.outcome.name().toLowerCase(Locale.ROOT));
        result.put("failure", entry.failure.name().toLowerCase(Locale.ROOT));
        result.put("status", (long) entry.status);
        result.put("durationMs", entry.durationMs);
        result.put("count", entry.count);
        result.put("bytes", entry.bytes);
        result.put("etagPresent", entry.etagPresent);
        result.put("conditional", entry.conditional);
        return result;
    }

    /** Strictly parse, validate and reconstruct one complete bounded input record. */
    private static Map<String, Object> validatedRecord(byte[] content, int start, int end, String writer, long now) {
        try {
            String json = StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                    .onUnmappableCharacter(CodingErrorAction.REPORT)
                    .decode(ByteBuffer.wrap(content, start, end - start)).toString();
            Map<String, Object> input = new ScalarJson(json).parse();
            Set<String> keys = new HashSet<>(input.keySet());
            keys.remove("operationId");
            if (!keys.equals(REQUIRED_KEYS) || !Long.valueOf(1).equals(input.get("schemaVersion"))) return null;
            String time = string(input.get("time"));
            if (!time.matches("[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\\.[0-9]{3}Z")) return null;
            ParsePosition position = new ParsePosition(0);
            Date date = dateFormat().parse(time, position);
            if (date == null || position.getIndex() != time.length() || date.getTime() < now - RETENTION_MS
                    || date.getTime() > now + 300000L) return null;
            if (!string(input.get("runId")).matches("[0-9a-f]{32}")) return null;
            if (input.containsKey("operationId")) {
                String operation = string(input.get("operationId"));
                String prefix = "go".equals(writer) ? "g-" : "n-";
                if (!operation.matches(prefix + "[1-9][0-9]{0,15}")
                        || Long.parseLong(operation.substring(2)) > INTEGER_LIMIT) return null;
            }
            Entry entry = new Entry(enumValue(Level.class, input.get("level")), enumValue(Source.class, input.get("source")),
                    enumValue(Event.class, input.get("event")), enumValue(Outcome.class, input.get("outcome")),
                    enumValue(Failure.class, input.get("failure")), (int) number(input.get("status")),
                    number(input.get("durationMs")), number(input.get("count")), number(input.get("bytes")),
                    bool(input.get("etagPresent")), bool(input.get("conditional")));
            if (!isValidEntry(entry) || number(input.get("status")) > 599) return null;
            Map<String, Object> clean = fields(entry, date.getTime());
            clean.put("runId", input.get("runId"));
            if (input.containsKey("operationId")) clean.put("operationId", input.get("operationId"));
            return clean;
        } catch (IllegalArgumentException | CharacterCodingException ignored) {
            return null;
        }
    }

    /** Require a JSON string rather than coercing arbitrary values. */
    private static String string(Object value) {
        if (!(value instanceof String)) throw new IllegalArgumentException();
        return (String) value;
    }

    /** Require a bounded JSON integer rather than coercing strings or floating point values. */
    private static long number(Object value) {
        if (!(value instanceof Long) || !isBounded((Long) value)) throw new IllegalArgumentException();
        return (Long) value;
    }

    /** Require a JSON boolean rather than truthy caller data. */
    private static boolean bool(Object value) {
        if (!(value instanceof Boolean)) throw new IllegalArgumentException();
        return (Boolean) value;
    }

    /** Match only exact lowercase enum spellings from the shared contract. */
    private static <Value extends Enum<Value>> Value enumValue(Class<Value> type, Object input) {
        String text = string(input);
        for (Value value : type.getEnumConstants()) {
            if (value.name().toLowerCase(Locale.ROOT).equals(text)) return value;
        }
        throw new IllegalArgumentException();
    }

    /** Create a strict UTC formatter available on Android API 21. */
    private static SimpleDateFormat dateFormat() {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.ROOT);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        format.setLenient(false);
        return format;
    }

    /** Serialize validated ASCII scalars in a fixed key order, never copying input JSON. */
    private static String serialize(Map<String, Object> fields) {
        String[] keys = {"schemaVersion", "time", "level", "source", "event", "outcome", "failure", "status",
                "durationMs", "count", "bytes", "etagPresent", "conditional", "runId", "operationId"};
        StringBuilder json = new StringBuilder("{");
        for (String key : keys) {
            if (!fields.containsKey(key)) continue;
            if (json.length() > 1) json.append(',');
            json.append('"').append(key).append("\":");
            Object value = fields.get(key);
            if (value instanceof String) json.append('"').append(value).append('"');
            else json.append(value);
        }
        return json.append('}').toString();
    }

    /** Refuse symlink directories and paths outside the selected app-private directory. */
    private boolean requireDirectory(boolean shouldCreate) throws IOException, ErrnoException {
        if (directory == null) throw new IOException();
        if (!directory.getCanonicalFile().equals(directory.getAbsoluteFile())) throw new IOException();
        StructStat metadata = metadata(directory);
        if (metadata == null && shouldCreate) {
            if (!directory.mkdir()) throw new IOException();
            metadata = metadata(directory);
        }
        if (metadata == null) return false;
        if (!OsConstants.S_ISDIR(metadata.st_mode)) throw new IOException();
        return true;
    }

    /** Construct only internal fixed filenames; no caller path reaches this method. */
    private File file(String name) {
        return new File(directory, name);
    }

    /** Return lstat metadata without following even dangling symlinks. */
    private static StructStat metadata(File file) throws ErrnoException {
        try {
            return Os.lstat(file.getPath());
        } catch (ErrnoException error) {
            if (error.errno == OsConstants.ENOENT) return null;
            throw error;
        }
    }

    /** Permit missing files or regular files only, rejecting links, directories and devices. */
    private static StructStat regularOrMissing(File file) throws ErrnoException, IOException {
        StructStat metadata = metadata(file);
        if (metadata != null && !OsConstants.S_ISREG(metadata.st_mode)) throw new IOException();
        return metadata;
    }

    /** Open a regular file without following symlinks on all supported Android versions. */
    private static FileDescriptor open(File file, int flags) throws ErrnoException, IOException {
        regularOrMissing(file);
        FileDescriptor descriptor = Os.open(file.getPath(), flags | OsConstants.O_NOFOLLOW, 0600);
        boolean shouldClose = true;
        try {
            if (!OsConstants.S_ISREG(Os.fstat(descriptor).st_mode)) throw new IOException();
            shouldClose = false;
            return descriptor;
        } finally {
            if (shouldClose) Os.close(descriptor);
        }
    }

    /** Read at most the stated bound, treating a missing file as normal. */
    private static byte[] readFile(File file, int limit) throws IOException, ErrnoException {
        if (regularOrMissing(file) == null) return null;
        FileDescriptor descriptor;
        try {
            descriptor = open(file, OsConstants.O_RDONLY);
        } catch (ErrnoException error) {
            if (error.errno == OsConstants.ENOENT) return null;
            throw error;
        }
        try {
            ByteArrayOutputStream content = new ByteArrayOutputStream();
            byte[] buffer = new byte[Math.min(limit, 4096)];
            int remaining = limit;
            while (remaining > 0) {
                int count = Os.read(descriptor, buffer, 0, Math.min(remaining, buffer.length));
                if (count <= 0) break;
                content.write(buffer, 0, count);
                remaining -= count;
            }
            return content.toByteArray();
        } finally {
            Os.close(descriptor);
        }
    }

    /** Write a small atomic-publication candidate and sync it before rename. */
    private static void writeFile(File file, byte[] content) throws IOException, ErrnoException {
        FileDescriptor descriptor = open(file, OsConstants.O_WRONLY | OsConstants.O_CREAT);
        try {
            Os.ftruncate(descriptor, 0);
            writeAll(descriptor, content);
            Os.fsync(descriptor);
        } finally {
            Os.close(descriptor);
        }
    }

    /** Remove partial tails before appending one complete record. */
    private static void append(File file, int length, byte[] content) throws IOException, ErrnoException {
        FileDescriptor descriptor = open(file, OsConstants.O_RDWR | OsConstants.O_CREAT);
        try {
            Os.ftruncate(descriptor, length);
            Os.lseek(descriptor, length, OsConstants.SEEK_SET);
            writeAll(descriptor, content);
        } finally {
            Os.close(descriptor);
        }
    }

    /** Handle short writes without emitting any filesystem error text. */
    private static void writeAll(FileDescriptor descriptor, byte[] content) throws IOException, ErrnoException {
        int offset = 0;
        while (offset < content.length) {
            int count = Os.write(descriptor, content, offset, content.length - offset);
            if (count <= 0) throw new IOException();
            offset += count;
        }
    }

    /** Inspect only the tail during normal appends; bound recovery scans after interrupted writes. */
    private static int completeLength(File file) throws IOException, ErrnoException {
        StructStat metadata = regularOrMissing(file);
        if (metadata == null || metadata.st_size > FILE_LIMIT) return 0;
        FileDescriptor descriptor = open(file, OsConstants.O_RDONLY);
        try {
            int end = (int) Os.fstat(descriptor).st_size;
            if (end > FILE_LIMIT) return 0;
            byte[] buffer = new byte[4096];
            while (end > 0) {
                int start = Math.max(0, end - buffer.length);
                Os.lseek(descriptor, start, OsConstants.SEEK_SET);
                int count = Os.read(descriptor, buffer, 0, end - start);
                for (int offset = count - 1; offset >= 0; offset--) {
                    if (buffer[offset] == '\n') return start + offset + 1;
                }
                end = start;
            }
            return 0;
        } finally {
            Os.close(descriptor);
        }
    }

    /** Remove expired or oversized files only within the native writer's four exact regular filenames. */
    private void expireNativeFiles(long now) throws IOException, ErrnoException {
        for (int index = 0; index < 4; index++) {
            File candidate = file(filename("native", index));
            StructStat metadata = regularOrMissing(candidate);
            if (metadata != null && (metadata.st_size > FILE_LIMIT || candidate.lastModified() < now - RETENTION_MS)
                    && !candidate.delete()) {
                throw new IOException();
            }
        }
    }

    /** Rotate only native files, preserving the independently owned Go writer and paired data. */
    private void rotate() throws IOException, ErrnoException {
        for (int index = 3; index >= 0; index--) regularOrMissing(file(filename("native", index)));
        File oldest = file(filename("native", 3));
        if (oldest.exists() && !oldest.delete()) throw new IOException();
        for (int index = 2; index >= 0; index--) {
            File source = file(filename("native", index));
            if (source.exists()) Os.rename(source.getPath(), file(filename("native", index + 1)).getPath());
        }
    }

    /** Name only one of the contract's four files for a known internal writer. */
    private static String filename(String writer, int index) {
        return writer + (index == 0 ? "" : "." + index) + ".jsonl";
    }

    /** Small strict flat-object JSON parser: duplicates, containers, null and floats are rejected. */
    private static final class ScalarJson {
        private final String input;
        private int offset;

        /** Parse only a previously bounded record string. */
        ScalarJson(String input) {
            this.input = input;
        }

        /** Read one flat scalar object and require complete consumption. */
        Map<String, Object> parse() {
            Map<String, Object> result = new HashMap<>();
            expect('{');
            if (!take('}')) {
                do {
                    String key = readString();
                    if (result.containsKey(key)) throw new IllegalArgumentException();
                    expect(':');
                    skipWhitespace();
                    Object value;
                    if (peek() == '"') value = readString();
                    else if (input.startsWith("true", offset)) { offset += 4; value = true; }
                    else if (input.startsWith("false", offset)) { offset += 5; value = false; }
                    else {
                        int start = offset;
                        if (peek() == '-') offset++;
                        if (peek() < '0' || peek() > '9') throw new IllegalArgumentException();
                        if (peek() == '0') offset++;
                        else while (peek() >= '0' && peek() <= '9') offset++;
                        value = Long.parseLong(input.substring(start, offset));
                    }
                    result.put(key, value);
                } while (take(','));
                expect('}');
            }
            skipWhitespace();
            if (offset != input.length()) throw new IllegalArgumentException();
            return result;
        }

        /** Decode JSON escapes so escaped unknown fields and enum values receive identical validation. */
        private String readString() {
            expect('"');
            StringBuilder value = new StringBuilder();
            while (offset < input.length()) {
                char character = input.charAt(offset++);
                if (character == '"') return value.toString();
                if (character < 32) throw new IllegalArgumentException();
                if (character == '\\') {
                    if (offset >= input.length()) throw new IllegalArgumentException();
                    char escape = input.charAt(offset++);
                    int index = "\"\\/bfnrt".indexOf(escape);
                    if (index >= 0) character = "\"\\/\b\f\n\r\t".charAt(index);
                    else if (escape == 'u' && offset + 4 <= input.length()) {
                        String hex = input.substring(offset, offset + 4);
                        if (!hex.matches("[0-9a-fA-F]{4}")) throw new IllegalArgumentException();
                        character = (char) Integer.parseInt(hex, 16);
                        offset += 4;
                    } else throw new IllegalArgumentException();
                }
                value.append(character);
            }
            throw new IllegalArgumentException();
        }

        /** Read the next character without accepting missing input as a token. */
        private char peek() {
            return offset < input.length() ? input.charAt(offset) : '\0';
        }

        /** Consume only JSON's four allowed whitespace characters. */
        private void skipWhitespace() {
            while (offset < input.length() && " \t\r\n".indexOf(input.charAt(offset)) >= 0) offset++;
        }

        /** Optionally consume a structural character after whitespace. */
        private boolean take(char expected) {
            skipWhitespace();
            if (peek() != expected) return false;
            offset++;
            return true;
        }

        /** Require a structural character, failing closed on malformed input. */
        private void expect(char expected) {
            if (!take(expected)) throw new IllegalArgumentException();
        }
    }
}
