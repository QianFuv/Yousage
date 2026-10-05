// Package antigravity reads protobuf usage facts without reading conversation text.
package antigravity

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxSafeInteger = uint64(1<<53 - 1)

type fields map[uint64][]any

func parseFields(data []byte) (fields, error) {
	position := 0
	varint := func() (uint64, error) {
		var value uint64
		for shift := uint(0); shift < 70; shift += 7 {
			if position == len(data) {
				return 0, errors.New("truncated protobuf varint")
			}
			current := data[position]
			position++
			if shift == 63 && current > 1 {
				return 0, errors.New("overflowing protobuf varint")
			}
			value |= uint64(current&127) << shift
			if current < 128 {
				return value, nil
			}
		}
		return 0, errors.New("invalid protobuf varint")
	}
	result := fields{}
	for position < len(data) {
		tag, err := varint()
		if err != nil {
			return nil, err
		}
		if tag > maxSafeInteger || tag/8 == 0 {
			return nil, errors.New("invalid protobuf field")
		}
		var value any
		switch tag % 8 {
		case 0:
			value, err = varint()
		case 1, 2, 5:
			length := uint64(8)
			if tag%8 == 5 {
				length = 4
			}
			if tag%8 == 2 {
				length, err = varint()
			}
			if err == nil && (length > maxSafeInteger || length > uint64(len(data)-position)) {
				err = errors.New("truncated protobuf field")
			}
			if err == nil {
				value = data[position : position+int(length)]
				position += int(length)
				if tag%8 != 2 {
					continue
				}
			}
		default:
			return nil, errors.New("unsupported protobuf wire type")
		}
		if err != nil {
			return nil, err
		}
		result[tag/8] = append(result[tag/8], value)
	}
	return result, nil
}

func first(value fields, key uint64) any {
	if len(value[key]) > 0 {
		return value[key][0]
	}
	return nil
}
func numberAt(value fields, key uint64) int64 {
	number, ok := first(value, key).(uint64)
	if !ok || number > maxSafeInteger {
		return 0
	}
	return int64(number)
}
func bytesAt(value fields, key uint64) []byte         { data, _ := first(value, key).([]byte); return data }
func nested(value fields, key uint64) (fields, error) { return parseFields(bytesAt(value, key)) }
func textAt(value fields, key uint64) (string, error) {
	data := bytesAt(value, key)
	if !utf8.Valid(data) {
		return "", errors.New("invalid UTF-8 protobuf text")
	}
	return strings.TrimSpace(string(data)), nil
}
func timestamp(value fields) *int64 {
	seconds := numberAt(value, 1)
	if seconds <= 0 {
		return nil
	}
	instant := seconds*1000 + numberAt(value, 2)/1000000
	return &instant
}

var modelIds = map[int64]string{
	246: "gemini-2.5-pro", 312: "gemini-2.5-flash", 313: "gemini-2.5-flash-thinking", 329: "gemini-2.5-flash-thinking", 330: "gemini-2.5-flash-lite",
	281: "claude-sonnet-4", 282: "claude-sonnet-4", 290: "claude-opus-4", 291: "claude-opus-4", 333: "claude-sonnet-4-5", 334: "claude-sonnet-4-5", 340: "claude-haiku-4-5", 341: "claude-haiku-4-5",
	1026: "claude-opus-4-6", 1035: "claude-sonnet-4-6", 1016: "gemini-3.1-pro", 1036: "gemini-3.1-pro", 1037: "gemini-3.1-pro", 1018: "gemini-3-flash-preview", 1084: "gemini-3-flash-preview", 1047: "gemini-3-flash-preview",
}
var modelSuffix = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
var claudeGeneration = regexp.MustCompile(`^claude-(4(?:\.\d+)?)-(sonnet|opus|haiku)`)

func modelName(name string, id int64) string {
	if name != "" {
		name = strings.ReplaceAll(modelSuffix.ReplaceAllString(strings.ToLower(name), ""), " ", "-")
		if strings.HasPrefix(name, "claude-") {
			name = strings.ReplaceAll(claudeGeneration.ReplaceAllString(name, "claude-$2-$1"), ".", "-")
		}
		return name
	}
	if name = modelIds[id]; name != "" {
		return name
	}
	if id > 0 {
		return fmt.Sprintf("antigravity-model-%d", id)
	}
	return ""
}

type metadata struct {
	model   string
	instant *int64
	usages  []fields
}

func parseMetadata(blob []byte, isStep bool) (metadata, error) {
	result := metadata{}
	root, err := parseFields(blob)
	if err != nil {
		return result, err
	}
	data := root
	if !isStep {
		if bytesAt(root, 1) == nil {
			return result, errors.New("missing generation metadata")
		}
		data, err = nested(root, 1)
		if err != nil {
			return result, err
		}
	}
	model := data
	usageKey, retryKey, firstName, secondName, modelId := uint64(4), uint64(17), uint64(19), uint64(21), uint64(3)
	if isStep {
		model, err = nested(data, 24)
		if err != nil {
			return result, err
		}
		usageKey, retryKey, firstName, secondName, modelId = 9, 28, 12, 8, 1
	}
	name, err := textAt(model, firstName)
	if err != nil {
		return result, err
	}
	if name == "" {
		name, err = textAt(model, secondName)
	}
	if err != nil {
		return result, err
	}
	result.model = modelName(name, numberAt(model, modelId))
	if bytesAt(data, usageKey) != nil {
		usage, err := nested(data, usageKey)
		if err != nil {
			return result, err
		}
		result.usages = append(result.usages, usage)
	}
	for _, value := range data[retryKey] {
		retry, ok := value.([]byte)
		if !ok {
			return result, errors.New("invalid retry metadata")
		}
		parsed, err := parseFields(retry)
		if err != nil {
			return result, err
		}
		if bytesAt(parsed, 2) != nil {
			usage, err := nested(parsed, 2)
			if err != nil {
				return result, err
			}
			result.usages = append(result.usages, usage)
		}
	}
	timeData, err := nested(data, 9)
	if isStep {
		timeData, err = nested(data, 8)
	}
	if err != nil {
		return result, err
	}
	if !isStep {
		timeData, err = nested(timeData, 4)
	}
	if err != nil {
		return result, err
	}
	result.instant = timestamp(timeData)
	if isStep && result.instant == nil {
		timeData, err = nested(data, 1)
		if err != nil {
			return result, err
		}
		result.instant = timestamp(timeData)
	}
	return result, nil
}
