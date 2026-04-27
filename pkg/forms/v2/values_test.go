// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms_test

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

func TestValueFlags(t *testing.T) {
	tests := []struct {
		f       forms.ValueFlags
		isOk    bool
		isBound bool
		isNil   bool
		isEmpty bool
	}{
		{forms.ValueFlags(0), false, false, false, false},
		{forms.IsNil, false, false, true, false},
		{forms.IsBound | forms.IsNil | forms.IsEmpty, true, true, true, true},
		{(forms.IsBound | forms.IsNil) &^ forms.IsBound, false, false, true, false},
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			assert := assert.New(t)
			assert.Equal(test.isBound, test.f.IsBound())
			assert.Equal(test.isNil, test.f.IsNil())
			assert.Equal(test.isEmpty, test.f.IsEmpty())
		})
	}
}

type valueTest[D any, T any] struct {
	data  D
	value T
	str   string
	flags forms.ValueFlags
	err   error
}

func (test valueTest[D, T]) assert(t *testing.T, value forms.Valuer[T], err error) {
	t.Logf("%s : %#v", must(json.Marshal(value)), value)
	t.Logf("bound: %v, empty: %v, nil: %v, flags: %x", value.IsBound(), value.IsEmpty(), value.IsNil(), value.Flags())
	assert := assert.New(t)
	require := require.New(t)

	if test.err == nil {
		require.NoError(err, "no error")
	} else {
		require.ErrorContains(err, test.err.Error())
	}

	assert.Exactly(test.str, value.String(), "string")
	assert.Exactly(test.value, value.Value(), "value")
	assert.Exactly(test.flags, value.Flags(), "flags")
}

func runJSONValue[T any, V forms.Valuer[T]](tests []valueTest[string, T]) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				value := *new(V)
				err := json.Unmarshal([]byte(test.data), &value)
				test.assert(t, value, err)
			})
		}
	}
}

func runValuesValue[T any, V forms.Valuer[T]](tests []valueTest[[]string, T]) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				value := *new(V)
				err := forms.UnmarshalValues(test.data, &value)
				test.assert(t, value, err)
			})
		}
	}
}

func TestStringValue(t *testing.T) {
	t.Run("json", runJSONValue[string, forms.StringValue]([]valueTest[string, string]{
		{
			data:  "null",
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  `""`,
			flags: forms.IsBound | forms.IsEmpty,
		},
		{
			data:  `"abc"`,
			value: "abc",
			str:   "abc",
			flags: forms.IsBound,
		},
		{
			data:  "123",
			flags: forms.IsEmpty,
			err:   errors.New("cannot unmarshal number"),
		},
	}))

	t.Run("values", runValuesValue[string, forms.StringValue]([]valueTest[[]string, string]{
		{
			data:  []string{},
			flags: forms.IsBound | forms.IsEmpty,
		},
		{
			data:  []string{"\uff00"},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  []string{""},
			flags: forms.IsBound | forms.IsEmpty,
		},
		{
			data:  []string{"abc"},
			flags: forms.IsBound,
			value: "abc",
			str:   "abc",
		},
		{
			data:  []string{"xyz", "abc"},
			flags: forms.IsBound,
			value: "xyz",
			str:   "xyz",
		},
	}))
}

func TestBooleanValue(t *testing.T) {
	t.Run("json", runJSONValue[bool, forms.BooleanValue]([]valueTest[string, bool]{
		{
			data:  "null",
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  "true",
			flags: forms.IsBound,
			value: true,
			str:   "true",
		},
		{
			data:  "false",
			flags: forms.IsBound,
			value: false,
			str:   "false",
		},
		{
			data:  `"abc"`,
			flags: forms.IsEmpty,
			value: false,
			err:   errors.New("cannot unmarshal string into"),
		},
	}))

	t.Run("values", runValuesValue[bool, forms.BooleanValue]([]valueTest[[]string, bool]{
		{
			data:  []string{},
			flags: forms.IsBound | forms.IsEmpty,
			str:   "false",
		},
		{
			data:  []string{"\uff00"},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  []string{""},
			flags: forms.IsBound | forms.IsEmpty,
			value: false,
			str:   "false",
		},
		{
			data:  []string{"on"},
			flags: forms.IsBound,
			value: true,
			str:   "true",
		},
		{
			data:  []string{"t"},
			flags: forms.IsBound,
			value: true,
			str:   "true",
		},
		{
			data:  []string{"0"},
			flags: forms.IsBound,
			value: false,
			str:   "false",
		},
		{
			data:  []string{"1"},
			flags: forms.IsBound,
			value: true,
			str:   "true",
		},
		{
			data:  []string{"abc"},
			flags: forms.IsEmpty,
			value: false,
			err:   errors.New("invalid syntax"),
		},
	}))
}

