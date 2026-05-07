// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"encoding"
	"errors"
	"iter"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// ValuesUnmarshaler is an interface implemented by types with a custom
// [url.Values] item decoder.
type ValuesUnmarshaler interface {
	UnmarshalValues([]string) error
}

// URLValuesUnmarshaler is an interface implemented by types with a
// custom [url.Values] decoder.
// This can be implemented by a [Form] instance for special use
// cases.
type URLValuesUnmarshaler interface {
	UnmarshalValues(url.Values) error
}

// UnmarshalURLValues decode [url.Values] into v.
// v must be a pointer to a struct. Nested values are supported
// with a "." separator.
// For example, the following struct:
//
//	type nested {
//		Name string `json:"string"`
//		Meta struct {
//			Address string `json:"address"`
//		} `json:"meta"`
//	}
//
// can be decoded with "?name=someone&meta.address=somewhere"
//
// The output struct's fields support the "json" tag.
//
// Each value name matching a struct field is decoded using [UnmarshalValues]
// while values with a prefix are sent again to this function with the prefix
// removed.
func UnmarshalURLValues(values url.Values, v any) (err error) {
	pv := reflect.ValueOf(v)
	cv := reflect.Indirect(pv)
	if pv.Kind() != reflect.Pointer || cv.Kind() != reflect.Struct {
		return errors.New("value must be a pointer to a struct")
	}

	if v, ok := v.(URLValuesUnmarshaler); ok {
		return v.UnmarshalValues(values)
	}

	for fi := range recurseStructFields(cv, "") {
		if fi.field.Tag.Get("json") == "-" {
			continue
		}

		v := fi.value
		if v.Kind() != reflect.Pointer {
			v = v.Addr()
		}

		// Direct value
		if values.Has(fi.name) {
			if err = UnmarshalValues(values[fi.name], v.Interface()); err != nil {
				return err
			}
		}

		// Prefixed values
		prefixedValues := url.Values{}
		for k, val := range values {
			if p, s, _ := strings.Cut(k, "."); p == fi.name && s != "" {
				prefixedValues[s] = val
			}
		}
		if len(prefixedValues) > 0 {
			if err = UnmarshalURLValues(prefixedValues, v.Interface()); err != nil {
				return err
			}
		}
	}
	return nil
}

// UnmarshalValues decodes a list of string into v.
// When v implements [ValuesUnmarshaler], [encoding.TextUnmarshaler]
// or [encoding.BinaryUnmarshaler], their respective unmarshal methods
// have priority (in that order).
//
// Otherwise, scalar values are decoded (bool, string, float, signed and unsigned integer).
// With [encoding.TextUnmarshaler], [encoding.BinaryUnmarshaler] or scalar
// values, only the first item from values is decoded.
//
// When v is a slice, [UnmarshalValues] is called on each item in values.
func UnmarshalValues(values []string, v any) (err error) {
	if v, ok := v.(ValuesUnmarshaler); ok {
		return v.UnmarshalValues(values)
	}

	if len(values) == 0 {
		return nil
	}

	if v, ok := v.(encoding.TextUnmarshaler); ok {
		return v.UnmarshalText([]byte(values[0]))
	}

	if v, ok := v.(encoding.BinaryUnmarshaler); ok {
		return v.UnmarshalBinary([]byte(values[0]))
	}

	pv := reflect.ValueOf(v)
	if pv.Kind() == reflect.Pointer {
		pv = reflect.Indirect(pv)
	}

	switch pv.Type().Kind() {
	case reflect.String:
		pv.SetString(values[0])
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			return err
		}
		pv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(values[0], 10, 64)
		if err != nil {
			return err
		}
		pv.SetUint(n)
	case reflect.Float32:
		n, err := strconv.ParseFloat(values[0], 32)
		if err != nil {
			return err
		}
		pv.SetFloat(n)
	case reflect.Float64:
		n, err := strconv.ParseFloat(values[0], 64)
		if err != nil {
			return err
		}
		pv.SetFloat(n)
	case reflect.Bool:
		ok, err := strconv.ParseBool(values[0])
		if err != nil {
			return err
		}
		pv.SetBool(ok)
	case reflect.Slice:
		pv.Set(reflect.MakeSlice(pv.Type(), len(values), len(values)))
		for i, x := range values {
			d := reflect.New(pv.Type().Elem())
			if err := UnmarshalValues([]string{x}, d.Interface()); err != nil {
				pv.Set(reflect.MakeSlice(pv.Type(), 0, 0))
				return err
			}
			pv.Index(i).Set(reflect.Indirect(d))
		}
	}

	return nil
}

// recurseStructFields iterates recursively over a struc fields.
// It yields a name (prefixed when nested) and a [reflect.Value] of
// anything that is exported.
func recurseStructFields(src reflect.Value, prefix string) iter.Seq[structFieldInfo] {
	return func(yield func(structFieldInfo) bool) {
		if !pushRecurseStructFields(src, prefix, yield) {
			return
		}
	}
}

// pushRecurseStructFields is the recursive iteration pusher.
// It can handle Anonymous struct fields (allowing for form composition)
// and nested structs.
func pushRecurseStructFields(src reflect.Value, prefix string, yield func(structFieldInfo) bool) bool {
	for info := range iterStructFields(src) {
		info.name = prefix + info.name

		if info.field.Type.Kind() == reflect.Struct {
			if !pushRecurseStructFields(info.value, info.name+".", yield) {
				return false
			}
		}

		if !yield(info) {
			return false
		}
	}

	return true
}

// iterStructFields iterates over a struc fields. If a field is anonymous
// it yields its inner values at the same level.
func iterStructFields(src reflect.Value) iter.Seq[structFieldInfo] {
	return func(yield func(structFieldInfo) bool) {
		if !pushIterStructFields(src, yield) {
			return
		}
	}
}

// pushIterStructFields is the iteration pusher.
// It can handle Anonymous struct fields (allowing for form composition).
func pushIterStructFields(src reflect.Value, yield func(structFieldInfo) bool) bool {
	for field, value := range src.Fields() {
		if !field.IsExported() {
			continue
		}

		// An anonymous field stays at the same prefix level
		// and we iterate over its own fields.
		if field.Anonymous {
			if field.Type.Kind() == reflect.Struct {
				if !pushIterStructFields(value, yield) {
					return false
				}
			}
			continue
		}

		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" {
			name = field.Name
		}

		if !yield(structFieldInfo{name: name, field: field, value: value}) {
			return false
		}
	}
	return true
}

type structFieldInfo struct {
	name  string
	field reflect.StructField
	value reflect.Value
}
