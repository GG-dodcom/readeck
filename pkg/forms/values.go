// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

var _ SetterValuer[any] = (*BaseValue[any])(nil)

// timeFormats is a list of supported [time.Time] formats for
// [Value[time.Time]] and [Value[[]time.Time]].
var timeFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04",    // as sent by <input type="datetime-local">
	time.DateOnly,         // as sent by <input type="date">
	"2006-01-02T15:04:05", // RFC3339 without tz
	time.RFC822,
	time.RFC822Z,
	time.DateTime,
}

// nilText is a text null value.
// In an URL of form value, it would be a field with %EF%BC%80 value.
// On an HTML field, it's simply &#xff00.
const nilText = "\uff00"

// numberType is a constraint used by number related values and fields.
type numberType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// ValueFlags holds the flags a value can get.
type ValueFlags byte

const (
	// IsEmpty is the flag for an empty value.
	IsEmpty ValueFlags = 1 << iota
	// IsBound is the flag for a bound value.
	IsBound
	// IsNil is the flag for a nil value.
	IsNil
)

// IsBound returns true when [IsBound] is present in the flags.
func (f ValueFlags) IsBound() bool {
	return f&IsBound > 0
}

// IsNil returns true when [IsNil] is present in the flags.
func (f ValueFlags) IsNil() bool {
	return f&IsNil > 0
}

// IsEmpty returns true when [IsEmpty] is present in the flags.
func (f ValueFlags) IsEmpty() bool {
	return f&IsEmpty > 0
}

// Valuer is the interface implemented by types that can load and decode input data.
type Valuer[T any] interface {
	fmt.Stringer
	json.Marshaler

	Value() T
	Flags() ValueFlags
	IsBound() bool
	IsNil() bool
	IsEmpty() bool
}

// Setter describes a type that can sets its value.
type Setter[T any] interface {
	Set(T)
}

// FlagSetter describes a type that can sets its flags.
type FlagSetter interface {
	SetFlags(f ValueFlags)
}

// SetterValuer describes a [Valuer], [Setter] and [FlagSetter] type.
type SetterValuer[T any] interface {
	Valuer[T]
	Setter[T]
	FlagSetter
}

// ErrValueFlags is an error that can be returned from [DecodeValueData].
type ErrValueFlags ValueFlags

func (e ErrValueFlags) Error() string {
	return ""
}

// BaseValue is an implementation of [Valuer]. It provides a working
// decoder and a generic [fmt.Stringer] without any specialization.
type BaseValue[T any] struct {
	flags      ValueFlags
	validators []Validator
	value      T
}

// UnmarshalJSON implements [json.Unmarshaler].
func (v *BaseValue[T]) UnmarshalJSON(in []byte) error {
	return DecodeValueData(v, in, func(data []byte) (res *T, err error) {
		res = new(T)
		err = json.Unmarshal(data, res)
		return res, err
	})
}

// UnmarshalValues implement [ValuesUnmarshaler].
func (v *BaseValue[T]) UnmarshalValues(values []string) error {
	return DecodeValueData(v, values, func(data []string) (res *T, err error) {
		if len(data) == 0 || data[0] == nilText {
			return nil, nil
		}

		res = new(T)
		err = UnmarshalValues(data, res)
		return res, err
	})
}

// Set sets the value's value.
func (v *BaseValue[T]) Set(in T) {
	v.value = in
}

// Value returns the value's value.
func (v BaseValue[T]) Value() T {
	return v.value
}

// String returns a string representation of the value.
func (v BaseValue[T]) String() string {
	if t, ok := any(v.value).(fmt.Stringer); ok {
		return t.String()
	}

	return fmt.Sprintf("%v", v.value)
}

// MarshalJSON implements [json.Marshaler].
func (v BaseValue[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.value)
}

// Validators implements [ValidatorsProvider] and returns
// the value's validators.
func (v BaseValue[T]) Validators() []Validator {
	return v.validators
}

