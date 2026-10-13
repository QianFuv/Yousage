package history

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

type selection struct {
	all      bool
	children map[string]*selection
}

var allFields = &selection{all: true}
var errorFields = &selection{children: map[string]*selection{"type": allFields, "code": allFields, "status": allFields, "message": allFields}}
var transcriptFields = &selection{children: map[string]*selection{
	"params": {children: map[string]*selection{"sessionId": allFields, "_meta": {children: map[string]*selection{"agentTimestampMs": allFields}}, "update": {children: map[string]*selection{"sessionUpdate": allFields, "prompt_id": allFields, "usage": allFields}}}},
	"reason": allFields, "subtype": allFields, "isApiErrorMessage": allFields, "error": errorFields,
	"type": allFields, "timestamp": allFields, "costUSD": allFields, "sessionId": allFields, "requestId": allFields,
	"uuid": allFields, "parentUuid": allFields, "thinkingDurationMs": allFields, "apiBlockIndex": allFields,
	"message": {children: map[string]*selection{"id": allFields, "model": allFields, "usage": allFields, "stop_reason": allFields}},
	"payload": {children: map[string]*selection{"role": allFields, "reason": allFields, "message": allFields, "error": errorFields, "type": allFields, "id": allFields, "session_id": allFields, "forked_from_id": allFields, "source": {children: map[string]*selection{"subagent": {children: map[string]*selection{"thread_spawn": {children: map[string]*selection{"parent_thread_id": allFields}}}}}}, "model": allFields, "thread_settings": {children: map[string]*selection{"service_tier": allFields}}, "rate_limits": {children: map[string]*selection{"limit_id": allFields, "plan_type": allFields, "primary": {children: map[string]*selection{"used_percent": allFields, "window_minutes": allFields, "resets_at": allFields}}, "secondary": {children: map[string]*selection{"used_percent": allFields, "window_minutes": allFields, "resets_at": allFields}}}}, "info": {children: map[string]*selection{"last_token_usage": allFields}}, "started_at_ms": allFields, "item": {children: map[string]*selection{"type": allFields}}}},
}}
var jsonNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`)

type lineStream struct {
	reader              *bufio.Reader
	ctx                 context.Context
	bytes               int64
	hasNewline, isEnded bool
	pending             byte
	hasPending          bool
}

func (stream *lineStream) read() (byte, error) {
	if stream.hasPending {
		stream.hasPending = false
		return stream.pending, nil
	}
	if stream.isEnded {
		return 0, io.EOF
	}
	if stream.bytes%4096 == 0 {
		if err := stream.ctx.Err(); err != nil {
			return 0, err
		}
	}
	value, err := stream.reader.ReadByte()
	if err != nil {
		stream.isEnded = true
		return 0, err
	}
	stream.bytes++
	if value == '\n' {
		stream.hasNewline, stream.isEnded = true, true
		return 0, io.EOF
	}
	return value, nil
}
func (stream *lineStream) unread(value byte) { stream.pending, stream.hasPending = value, true }
func (stream *lineStream) nonspace() (byte, error) {
	for {
		value, err := stream.read()
		if err != nil {
			return 0, err
		}
		if value != ' ' && value != '\t' && value != '\r' {
			return value, nil
		}
	}
}
func (stream *lineStream) drain() error {
	for {
		_, err := stream.read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// projectValue validates skipped JSON without retaining large unselected tool strings.
func projectValue(stream *lineStream, selected *selection) ([]byte, error) {
	first, err := stream.nonspace()
	if err != nil {
		return nil, err
	}
	isSelected := selected != nil
	if first == '"' {
		return projectString(stream, isSelected, 0)
	}
	if first == '{' || first == '[' {
		closing := byte('}')
		if first == '[' {
			closing = ']'
		}
		result := []byte{}
		if isSelected {
			result = append(result, first)
		}
		next, err := stream.nonspace()
		if err != nil {
			return nil, err
		}
		if next == closing {
			if isSelected {
				result = append(result, closing)
			}
			return result, nil
		}
		stream.unread(next)
		didWrite := false
		for {
			var key []byte
			child := (*selection)(nil)
			if isSelected && selected.all {
				child = allFields
			}
			if first == '{' {
				quote, err := stream.nonspace()
				if err != nil || quote != '"' {
					return nil, errors.New("invalid JSON key")
				}
				key, err = projectString(stream, true, 256)
				if err != nil {
					return nil, err
				}
				colon, err := stream.nonspace()
				if err != nil || colon != ':' {
					return nil, errors.New("invalid JSON colon")
				}
				if isSelected && !selected.all {
					var name string
					if json.Unmarshal(key, &name) == nil {
						child = selected.children[name]
					}
				}
			}
			value, err := projectValue(stream, child)
			if err != nil {
				return nil, err
			}
			if child != nil {
				if didWrite {
					result = append(result, ',')
				}
				if first == '{' {
					result = append(result, key...)
					result = append(result, ':')
				}
				result = append(result, value...)
				didWrite = true
			}
			separator, err := stream.nonspace()
			if err != nil {
				return nil, err
			}
			if separator == closing {
				if isSelected {
					result = append(result, closing)
				}
				return result, nil
			}
			if separator != ',' {
				return nil, errors.New("invalid JSON separator")
			}
		}
	}
	atom := []byte{first}
	for {
		value, err := stream.read()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
			break
		}
		if value == ',' || value == '}' || value == ']' || value == ' ' || value == '\t' || value == '\r' {
			stream.unread(value)
			break
		}
		atom = append(atom, value)
		if len(atom) > 128 {
			return nil, errors.New("invalid JSON atom")
		}
	}
	if string(atom) != "null" && string(atom) != "true" && string(atom) != "false" && !jsonNumberPattern.Match(atom) {
		return nil, errors.New("invalid JSON atom")
	}
	if !isSelected {
		return nil, nil
	}
	return atom, nil
}
func projectString(stream *lineStream, shouldKeep bool, maxBytes int) ([]byte, error) {
	result := []byte{}
	if shouldKeep {
		result = append(result, '"')
	}
	for {
		if !stream.hasPending && !stream.isEnded {
			chunk, err := stream.stringBytes()
			if err != nil {
				return nil, err
			}
			if len(chunk) > 0 {
				if shouldKeep {
					if maxBytes > 0 && len(result)+len(chunk) > maxBytes {
						shouldKeep, result = false, nil
					} else {
						result = append(result, chunk...)
					}
				}
				continue
			}
		}
		value, err := stream.read()
		if err != nil {
			return nil, err
		}
		if value < 0x20 {
			return nil, errors.New("invalid JSON string")
		}
		if shouldKeep {
			result = append(result, value)
			if maxBytes > 0 && len(result) > maxBytes {
				shouldKeep = false
				result = nil
			}
		}
		if value == '"' {
			if maxBytes > 0 && !shouldKeep {
				return []byte(`""`), nil
			}
			return result, nil
		}
		if value != '\\' {
			continue
		}
		escaped, err := stream.read()
		if err != nil {
			return nil, err
		}
		if shouldKeep {
			result = append(result, escaped)
		}
		switch escaped {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			for index := 0; index < 4; index++ {
				digit, err := stream.read()
				if err != nil {
					return nil, err
				}
				if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f') || (digit >= 'A' && digit <= 'F')) {
					return nil, errors.New("invalid Unicode escape")
				}
				if shouldKeep {
					result = append(result, digit)
				}
			}
		default:
			return nil, errors.New("invalid JSON escape")
		}
	}
}

// stringBytes advances ordinary string bytes in bounded spans without retaining skipped text.
// Quotes, escapes and control bytes remain with the validating scalar parser.
func (stream *lineStream) stringBytes() ([]byte, error) {
	if err := stream.ctx.Err(); err != nil {
		return nil, err
	}
	if stream.reader.Buffered() == 0 {
		if _, err := stream.reader.Peek(1); err != nil {
			return nil, err
		}
	}
	data, err := stream.reader.Peek(min(stream.reader.Buffered(), 4096))
	if err != nil {
		return nil, err
	}
	length := 0
	for _, value := range data {
		if value < 0x20 || value == '"' || value == '\\' {
			break
		}
		length++
	}
	stream.reader.Discard(length)
	stream.bytes += int64(length)
	return data[:length], nil
}
