package diagnostics

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReadReport reads Go and native files independently of any running Store or UI.
// It returns only reconstructed metadata, plus a fixed error on disk failures.
func ReadReport(directory string) (string, error) {
	if !filepath.IsAbs(directory) {
		return reportHeader, ErrUnavailable
	}
	return readReport(directory, time.Now())
}

// readReport bounds disk reads and output, keeping the newest valid records.
func readReport(directory string, now time.Time) (string, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return reportHeader, ErrUnavailable
	}
	var records []record
	var reportErr error
	isTruncated := false
	for _, writer := range []string{"go", "native"} {
		for rotation := 0; rotation <= 3; rotation++ {
			path := filepath.Join(directory, logFilename(writer, rotation))
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() {
				reportErr = ErrUnavailable
				continue
			}
			if info.ModTime().Before(now.Add(-Retention)) {
				continue
			}
			file, err := os.Open(path)
			if err != nil {
				reportErr = ErrUnavailable
				continue
			}
			if info.Size() > MaxFileBytes {
				isTruncated = true
			}
			prefix := "g"
			if writer == "native" {
				prefix = "n"
			}
			values, err := readRecords(io.LimitReader(file, MaxFileBytes), prefix, now)
			records = append(records, values...)
			if file.Close() != nil || err != nil {
				reportErr = ErrUnavailable
			}
		}
	}
	sort.SliceStable(records, func(first, second int) bool { return records[first].Time > records[second].Time })
	var report strings.Builder
	report.Grow(MaxReportBytes)
	report.WriteString(reportHeader)
	for _, value := range records {
		data, err := json.Marshal(value)
		if err != nil {
			continue
		}
		if report.Len()+len(data)+1+len(truncationLine) > MaxReportBytes {
			isTruncated = true
			break
		}
		report.Write(data)
		report.WriteByte('\n')
	}
	if isTruncated {
		report.WriteString(truncationLine)
	}
	return report.String(), reportErr
}

// readRecords skips oversized lines without losing subsequent complete records.
func readRecords(input io.Reader, writer string, now time.Time) ([]record, error) {
	reader := bufio.NewReaderSize(input, MaxRecordBytes)
	var records []record
	isOversized := false
	for {
		line, err := reader.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			isOversized = true
			continue
		}
		if err == io.EOF {
			return records, nil
		}
		if err != nil {
			return records, err
		}
		if isOversized {
			isOversized = false
			continue
		}
		if len(line) > MaxRecordBytes {
			continue
		}
		if value, ok := parseRecord(bytes.TrimSuffix(line, []byte{'\n'}), writer, now); ok {
			records = append(records, value)
		}
	}
}

// parseRecord rejects unknown fields, enums and malformed IDs before reconstruction.
func parseRecord(data []byte, writer string, now time.Time) (record, bool) {
	var value record
	if !strictObject(data, &value, []string{"schemaVersion", "time", "level", "source", "event", "outcome", "failure", "status", "durationMs", "count", "bytes", "etagPresent", "conditional", "runId"}, "operationId") || value.SchemaVersion != 1 || !validMetadata(value) {
		return record{}, false
	}
	observed, err := time.Parse(timeLayout, value.Time)
	if err != nil || observed.Format(timeLayout) != value.Time || observed.Before(now.Add(-Retention)) || observed.After(now.Add(5*time.Minute)) {
		return record{}, false
	}
	if !validRunId(value.RunId) || value.OperationId != "" && !validOperationId(writer, value.OperationId) {
		return record{}, false
	}
	return value, true
}