// SetValidators implements [ValidatorsProvider]
// and sets the value's validators.
func (v *BaseValue[T]) SetValidators(validators []Validator) {
	v.validators = validators
}

// Flags returns the value's [ValueFlags].
func (v BaseValue[T]) Flags() ValueFlags {
	return v.flags
}

// SetFlags replaces the value's [ValueFlags].
func (v *BaseValue[T]) SetFlags(f ValueFlags) {
	v.flags = f
}

// IsBound return true if the value was attached after decoding.
func (v BaseValue[T]) IsBound() bool {
	return v.flags.IsBound()
}

// IsNil returns true if the input value was null.
func (v BaseValue[T]) IsNil() bool {
	return v.flags.IsNil()
}

// IsEmpty return true if the value is empty.
func (v BaseValue[T]) IsEmpty() bool {
	return v.flags.IsEmpty()
}

// StringValue is an alias to [BaseValue] for [string] type.
type StringValue = BaseValue[string]

// BooleanValue is a [Valuer] for [bool] types.
type BooleanValue struct {
	BaseValue[bool]
}

func (v BooleanValue) String() string {
	if !v.IsBound() || v.IsNil() {
		return ""
	}
	return strconv.FormatBool(v.value)
}

// UnmarshalValues implements [ValuesUnmarshaler]. It parses a value "on" as true.
func (v *BooleanValue) UnmarshalValues(values []string) error {
	return DecodeValueData(v, values, func(data []string) (res *bool, err error) {
		if len(data) == 0 || data[0] == nilText {
			return nil, nil
		}

		data[0] = ApplyCleaners(v, data[0])
		switch data[0] {
		case "":
			// Empty is false but must stay empty
			return new(false), ErrValueFlags(IsEmpty)
		case "on":
			return new(true), nil
		}

		res = new(bool)
		err = UnmarshalValues(data, res)
		return res, err
	})
}

// NumberValue is a [Valuer] for all int, uint and float types.
// Its decoder supports int and uint types with trailing zero decimals.
type NumberValue[T numberType] struct {
	BaseValue[T]
}

// UnmarshalJSON implements [json.Unmarshaler].
func (v *NumberValue[T]) UnmarshalJSON(in []byte) error {
	return DecodeValueData(v, in, func(data []byte) (res *T, err error) {
		var n json.Number
		if err := json.Unmarshal(data, &n); err != nil {
			return nil, err
		}

		x, err := decodeNumber[T](n.String()) //nolint:staticcheck // it is used!
		if err != nil {
			return nil, err
		}

		res = new(x)
		return res, nil
	})
}

// UnmarshalValues implements [ValuesUnmarshaler].
func (v *NumberValue[T]) UnmarshalValues(values []string) error {
	return DecodeValueData(v, values, func(data []string) (res *T, err error) {
		if len(values) == 0 || values[0] == nilText {
			return nil, nil
		}

		data[0] = ApplyCleaners(v, data[0])
		if data[0] == "" {
			// Empty is false but must stay empty
			return new(T), ErrValueFlags(IsEmpty)
		}

		var s string
		UnmarshalValues(values, &s) //nolint:errcheck // never fails on strings

		x, err := decodeNumber[T](s) //nolint:staticcheck // it is used!
		if err != nil {
			return nil, err
		}

		res = new(x)
		return res, nil
	})
}

// String returns the number formatted according to its type.
func (v NumberValue[T]) String() string {
	if v.IsEmpty() || v.IsNil() {
		return ""
	}

	var t any = new(T)
	switch t.(type) {
	case *int, *int8, *int16, *int32, *int64:
		return strconv.FormatInt(int64(v.value), 10)
	case *uint, *uint8, *uint16, *uint32, *uint64:
		return strconv.FormatUint(uint64(v.value), 10)
	case *float32, *float64:
		return strconv.FormatFloat(float64(v.value), 'g', -1, 64)
	}

	return ""
}

// DatetimeValue is a [Valuer] for [time.Time] values.
// It can parse more formats than what's allowed by [time.Time]
// unmarshal functions.
type DatetimeValue struct {
	BaseValue[time.Time]
}

