package mobile

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	datasync "yousage/internal/sync"
)

// snapshotProblem checks the supported viewer contract without interpreting provider names.
// Missing scalar fields retain binding defaults; nullable pointers and collections retain
// their binding normalization. Required roots are checked separately from Go zero values.
func snapshotProblem(payload []byte) string {
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil || root == nil {
		return "data"
	}
	var version int
	if bytes.Equal(bytes.TrimSpace(root["formatVersion"]), []byte("null")) || json.Unmarshal(root["formatVersion"], &version) != nil {
		return "data"
	}
	if version != 1 {
		return "version"
	}
	for _, name := range []string{"generatedAt", "appVersion", "timeZone"} {
		var value string
		if json.Unmarshal(root[name], &value) != nil || strings.TrimSpace(value) == "" {
			return "data"
		}
		if name == "generatedAt" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return "data"
			}
		}
	}
	periods := snapshotObject(root["periods"])
	for _, days := range []string{"1", "7", "30", "90"} {
		period := snapshotObject(periods[days])
		for _, report := range []string{"usage", "loss", "speed", "receipt"} {
			if snapshotObject(period[report]) == nil {
				return "data"
			}
		}
	}
	display := snapshotObject(root["display"])
	if snapshotObject(display["value"]) == nil || snapshotObject(root["appearance"]) == nil || snapshotObject(root["bootstrap"]) == nil {
		return "data"
	}
	if !snapshotValue(root["bootstrap"], reflect.TypeFor[struct {
		IsSystemDark bool   `json:"isSystemDark"`
		SystemLocale string `json:"systemLocale"`
	}]()) || !snapshotValue(payload, reflect.TypeFor[datasync.Snapshot]()) {
		return "data"
	}
	return ""
}

// snapshotObject distinguishes a JSON object from absent, null, and array values.
func snapshotObject(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

// snapshotValue validates present fields against the source schema, including nested
// collections and embedded reports. It deliberately permits omitted fields supplied
// by generated binding defaults and future fields unknown to this application.
func snapshotValue(raw json.RawMessage, schema reflect.Type) bool {
	if schema == reflect.TypeFor[json.RawMessage]() || schema.Kind() == reflect.Interface {
		return true
	}
	isNull := bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	switch schema.Kind() {
	case reflect.Pointer:
		return isNull || snapshotValue(raw, schema.Elem())
	case reflect.Struct:
		fields := snapshotObject(raw)
		if fields == nil {
			return false
		}
		for index := 0; index < schema.NumField(); index++ {
			field := schema.Field(index)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if field.Anonymous && name == "" {
				if !snapshotValue(raw, field.Type) {
					return false
				}
				continue
			}
			if name == "" {
				name = field.Name
			}
			if value, exists := fields[name]; exists && !snapshotValue(value, field.Type) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if isNull {
			return true
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return false
		}
		for _, value := range values {
			if !snapshotValue(value, schema.Elem()) {
				return false
			}
		}
		return true
	case reflect.Map:
		if isNull {
			return true
		}
		values := snapshotObject(raw)
		if values == nil {
			return false
		}
		for _, value := range values {
			if !snapshotValue(value, schema.Elem()) {
				return false
			}
		}
		return true
	default:
		return !isNull && json.Unmarshal(raw, reflect.New(schema).Interface()) == nil
	}
}
