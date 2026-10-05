package history

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"yousage/internal/domain"
)

const ParserVersion = 1

type FileRead struct {
	Checkpoint domain.Checkpoint
	Records    []domain.Record
	Tail       []domain.Record
	IsResumed  bool
	Malformed  int
}

func guard(file *os.File, offset int64, length int) (string, error) {
	if length == 0 {
		return "", nil
	}
	data := make([]byte, length)
	if _, err := file.ReadAt(data, offset-int64(length)); err != nil {
		return "", err
	}
	hash := fnv.New32a()
	hash.Write(data)
	return fmt.Sprintf("%08x", hash.Sum32()), nil
}

// ReadTranscript streams selected facts, replaying incomplete EOF independently of committed state.
func ReadTranscript(ctx context.Context, path string, provider domain.Provider, previous domain.Checkpoint) (FileRead, error) {
	result := FileRead{Checkpoint: previous, Records: []domain.Record{}, Tail: []domain.Record{}}
	if provider != domain.Codex && provider != domain.Claude {
		return result, errors.New("unsupported transcript parser")
	}
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("history must be a regular file")
	}
	state, start := InitialCodexState(), int64(0)
	if previous.ParserVersion == ParserVersion && info.Size() > previous.FileSize && previous.Offset > 0 && previous.GuardLength > 0 {
		actual, err := guard(file, previous.Offset, previous.GuardLength)
		if err == nil && actual == previous.Guard && (provider != domain.Codex || (json.Unmarshal(previous.Reducer, &state) == nil && state.Speed != "")) {
			start, result.IsResumed = previous.Offset, true
		}
	}
	if !result.IsResumed {
		state = InitialCodexState()
	}
	result.Checkpoint.Offset = start
	reader := bufio.NewReaderSize(io.NewSectionReader(file, start, info.Size()-start), 256*1024)
	readBytes := start
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		stream := &lineStream{reader: reader, ctx: ctx}
		projected, parseError := projectValue(stream, transcriptFields)
		if parseError == nil {
			if _, err := stream.nonspace(); !errors.Is(err, io.EOF) {
				if err != nil {
					parseError = err
				} else {
					parseError = errors.New("extra JSON content")
				}
			}
		}
		if parseError != nil {
			if err := stream.drain(); err != nil {
				return result, err
			}
			if stream.bytes > 1 {
				result.Malformed++
			}
		}
		readBytes += stream.bytes
		lineState := state
		if parseError == nil {
			var record *domain.Record
			if provider == domain.Codex {
				record = ParseCodex(projected, &lineState)
			} else {
				record = ParseClaude(projected)
			}
			if record != nil {
				if stream.hasNewline {
					result.Records = append(result.Records, *record)
				} else {
					result.Tail = append(result.Tail, *record)
				}
			}
		}
		if stream.hasNewline {
			state = lineState
			result.Checkpoint.Offset = readBytes
		} else {
			break
		}
	}
	if readBytes != info.Size() {
		return result, errors.New("history changed while reading")
	}
	current, err := file.Stat()
	if err != nil {
		return result, err
	}
	if current.Size() < info.Size() || (current.Size() == info.Size() && !current.ModTime().Equal(info.ModTime())) {
		return result, errors.New("history rewritten while reading")
	}
	result.Checkpoint.Path, result.Checkpoint.FileSize, result.Checkpoint.ModifiedNs, result.Checkpoint.ParserVersion = path, info.Size(), info.ModTime().UnixNano(), ParserVersion
	result.Checkpoint.GuardLength = int(min(64, result.Checkpoint.Offset))
	result.Checkpoint.Guard, err = guard(file, result.Checkpoint.Offset, result.Checkpoint.GuardLength)
	if err != nil {
		return result, err
	}
	result.Checkpoint.Reducer, err = json.Marshal(state)
	return result, err
}