// UnmarshalJSON implements [json.Unmarshaler].
func (v *DatetimeValue) UnmarshalJSON(in []byte) error {
	return DecodeValueData(v, in, func(data []byte) (res *time.Time, err error) {
		res = new(time.Time)

		var i int64
		if err := json.Unmarshal(data, &i); err == nil {
			// we have a timestamp
			*res = time.Unix(i, 0).UTC()
			return res, nil
		}

		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}

		*res, err = decodeTime(s)
		return res, err
	})
}

// UnmarshalValues implements [ValuesUnmarshaler].
func (v *DatetimeValue) UnmarshalValues(values []string) error {
	return DecodeValueData(v, values, func(data []string) (res *time.Time, err error) {
		if len(values) == 0 || values[0] == nilText {
			return nil, nil
		}

		data[0] = ApplyCleaners(v, data[0])
		if data[0] == "" {
			return nil, nil
		}

		res = new(time.Time)

		if i, err := strconv.ParseInt(values[0], 10, 64); err == nil {
			// timestamp
			*res = time.Unix(i, 0).UTC()
			return res, nil
		}

		var s string
		UnmarshalValues(values, &s) //nolint:errcheck // never fails on strings
		*res, err = decodeTime(s)
		return res, err
	})
}

// String returns the value formatted with [time.RFC3339].
func (v DatetimeValue) String() string {
	if !v.value.IsZero() {
		return v.value.Format(time.RFC3339)
	}
	return ""
}

// URLValue is a [Valuer] that can decode [url.URL] values.
// Input values are decoded using [url.Parse] and no further
// URL validation is performed.
type URLValue struct {
	BaseValue[url.URL]
}

// UnmarshalJSON decodes an [url.URL] from a JSON string.
func (v *URLValue) UnmarshalJSON(in []byte) error {
	return DecodeValueData(v, in, func(data []byte) (res *url.URL, err error) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}

		return url.Parse(s)
	})
}

// UnmarshalValues decoded an [url.URL] from a value string.
func (v *URLValue) UnmarshalValues(values []string) error {
	return DecodeValueData(v, values, func(data []string) (res *url.URL, err error) {
		if len(values) == 0 || values[0] == nilText {
			return nil, nil
		}

		data[0] = ApplyCleaners(v, data[0])
		if data[0] == "" {
			return nil, nil
		}

		return url.Parse(data[0])
	})
}

func (v URLValue) String() string {
	return v.value.String()
}