func TestNumberValueInt(t *testing.T) {
	t.Run("json", runJSONValue[int, forms.NumberValue[int]]([]valueTest[string, int]{
		{
			data:  "null",
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  "0",
			flags: forms.IsBound,
			str:   "0",
		},
		{
			data:  "10",
			flags: forms.IsBound,
			value: 10,
			str:   "10",
		},
		{
			data:  "123.0",
			flags: forms.IsBound,
			value: 123,
			str:   "123",
		},
		{
			data:  "9.2",
			flags: forms.IsEmpty,
			err:   errors.New("invalid integer value"),
		},
		{
			data:  `"abc"`,
			flags: forms.IsEmpty,
			err:   errors.New("invalid number literal"),
		},
	}))

	t.Run("values", runValuesValue[int, forms.NumberValue[int]]([]valueTest[[]string, int]{
		{
			data:  []string{},
			flags: forms.IsBound | forms.IsEmpty,
		},
		{
			data:  []string{"\uff00"},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  []string{""},
			flags: forms.IsBound | forms.IsEmpty,
		},
		{
			data:  []string{"0"},
			flags: forms.IsBound,
			str:   "0",
		},
		{
			data:  []string{"10.0"},
			flags: forms.IsBound,
			value: 10,
			str:   "10",
		},
		{
			data:  []string{"9.2"},
			flags: forms.IsEmpty,
			err:   errors.New("invalid integer value"),
		},
		{
			data:  []string{"-5"},
			flags: forms.IsBound,
			value: -5,
			str:   "-5",
		},
		{
			data:  []string{"abc"},
			flags: forms.IsEmpty,
			err:   errors.New("invalid syntax"),
		},
	}))
}

func TestNumberValueUInt(t *testing.T) {
	t.Run("json", runJSONValue[uint, forms.NumberValue[uint]]([]valueTest[string, uint]{
		{
			data:  "10",
			flags: forms.IsBound,
			value: 10,
			str:   "10",
		},
		{
			data:  "-5",
			flags: forms.IsEmpty,
			err:   errors.New("invalid integer value"),
		},
		{
			data:  "9.2",
			flags: forms.IsEmpty,
			err:   errors.New("invalid integer value"),
		},
	}))

	t.Run("values", runValuesValue[uint, forms.NumberValue[uint]]([]valueTest[[]string, uint]{
		{
			data:  []string{"10.0"},
			flags: forms.IsBound,
			value: 10,
			str:   "10",
		},
		{
			data:  []string{"-5"},
			flags: forms.IsEmpty,
			err:   errors.New("invalid integer value"),
		},
		{
			data:  []string{"9.2"},
			flags: forms.IsEmpty,
			err:   errors.New("invalid integer value"),
		},
	}))
}

func TestNumberValueFloat(t *testing.T) {
	t.Run("json", runJSONValue[float64, forms.NumberValue[float64]]([]valueTest[string, float64]{
		{
			data:  "10",
			flags: forms.IsBound,
			value: 10,
			str:   "10",
		},
		{
			data:  "-5.2",
			flags: forms.IsBound,
			value: -5.2,
			str:   "-5.2",
		},
	}))

	t.Run("values", runValuesValue[float64, forms.NumberValue[float64]]([]valueTest[[]string, float64]{
		{
			data:  []string{"10.0"},
			flags: forms.IsBound,
			value: 10,
			str:   "10",
		},
		{
			data:  []string{"2.4367"},
			flags: forms.IsBound,
			value: 2.4367,
			str:   "2.4367",
		},
		{
			data:  []string{"abc"},
			flags: forms.IsEmpty,
			err:   errors.New("invalid syntax"),
		},
	}))
}

