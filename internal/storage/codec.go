package storage

import (
	"encoding/binary"
	"errors"
	"math"
	"yousage/internal/domain"
)

var errUnknownString = errors.New("unknown storage string identity")

// payloadWriter encodes signed facts and exact IEEE floating point values.
type payloadWriter struct{ data []byte }

// integer appends a signed varint.
func (writer *payloadWriter) integer(value int64) {
	writer.data = binary.AppendVarint(writer.data, value)
}

// number preserves floating point bits, including signed zero.
func (writer *payloadWriter) number(value float64) {
	writer.data = binary.LittleEndian.AppendUint64(writer.data, math.Float64bits(value))
}

// payloadReader rejects truncated, unknown and trailing payloads.
type payloadReader struct {
	data    []byte
	err     error
	version byte
}

// integer reads a signed varint.
func (reader *payloadReader) integer() int64 {
	value, length := binary.Varint(reader.data)
	if length <= 0 {
		reader.err = errors.New("invalid storage payload integer")
		return 0
	}
	reader.data = reader.data[length:]
	return value
}

// number reads an exact floating point value.
func (reader *payloadReader) number() float64 {
	if len(reader.data) < 8 {
		reader.err = errors.New("invalid storage payload number")
		return 0
	}
	value := math.Float64frombits(binary.LittleEndian.Uint64(reader.data))
	reader.data = reader.data[8:]
	return value
}

// newPayloadReader checks the payload format version.
func newPayloadReader(data []byte) payloadReader {
	reader := payloadReader{data: data}
	if len(data) == 0 || (data[0] != 1 && data[0] != 2) {
		reader.err = errors.New("unsupported storage payload version")
		return reader
	}
	reader.version = data[0]
	reader.data = data[1:]
	return reader
}

// finish verifies complete decoding.
func (reader *payloadReader) finish() error {
	if reader.err != nil {
		return reader.err
	}
	if len(reader.data) != 0 {
		return errors.New("trailing storage payload")
	}
	return nil
}

// encodeRecord writes payload v2 with the original seven interned strings and closed error/TTL codes.
func encodeRecord(record domain.Record, intern func(string) (int64, error)) ([]byte, error) {
	writer := payloadWriter{data: []byte{2}}
	for _, value := range []string{string(record.Provider), record.Model, record.RateModel, record.SessionId, record.Speed, record.DedupeKey, record.SourcePath} {
		identity, err := intern(value)
		if err != nil {
			return nil, err
		}
		writer.integer(identity)
	}
	for _, value := range []int64{record.Totals.UncachedInputTokens, record.Totals.CachedInputTokens, record.Totals.CacheCreationTokens, record.Totals.OutputTokens, record.Totals.ReasoningTokens} {
		writer.integer(value)
	}
	if record.ReportedCostUsd == nil {
		writer.integer(0)
	} else {
		writer.integer(1)
		writer.number(*record.ReportedCostUsd)
	}
	// Closed enum codes keep arbitrary error strings out of the dictionary.
	kindCode := int64(0)
	if record.ErrorKind != "" {
		for index, kind := range domain.ErrorKinds {
			if kind == record.ErrorKind {
				kindCode = int64(index + 1)
			}
		}
		if kindCode == 0 {
			return nil, errors.New("invalid error kind")
		}
	}
	writer.integer(kindCode)
	writer.integer(record.CacheTtlMs)
	return writer.data, nil
}

// decodeRecord restores v1/v2 facts with relational timestamp, bucket and observation fields.
func decodeRecord(data []byte, timestamp int64, bucket string, isQuotaOnly bool, quota []byte, lookup func(int64) (string, bool)) (domain.Record, error) {
	reader := newPayloadReader(data)
	values := make([]string, 7)
	for index := range values {
		identity := reader.integer()
		value, exists := lookup(identity)
		if !exists {
			reader.err = errUnknownString
		}
		values[index] = value
	}
	record := domain.Record{Provider: domain.Provider(values[0]), TimestampMs: timestamp, Model: values[1], RateModel: values[2], SessionId: values[3], Speed: values[4], DedupeKey: values[5], SourcePath: values[6], QuotaBucket: bucket, IsQuotaOnly: isQuotaOnly}
	record.Totals = domain.Tokens{UncachedInputTokens: reader.integer(), CachedInputTokens: reader.integer(), CacheCreationTokens: reader.integer(), OutputTokens: reader.integer(), ReasoningTokens: reader.integer()}
	switch reader.integer() {
	case 0:
	case 1:
		value := reader.number()
		record.ReportedCostUsd = &value
	default:
		reader.err = errors.New("invalid storage cost marker")
	}
	if reader.version == 2 {
		kindCode := reader.integer()
		if kindCode < 0 || kindCode > int64(len(domain.ErrorKinds)) {
			reader.err = errors.New("invalid error kind code")
		} else if kindCode > 0 {
			record.ErrorKind = domain.ErrorKinds[kindCode-1]
		}
		record.CacheTtlMs = reader.integer()
	}
	if err := reader.finish(); err != nil {
		return record, err
	}
	if quota != nil {
		observation, err := decodeQuota(quota, lookup)
		record.Quota = &observation
		return record, err
	}
	return record, nil
}

// encodeQuota retains every window and distinguishes nil from empty windows.
func encodeQuota(observation domain.QuotaObservation, intern func(string) (int64, error)) ([]byte, error) {
	writer := payloadWriter{data: []byte{1}}
	plan, err := intern(observation.PlanType)
	if err != nil {
		return nil, err
	}
	writer.integer(plan)
	count := int64(len(observation.Windows))
	if observation.Windows == nil {
		count = -1
	}
	writer.integer(count)
	for _, window := range observation.Windows {
		identity, err := intern(window.Id)
		if err != nil {
			return nil, err
		}
		writer.integer(identity)
		writer.number(window.UsedPercent)
		writer.integer(int64(window.WindowMinutes))
		writer.integer(window.ResetsAt)
	}
	return writer.data, nil
}

// decodeQuota restores all window facts in their original order.
func decodeQuota(data []byte, lookup func(int64) (string, bool)) (domain.QuotaObservation, error) {
	reader := newPayloadReader(data)
	if reader.version != 1 {
		return domain.QuotaObservation{}, errors.New("unsupported quota payload version")
	}
	plan, exists := lookup(reader.integer())
	if !exists {
		reader.err = errUnknownString
	}
	observation := domain.QuotaObservation{PlanType: plan}
	count := reader.integer()
	if count < -1 || count > int64(len(reader.data)) {
		return observation, errors.New("invalid quota window count")
	}
	if count >= 0 {
		observation.Windows = make([]domain.QuotaWindow, int(count))
	}
	for index := range observation.Windows {
		identity, exists := lookup(reader.integer())
		if !exists {
			reader.err = errUnknownString
		}
		observation.Windows[index] = domain.QuotaWindow{Id: identity, UsedPercent: reader.number(), WindowMinutes: int(reader.integer()), ResetsAt: reader.integer()}
	}
	return observation, reader.finish()
}