// MarshalJSON implements [json.Marshaler].
func (v URLValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

// ListValue is a generic [Valuer] for list of values.
// It works by wrapping unmarshal calls to the Valuer V and then set its
// own value from the collected results.
type ListValue[T any, V Valuer[T]] struct {
	BaseValue[[]T]
}

// UnmarshalJSON implements [json.Unmarshaler].
func (v *ListValue[T, V]) UnmarshalJSON(in []byte) error {
	v.value = []T{}
	return DecodeValueData(v, in, func(data []byte) (res *[]T, err error) {
		// We're going to unmarshal into a list of [Valuer], leveraging
		// their own unmarshal functions
		var items []V

		// we always return an empty list
		res = new([]T{})

		if err := json.Unmarshal(data, &items); err != nil {
			return res, err
		}
		items = slices.DeleteFunc(items, func(v V) bool {
			return v.IsNil()
		})

		// Collect cleaned up items
		for _, item := range items {
			*res = append(*res, ApplyCleaners(v, item.Value()))
		}

		return res, nil
	})
}

// UnmarshalValues implements [ValuesUnmarshaler].
func (v *ListValue[T, V]) UnmarshalValues(values []string) error {
	// Same process as [ListValue.UnmarshalJSON] except for the
	// unmarshaler.

	v.value = []T{}
	return DecodeValueData(v, values, func(data []string) (res *[]T, err error) {
		if len(data) == 1 && data[0] == nilText {
			return nil, nil
		}

		var items []V
		res = new([]T{})

		if err := UnmarshalValues(data, &items); err != nil {
			return res, err
		}

		items = slices.DeleteFunc(items, func(v V) bool {
			return v.IsNil()
		})

		for _, item := range items {
			*res = append(*res, ApplyCleaners(v, item.Value()))
		}

		return res, nil
	})
}

// UnmarshalFiles implements [FilesUnmarshaler]. It only works on
// [Valuer] items implementing [FilesUnmarshaler] themselves.
func (v *ListValue[T, V]) UnmarshalFiles(files []*multipart.FileHeader) error {
	v.value = []T{}
	v.SetFlags(v.Flags() | IsEmpty)

	if len(files) == 0 {
		v.SetFlags(IsEmpty | IsBound)
		return nil
	}

	for _, file := range files {
		var item V
		if u, ok := any(&item).(FilesUnmarshaler); ok {
			if err := u.UnmarshalFiles([]*multipart.FileHeader{file}); err != nil {
				return err
			}
			v.value = append(v.value, item.Value())
		}
	}

	v.SetFlags(v.Flags() | IsBound)
	if len(v.value) > 0 {
		v.SetFlags(v.Flags() &^ IsEmpty)
	}

	return nil
}

// String returns a simple comma separated list of each values' String() result.
func (v ListValue[T, V]) String() string {
	isSetter := v.isSetterValuer()
	var vv V

	b := new(strings.Builder)
	for i, x := range v.value {
		if isSetter {
			any(&vv).(Setter[T]).Set(x)
			b.WriteString(vv.String())
		} else {
			fmt.Fprintf(b, "%v", x)
		}
		if i+1 < len(v.value) {
			b.WriteString(", ")
		}
	}

	return b.String()
}

// MarshalJSON implements [json.Marshaler].
func (v ListValue[T, V]) MarshalJSON() ([]byte, error) {
	if v.isSetterValuer() {
		// If we can, we're using the underlyin [Valuer] to marshal each item.
		values := make([]V, len(v.value))
		for i, x := range v.value {
			any(&values[i]).(Setter[T]).Set(x)
		}

		return json.Marshal(values)
	}

	// Fallback to marshaling the list.
	return v.BaseValue.MarshalJSON()
}

func (v ListValue[T, V]) isSetterValuer() (ok bool) {
	_, ok = any(new(V)).(Setter[T])
	return ok
}

// PivotListValue is a [Valuer] that holds a list of T items.
// Decoding uses an intermediate [ListValue] with PT type and PV [Valuer].
// This is useful when you need to compose list of types but need some
// cleaners to run on the intermediate type first.
// For example, a URL list would be PivotListValue[url.URL, URLValue, string, StringValue].
type PivotListValue[T any, V Valuer[T], PT any, PV Valuer[PT]] struct {
	ListValue[T, V]
}

// UnmarshalJSON implements [json.Unmarshaler].
func (v *PivotListValue[T, V, PT, PV]) UnmarshalJSON(in []byte) error {
	v.value = []T{}
	return DecodeValueData(v, in, func(data []byte) (res *[]T, err error) {
		// First decode into the intermediate list
		var pivot ListValue[PT, PV]
		(&pivot).validators = v.validators
		if err = json.Unmarshal(data, &pivot); err != nil {
			return nil, err
		}

		return v.decodeIntermediate(pivot)
	})
}

// UnmarshalValues implements [ValuesUnmarshaler].
func (v *PivotListValue[T, V, PT, PV]) UnmarshalValues(values []string) error {
	v.value = []T{}
	return DecodeValueData(v, values, func(data []string) (res *[]T, err error) {
		var pivot ListValue[PT, PV]
		(&pivot).validators = v.validators
		if err = UnmarshalValues(data, &pivot); err != nil {
			return nil, err
		}

		return v.decodeIntermediate(pivot)
	})
}

func (v *PivotListValue[T, V, PT, PV]) decodeIntermediate(pivot ListValue[PT, PV]) (res *[]T, err error) {
	buf := new(bytes.Buffer)
	if err = json.NewEncoder(buf).Encode(pivot); err != nil {
		return nil, err
	}

	// Sadly we need to perform []PT -> json -> []T here
	// It comes with the benefit of applying any existing cleaner for T
	var items ListValue[T, V]
	(&items).validators = v.validators
	if err = json.NewDecoder(buf).Decode(&items); err != nil {
		return nil, err
	}

	res = new([]T)
	*res = append(*res, items.Value()...)
	return res, nil
}

func decodeNumber[T numberType](s string) (res T, err error) {
	var nv any = new(T)
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return res, err
	}

	switch nv.(type) {
	case *int, *int8, *int16, *int32, *int64, *uint, *uint8, *uint16, *uint32, *uint64:
		if res = T(n); float64(res) == n {
			return res, nil
		}
		return T(0), errors.New("invalid integer value")
	case *float32, *float64:
		res = T(n)
		return res, nil
	}

	return res, nil
}

