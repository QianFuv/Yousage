package diagnostics

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store serializes bounded Go records in one private directory. Use one Store
// per directory per process; native writes only its separate native files.
type Store struct {
	mutex     sync.Mutex
	directory string
	context   processContext
	sequence  uint64
	now       func() time.Time
}

// Initialize publishes a fresh random process context without persistent keys.
// Native owns this call before Go on Android; desktop may call it directly once
// before Open. It refuses to overwrite symlinks or non-regular context files.
func Initialize(directory string) error {
	if prepareDirectory(directory) != nil {
		return ErrUnavailable
	}
	if info, err := os.Lstat(filepath.Join(directory, contextFilename)); err == nil {
		if !info.Mode().IsRegular() {
			return ErrUnavailable
		}
	} else if !os.IsNotExist(err) {
		return ErrUnavailable
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return ErrUnavailable
	}
	context := processContext{SchemaVersion: 1, RunId: hex.EncodeToString(nonce)}
	data, err := json.Marshal(context)
	if err != nil {
		return ErrUnavailable
	}
	file, err := os.CreateTemp(directory, ".context-*")
	if err != nil {
		return ErrUnavailable
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return ErrUnavailable
	}
	if file.Close() != nil {
		return ErrUnavailable
	}
	if os.Rename(temporary, filepath.Join(directory, contextFilename)) != nil {
		return ErrUnavailable
	}
	return nil
}

// Open reads an already initialized shared process context. It does not change
// run identity or create goroutines. Diagnostic failures must not gate app work.
func Open(directory string) (*Store, error) {
	if prepareDirectory(directory) != nil {
		return nil, ErrUnavailable
	}
	context, err := readContext(directory)
	if err != nil {
		return nil, ErrUnavailable
	}
	store := &Store{directory: directory, context: context, now: time.Now}
	if store.maintain(store.now()) != nil {
		return nil, ErrUnavailable
	}
	return store, nil
}

// NewOperation allocates an opaque correlation handle without a registry.
// An unavailable Store or exhausted sequence returns the empty handle.
func (store *Store) NewOperation() Operation {
	if store == nil || store.now == nil {
		return Operation{}
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.sequence >= uint64(maxInteger) {
		return Operation{}
	}
	store.sequence++
	return Operation{owner: store, sequence: store.sequence}
}

// Record appends validated fixed metadata with a writer-generated UTC timestamp.
// Errors contain no paths or input, and must not alter primary action results.
func (store *Store) Record(entry Entry) error {
	if store == nil || store.now == nil {
		return ErrUnavailable
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	now := store.now().UTC()
	failure := entry.Failure
	if failure == "" {
		failure = FailureNone
	}
	value := record{SchemaVersion: 1, Time: now.Format(timeLayout), Level: entry.Level,
		Source: entry.Source, Event: entry.Event, Outcome: entry.Outcome, Failure: failure,
		Status: entry.Status, DurationMs: entry.DurationMs, Count: entry.Count, Bytes: entry.Bytes,
		EtagPresent: entry.EtagPresent, Conditional: entry.Conditional, RunId: store.context.RunId}
	if !validMetadata(value) {
		return ErrInvalid
	}
	if entry.Operation.owner != nil || entry.Operation.sequence != 0 {
		if entry.Operation.owner != store || entry.Operation.sequence == 0 || entry.Operation.sequence > store.sequence {
			return ErrInvalid
		}
		value.OperationId = operationId("g", entry.Operation.sequence)
	}
	data, err := json.Marshal(value)
	if err != nil || len(data)+1 > MaxRecordBytes {
		return ErrInvalid
	}
	data = append(data, '\n')
	if store.maintain(now) != nil {
		return ErrUnavailable
	}
	path := filepath.Join(store.directory, logFilename("go", 0))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return ErrUnavailable
	}
	size, err := repairTail(file)
	if err != nil {
		file.Close()
		return ErrUnavailable
	}
	if size+int64(len(data)) > MaxFileBytes {
		if file.Close() != nil || store.rotate() != nil {
			return ErrUnavailable
		}
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return ErrUnavailable
		}
	} else if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return ErrUnavailable
	}
	written, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil || written != len(data) {
		return ErrUnavailable
	}
	return nil
}

// Report returns a safe bounded merged report, serialized with this Go writer.
func (store *Store) Report() (string, error) {
	if store == nil || store.now == nil {
		return reportHeader, ErrUnavailable
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return readReport(store.directory, store.now())
}

// prepareDirectory confines work to an absolute private non-symlink directory.
func prepareDirectory(directory string) error {
	if !filepath.IsAbs(directory) {
		return ErrUnavailable
	}
	if os.MkdirAll(directory, 0700) != nil {
		return ErrUnavailable
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnavailable
	}
	return nil
}

// readContext validates fixed app-owned process metadata without exporting it.
func readContext(directory string) (processContext, error) {
	var context processContext
	path := filepath.Join(directory, contextFilename)
	info, err := os.Lstat(path)
	if err != nil {
		return context, err
	}
	if !info.Mode().IsRegular() || info.Size() > 512 {
		return context, ErrUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return context, ErrUnavailable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 513))
	if err != nil || len(data) > 512 || !strictObject(data, &context, []string{"schemaVersion", "runId"}) || context.SchemaVersion != 1 {
		return context, ErrUnavailable
	}
	if !validRunId(context.RunId) {
		return context, ErrUnavailable
	}
	return context, nil
}

// logFilename returns only the eight explicitly owned diagnostic filenames.
func logFilename(writer string, rotation int) string {
	if rotation == 0 {
		return writer + ".jsonl"
	}
	return fmt.Sprintf("%s.%d.jsonl", writer, rotation)
}

// maintain deletes only expired or oversized exact Go files, never native files.
func (store *Store) maintain(now time.Time) error {
	info, err := os.Lstat(store.directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnavailable
	}
	for rotation := 0; rotation <= 3; rotation++ {
		path := filepath.Join(store.directory, logFilename("go", rotation))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return ErrUnavailable
		}
		if info.ModTime().Before(now.Add(-Retention)) || info.Size() > MaxFileBytes {
			if os.Remove(path) != nil {
				return ErrUnavailable
			}
		}
	}
	return nil
}

// rotate moves only bounded exact Go files, with no open handles across renames.
func (store *Store) rotate() error {
	oldest := filepath.Join(store.directory, logFilename("go", 3))
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return ErrUnavailable
	}
	for rotation := 2; rotation >= 0; rotation-- {
		from := filepath.Join(store.directory, logFilename("go", rotation))
		to := filepath.Join(store.directory, logFilename("go", rotation+1))
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return ErrUnavailable
		}
	}
	return nil
}

// repairTail removes an incomplete interrupted write before the next append.
func repairTail(file *os.File) (int64, error) {
	info, err := file.Stat()
	if err != nil || info.Size() > MaxFileBytes {
		return 0, ErrUnavailable
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}
	var last [1]byte
	if _, err := file.ReadAt(last[:], size-1); err != nil {
		return 0, ErrUnavailable
	}
	if last[0] == '\n' {
		return size, nil
	}
	data := make([]byte, size)
	if _, err := file.ReadAt(data, 0); err != nil {
		return 0, ErrUnavailable
	}
	size = int64(bytes.LastIndexByte(data, '\n') + 1)
	if file.Truncate(size) != nil {
		return 0, ErrUnavailable
	}
	return size, nil
}