func TestDatetimeValue(t *testing.T) {
	t.Run("json", runJSONValue[time.Time, forms.DatetimeValue]([]valueTest[string, time.Time]{
		{
			data:  "null",
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  `"2026-03-17T13:12:24Z"`,
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "2026-03-17T13:12:24Z")),
			str:   "2026-03-17T13:12:24Z",
		},
		{
			data:  `"2026-03-17T13:12:24+01:00"`,
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "2026-03-17T13:12:24+01:00")),
			str:   "2026-03-17T13:12:24+01:00",
		},
		{
			data:  `"2026-03-17 13:12:24"`,
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "2026-03-17T13:12:24Z")),
			str:   "2026-03-17T13:12:24Z",
		},
		{
			data:  `"2026-03-17T13:12"`,
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "2026-03-17T13:12:00Z")),
			str:   "2026-03-17T13:12:00Z",
		},
		{
			data:  `"2026-03-17"`,
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "2026-03-17T00:00:00Z")),
			str:   "2026-03-17T00:00:00Z",
		},
		{
			data:  "3604",
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "1970-01-01T01:00:04Z")),
			str:   "1970-01-01T01:00:04Z",
		},
		{
			data:  `"abc"`,
			flags: forms.IsEmpty,
			err:   errors.New("cannot parse"),
		},
		{
			data:  `{}`,
			flags: forms.IsEmpty,
			err:   errors.New("cannot unmarshal"),
		},
	}))

	t.Run("values", runValuesValue[time.Time, forms.DatetimeValue]([]valueTest[[]string, time.Time]{
		{
			data:  []string{},
			flags: forms.IsBound | forms.IsEmpty,
		},
		{
			data:  []string{"\uff00"},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  []string{""},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  []string{"2026-03-17T13:12:24Z"},
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "2026-03-17T13:12:24Z")),
			str:   "2026-03-17T13:12:24Z",
		},
		{
			data:  []string{"2026-03-17T13:12"},
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "2026-03-17T13:12:00Z")),
			str:   "2026-03-17T13:12:00Z",
		},
		{
			data:  []string{"3604"},
			flags: forms.IsBound,
			value: must(time.Parse(time.RFC3339, "1970-01-01T01:00:04Z")),
			str:   "1970-01-01T01:00:04Z",
		},
	}))
}

func TestURLValue(t *testing.T) {
	t.Run("json", runJSONValue[url.URL, forms.URLValue]([]valueTest[string, url.URL]{
		{
			data:  "null",
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  `"\u0000"`,
			flags: forms.IsEmpty,
			err:   errors.New("invalid control character"),
		},
		{
			data:  `"https://example.org/abc/"`,
			flags: forms.IsBound,
			value: *must(url.Parse("https://example.org/abc/")),
			str:   "https://example.org/abc/",
		},
		{
			data:  `1234`,
			flags: forms.IsEmpty,
			err:   errors.New("cannot unmarshal"),
		},
	}))

	t.Run("values", runValuesValue[url.URL, forms.URLValue]([]valueTest[[]string, url.URL]{
		{
			data:  []string{},
			flags: forms.IsBound | forms.IsEmpty,
		},
		{
			data:  []string{""},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  []string{"\uff00"},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
		},
		{
			data:  []string{"\u0000"},
			flags: forms.IsEmpty,
			err:   errors.New("invalid control character"),
		},
		{
			data:  []string{"https://example.org/abc/?test=1#anchor"},
			flags: forms.IsBound,
			value: *must(url.Parse("https://example.org/abc/?test=1#anchor")),
			str:   "https://example.org/abc/?test=1#anchor",
		},
	}))
}