func decodeTime(s string) (res time.Time, err error) {
	for _, f := range timeFormats {
		err = nil
		res, err = time.Parse(f, s)
		if err == nil {
			return res, nil
		}
	}
	return res, err
}

type inputData interface {
	[]byte | []string
}

// DecodeValueData is the function that decodes a value and
// sets its flags.
// It receives a [SetterValuer] an input ([]byte or []string) and a suitable
// unmarshal function.
//
// When the unmarshal function returns nil without error, the value's flags
// are set to [IsNil], [IsEmpty], [IsBound] and [IsOk].
//
// When the unmarshal function returns an [ErrValueFlags] error, the error is ignored
// and its flag is added to the existing ones.
func DecodeValueData[IN inputData, T any](
	v SetterValuer[T],
	data IN,
	unmarshal func(data IN) (res *T, err error),
) error {
	v.SetFlags(v.Flags() | IsEmpty)

	switch data := any(data).(type) {
	case []byte:
		if len(data) == 4 && bytes.Equal(data, []byte("null")) {
			v.SetFlags(IsEmpty | IsNil | IsBound)
			return nil
		}
	case []string:
		if len(data) == 0 {
			v.SetFlags(IsEmpty | IsBound)
			return nil
		}
	}

	// Unmarshal call. If the error is [errValueFlags], it's ignored and
	// will be passed to the final flag setter.
	res, err := unmarshal(data)
	if _, ok := errors.AsType[ErrValueFlags](err); err != nil && !ok {
		return err
	}

	if res == nil {
		v.SetFlags(IsEmpty | IsNil | IsBound)
		return nil
	}

	// Received data cleanup
	if v, ok := v.(ValidatorsProvider); ok {
		*res = ApplyCleaners(v, *res)
	}

	// Set value and flags
	v.Set(*res)

	flags := v.Flags() | IsBound
	if f, ok := errors.AsType[ErrValueFlags](err); ok {
		flags |= ValueFlags(f)
	} else {
		setFlags(&flags, v.Value())
	}
	v.SetFlags(flags)

	return nil
}

func setFlags(flags *ValueFlags, v any) {
	switch v := v.(type) {
	case bool, int, int16, int32, int64, uint, uint16, uint32, uint64, float32, float64:
		// This types are not empty
		*flags &^= IsEmpty
		return
	case string:
		// String with content is not empty
		if len(v) > 0 {
			*flags &^= IsEmpty
		}
		return
	}

	pv := reflect.ValueOf(v)
	if pv.Kind() == reflect.Pointer {
		pv = reflect.Indirect(pv)
	}

	if pv.Kind() == reflect.Slice {
		if pv.Len() == 0 {
			// Empty slice is empty
			*flags |= IsEmpty
		} else {
			*flags &^= IsEmpty
		}
		return
	}

	// Default to zero value check
	if !pv.IsZero() {
		*flags &^= IsEmpty
	}
}
