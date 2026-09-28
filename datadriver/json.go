package datadriver

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
)

// MarshalValue encodes arbitrary-precision integers as decimal strings, matching
// contract data-driver conventions without a float64 intermediate. Ordinary Go
// integer types retain JSON-number semantics. Byte slices use JSON byte arrays.
func MarshalValue(value any) ([]byte, error) {
	v, e := jsonValue(reflect.ValueOf(value), 0)
	if e != nil {
		return nil, e
	}
	return json.Marshal(v)
}
func jsonValue(v reflect.Value, depth int) (any, error) {
	if depth > 256 {
		return nil, fmt.Errorf("datadriver: cyclic or overly nested JSON value")
	}
	if !v.IsValid() {
		return nil, nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, nil
		}
		return jsonValue(v.Elem(), depth+1)
	}
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, nil
	}
	if v.CanInterface() {
		switch x := v.Interface().(type) {
		case *big.Int:
			return x.String(), nil
		case big.Int:
			return x.String(), nil
		case json.Marshaler:
			return x, nil
		}
	}
	if v.Kind() == reflect.Pointer {
		return jsonValue(v.Elem(), depth+1)
	}
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return nil, nil
		}
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("datadriver: JSON object keys must be strings")
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			x, e := jsonValue(iter.Value(), depth+1)
			if e != nil {
				return nil, e
			}
			out[iter.Key().String()] = x
		}
		return out, nil
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil, nil
		}
		out := make([]any, v.Len())
		for i := range out {
			x, e := jsonValue(v.Index(i), depth+1)
			if e != nil {
				return nil, e
			}
			out[i] = x
		}
		return out, nil
	case reflect.Struct:
		out := make(map[string]any)
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			skip := false
			for _, opt := range tag[1:] {
				if (opt == "omitempty" || opt == "omitzero") && v.Field(i).IsZero() {
					skip = true
				}
			}
			if skip {
				continue
			}
			name := tag[0]
			if name == "" {
				name = field.Name
			}
			x, e := jsonValue(v.Field(i), depth+1)
			if e != nil {
				return nil, e
			}
			out[name] = x
		}
		return out, nil
	default:
		if v.CanInterface() {
			return v.Interface(), nil
		}
		return nil, fmt.Errorf("datadriver: unsupported JSON value")
	}
}