func TestStringListValue(t *testing.T) {
	t.Run("json", runJSONValue[[]string, forms.ListValue[string, forms.StringValue]]([]valueTest[string, []string]{
		{
			data:  "null",
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
			value: []string{},
		},
		{
			data:  `[]`,
			flags: forms.IsBound | forms.IsEmpty,
			value: []string{},
		},
		{
			data:  `["a", "b", "c"]`,
			flags: forms.IsBound,
			value: []string{"a", "b", "c"},
			str:   "a, b, c",
		},
		{
			data:  `["a", null, "b", "c"]`,
			flags: forms.IsBound,
			value: []string{"a", "b", "c"},
			str:   "a, b, c",
		},
		{
			data:  `[null]`,
			flags: forms.IsBound | forms.IsEmpty,
			value: []string{},
		},
		{
			data:  `[""]`,
			flags: forms.IsBound,
			value: []string{""},
		},
		{
			data:  `123`,
			flags: forms.IsEmpty,
			value: []string{},
			err:   errors.New("cannot unmarshal number"),
		},
		{
			data:  `["a", 1, 2, null]`,
			flags: forms.IsEmpty,
			value: []string{},
			err:   errors.New("cannot unmarshal number"),
		},
		{
			data:  `[1, 2, 3]`,
			flags: forms.IsEmpty,
			value: []string{},
			err:   errors.New("cannot unmarshal number"),
		},
	}))

	t.Run("values", runValuesValue[[]string, forms.ListValue[string, forms.StringValue]]([]valueTest[[]string, []string]{
		{
			data:  []string{},
			flags: forms.IsBound | forms.IsEmpty,
			value: []string{},
		},
		{
			data:  []string{"\uff00"},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
			value: []string{},
		},
		{
			data:  []string{"a"},
			flags: forms.IsBound,
			value: []string{"a"},
			str:   "a",
		},
		{
			data:  []string{"a", "b", "12"},
			flags: forms.IsBound,
			value: []string{"a", "b", "12"},
			str:   "a, b, 12",
		},
	}))
}

func TestIntegerListValue(t *testing.T) {
	t.Run("json", runJSONValue[[]int, forms.ListValue[int, forms.NumberValue[int]]]([]valueTest[string, []int]{
		{
			data:  "null",
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
			value: []int{},
		},
		{
			data:  `[]`,
			flags: forms.IsBound | forms.IsEmpty,
			value: []int{},
		},
		{
			data:  `[1, 2, 3]`,
			flags: forms.IsBound,
			value: []int{1, 2, 3},
			str:   "1, 2, 3",
		},
		{
			data:  `[1, null, 2, 3]`,
			flags: forms.IsBound,
			value: []int{1, 2, 3},
			str:   "1, 2, 3",
		},
		{
			data:  `[null]`,
			flags: forms.IsBound | forms.IsEmpty,
			value: []int{},
		},
		{
			data:  `123`,
			flags: forms.IsEmpty,
			value: []int{},
			err:   errors.New("cannot unmarshal number"),
		},
		{
			data:  `["a", 1, 2, null]`,
			flags: forms.IsEmpty,
			value: []int{},
			err:   errors.New("invalid number"),
		},
		{
			data:  `["a", "b", "c"]`,
			flags: forms.IsEmpty,
			value: []int{},
			err:   errors.New("invalid number"),
		},
	}))

	t.Run("values", runValuesValue[[]int, forms.ListValue[int, forms.NumberValue[int]]]([]valueTest[[]string, []int]{
		{
			data:  []string{},
			flags: forms.IsBound | forms.IsEmpty,
			value: []int{},
		},
		{
			data:  []string{"\uff00"},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
			value: []int{},
		},
		{
			data:  []string{"1", "2", "3"},
			flags: forms.IsBound,
			value: []int{1, 2, 3},
			str:   "1, 2, 3",
		},
		{
			data:  []string{"1", "\uff00", "2", "3"},
			flags: forms.IsBound,
			value: []int{1, 2, 3},
			str:   "1, 2, 3",
		},
		{
			data:  []string{"1", "2", "c"},
			flags: forms.IsEmpty,
			value: []int{},
			err:   errors.New("invalid syntax"),
		},
	}))
}
