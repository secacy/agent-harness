package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
)

type formatError struct{ code, reason string }

func (e *formatError) Error() string { return e.reason }

// 先逐 token 识别重复键；普通 Unmarshal 会静默采用最后一个值。
func jsonValue(d *json.Decoder, depth int) (any, error) {
	if depth > 128 {
		return nil, &formatError{"unsupported_format", "JSON nesting exceeds 128 levels"}
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key must be a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			value, err := jsonValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		_, err = d.Token()
		return object, err
	case '[':
		array := []any{}
		for d.More() {
			value, err := jsonValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err = d.Token()
		return array, err
	}
	return nil, errors.New("unexpected JSON delimiter")
}

func parseJSON(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	value, err := jsonValue(d, 0)
	if err != nil {
		var f *formatError
		if errors.As(err, &f) {
			return nil, err
		}
		return nil, &formatError{"invalid_json", err.Error()}
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return nil, &formatError{"invalid_json", "trailing JSON content"}
	}
	return value, nil
}

// 校验字段存在性与精确拼写，保留 null 与合法零值的区别。
// 只用于本包支持的静态 Go 数据类型，不是可配置 schema 引擎。
func shape(value any, t reflect.Type, field string) error {
	if t == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	if t == reflect.TypeOf([]byte{}) {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a base64 string", field)
		}
		return nil
	}
	if t.Kind() == reflect.Pointer {
		if value == nil {
			return nil
		}
		return shape(value, t.Elem(), field)
	}
	if t == reflect.TypeOf(time.Time{}) {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a timestamp", field)
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", field)
		}
		if t == reflect.TypeOf(budget.Snapshot{}) {
			if err := checkVersion(object, 1); err != nil {
				if err = checkVersion(object, 2); err != nil {
					return err
				}
			}
		}
		known := map[string]bool{}
		for n := 0; n < t.NumField(); n++ {
			f := t.Field(n)
			if !f.IsExported() {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			known[name] = true
		}
		names := make([]string, 0, len(object))
		for name := range object {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !known[name] {
				return &formatError{"unsupported_format", fmt.Sprintf("unsupported field %s.%s", field, name)}
			}
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			known[name] = true
			v, exists := object[name]
			optional := len(tag) > 1 && tag[1] == "omitempty"
			if !exists {
				if optional {
					continue
				}
				return fmt.Errorf("%s.%s is required", field, name)
			}
			if err := shape(v, f.Type, field+"."+name); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value == nil {
			return nil
		}
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", field)
		}
		names := make([]string, 0, len(object))
		for name := range object {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := shape(object[name], t.Elem(), field+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if value == nil {
			return nil
		}
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", field)
		}
		for i, v := range array {
			if err := shape(v, t.Elem(), fmt.Sprintf("%s[%d]", field, i)); err != nil {
				return err
			}
		}
	default:
		if value == nil {
			return fmt.Errorf("%s must not be null", field)
		}
	}
	return nil
}

// DecodeJSON 供可信场景检查共享严格格式规则；version=0 表示沿用父契约。
func DecodeJSON(data []byte, out any, version int) error { return decode(data, out, version) }

// UnsupportedFormat 区分尚不支持的格式与已知格式中的确切损坏。
func UnsupportedFormat(err error) bool {
	var f *formatError
	return errors.As(err, &f) && (f.code == "unsupported_schema" || f.code == "unsupported_format")
}

func decode(data []byte, out any, version int) error {
	value, err := parseJSON(data)
	if err != nil {
		return err
	}
	if version != 0 {
		obj, ok := value.(map[string]any)
		if !ok {
			return &formatError{"invalid_json", "expected JSON object"}
		}
		if err := checkVersion(obj, version); err != nil {
			return err
		}
	}
	if err = shape(value, reflect.TypeOf(out).Elem(), "$"); err != nil {
		var formatted *formatError
		if errors.As(err, &formatted) {
			return err
		}
		return &formatError{"invalid_json", err.Error()}
	}
	if err = json.Unmarshal(data, out); err != nil {
		return &formatError{"invalid_json", err.Error()}
	}
	return nil
}

func checkVersion(obj map[string]any, version int) error {
	raw, exists := obj["schema_version"]
	if !exists {
		return &formatError{"invalid_json", "schema_version is required"}
	}
	n, ok := raw.(json.Number)
	if !ok {
		return &formatError{"invalid_json", "schema_version must be an integer"}
	}
	actual, err := n.Int64()
	if err != nil {
		return &formatError{"invalid_json", "schema_version must be an integer"}
	}
	if actual != int64(version) {
		return &formatError{"unsupported_schema", fmt.Sprintf("unsupported schema_version %d", actual)}
	}
	return nil
}
