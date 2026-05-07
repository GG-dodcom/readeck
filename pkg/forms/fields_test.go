// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms_test

import (
	"encoding/json"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/pkg/forms"
)

func newField[T any, F forms.TypedBinder[T]]() forms.TypedBinder[T] {
	f := new(F)
	return any(f).(forms.TypedBinder[T])
}

type fieldTest[D any, T any] struct {
	data  D
	value T
	str   string
	empty bool
	nil   bool
	err   error
}

func (test fieldTest[D, T]) assert(t *testing.T, field forms.TypedBinder[T], err error) {
	require := require.New(t)
	assert := assert.New(t)
	t.Log(string(must(json.Marshal(field))))

	if test.err == nil {
		require.NoError(err, "no error")
		assert.True(field.IsBound(), "field is bound")
		assert.Empty(field.Errors(), "no field error")
	} else {
		assert.Len(field.Errors(), 1, "only one error")
		assert.False(field.IsBound(), "field is not bound")
		require.ErrorIs(field.Errors()[0], test.err, "field error")
	}

	assert.Equal(test.empty, field.IsEmpty(), "empty")
	assert.Equal(test.nil, field.IsNil(), "nil")
	assert.Exactly(test.value, field.Value(), "typed value")
	assert.Exactly(test.str, field.String(), "string")
}

func runJSONField[T any, F forms.TypedBinder[T]](fn func() forms.TypedBinder[T], tests []fieldTest[string, T]) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				var field forms.TypedBinder[T]
				if fn == nil {
					field = newField[T, F]()
				} else {
					field = fn()
				}

				err := any(field).(json.Unmarshaler).UnmarshalJSON([]byte(test.data))
				test.assert(t, field, err)
			})
		}
	}
}

func runValuesField[T any, F forms.TypedBinder[T]](fn func() forms.TypedBinder[T], tests []fieldTest[[]string, T]) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				var field forms.TypedBinder[T]
				if fn == nil {
					field = newField[T, F]()
				} else {
					field = fn()
				}

				err := any(field).(forms.ValuesUnmarshaler).UnmarshalValues(test.data)
				test.assert(t, field, err)
			})
		}
	}
}

func TestFieldAttributes(t *testing.T) {
	f := new(forms.TextField)
	fl := new(forms.TextListField)

	t.Run("set nil", func(t *testing.T) {
		assert.Empty(t, f.Value())
		assert.False(t, f.IsNil())

		f.Set("abc")
		assert.Equal(t, "abc", f.Value())

		f.SetNil()
		assert.Equal(t, "abc", f.Value())
		assert.True(t, f.IsNil())
	})

	// t.Run("translator", func(t *testing.T) {
	// 	assert.Nil(t, f.Translator())
	// })

	t.Run("isValid", func(t *testing.T) {
		assert.True(t, f.IsValid())
		assert.True(t, f.IsValid())

		assert.True(t, fl.IsValid())
		assert.True(t, fl.IsValid())
	})
}

func TestTextField(t *testing.T) {
	t.Run("json", runJSONField[string, forms.TextField](nil, []fieldTest[string, string]{
		{
			data:  `null`,
			empty: true,
			nil:   true,
		},
		{
			data:  `""`,
			empty: true,
		},
		{
			data:  `"test"`,
			value: "test",
			str:   "test",
		},
		{
			data: "//",
			err:  forms.ErrInvalidValue,
		},
		{
			data:  "1234",
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))

	t.Run("values", runValuesField[string, forms.TextField](nil, []fieldTest[[]string, string]{
		{
			data:  []string{""},
			value: "",
			str:   "",
			empty: true,
		},
		{
			data:  []string{"\uff00"},
			value: "",
			str:   "",
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"abc"},
			value: "abc",
			str:   "abc",
		},
		{
			data:  []string{"foo"},
			value: "foo",
			str:   "foo",
		},
		{
			data:  []string{"bar", "foo"},
			value: "bar",
			str:   "bar",
		},
	}))

	fieldTrim := func() forms.TypedBinder[string] {
		f := newField[string, forms.TextField]()
		f.(forms.ValidatorsProvider).SetValidators([]forms.Validator{forms.Trim})
		return f
	}

	t.Run("json trimmed", runJSONField[string, forms.TextField](fieldTrim, []fieldTest[string, string]{
		{
			data:  `"   \t\n "`,
			empty: true,
		},
		{
			data:  `"  \tabc \n\r  "`,
			value: "abc",
			str:   "abc",
		},
		{
			data:  "null",
			empty: true,
			nil:   true,
		},
	}))

	t.Run("values trimmed", runValuesField[string, forms.TextField](fieldTrim, []fieldTest[[]string, string]{
		{
			data:  []string{"\uff00"},
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"\t   \r\n "},
			empty: true,
		},
		{
			data:  []string{"  abc  \n\t "},
			value: "abc",
			str:   "abc",
		},
	}))
}

func TestBooleanField(t *testing.T) {
	t.Run("json", runJSONField[bool, forms.BooleanField](nil, []fieldTest[string, bool]{
		{
			data:  "true",
			value: true,
			str:   "true",
		},
		{
			data:  "false",
			value: false,
			str:   "false",
		},
		{
			data:  "null",
			value: false,
			empty: true,
			nil:   true,
		},
		{
			data:  "1234",
			value: false,
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))

	t.Run("values", runValuesField[bool, forms.BooleanField](nil, []fieldTest[[]string, bool]{
		{
			data:  []string{""},
			value: false,
			str:   "false",
			empty: true,
		},
		{
			data:  []string{"\uff00"},
			value: false,
			str:   "",
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"on"},
			value: true,
			str:   "true",
		},
		{
			data:  []string{"f"},
			value: false,
			str:   "false",
		},
		{
			data:  []string{"t"},
			value: true,
			str:   "true",
		},
		{
			data:  []string{"t", "f"},
			value: true,
			str:   "true",
		},
		{
			data:  []string{"0"},
			value: false,
			str:   "false",
		},
		{
			data:  []string{"1"},
			value: true,
			str:   "true",
		},
		{
			data:  []string{"whatever"},
			value: false,
			str:   "",
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))
}

func TestIntegerField(t *testing.T) {
	t.Run("json", runJSONField[int, forms.IntegerField](nil, []fieldTest[string, int]{
		{
			data:  "null",
			value: 0,
			empty: true,
			nil:   true,
		},
		{
			data:  "10",
			value: 10,
			str:   "10",
		},
		{
			data:  "-5",
			value: -5,
			str:   "-5",
		},
		{
			data:  `102.5`,
			value: 0,
			empty: true,
			err:   forms.ErrInvalidValue,
		},
		{
			data:  `"abcd"`,
			value: 0,
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))

	t.Run("values", runValuesField[int, forms.IntegerField](nil, []fieldTest[[]string, int]{
		{
			data:  []string{"\uff00"},
			value: 0,
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"10"},
			value: 10,
			str:   "10",
		},
		{
			data:  []string{"-5"},
			value: -5,
			str:   "-5",
		},
		{
			data:  []string{"102.5"},
			value: 0,
			empty: true,
			err:   forms.ErrInvalidValue,
		},
		{
			data:  []string{"whatever"},
			value: 0,
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))
}

func TestDatetimeField(t *testing.T) {
	d1 := must(time.Parse(time.DateOnly, "2020-01-30"))
	d2 := must(time.Parse(time.DateTime, "2020-01-30 14:24:06"))

	t.Run("json", runJSONField[time.Time, forms.DatetimeField](nil, []fieldTest[string, time.Time]{
		{
			data:  `""`,
			empty: true,
			err:   forms.ErrInvalidValue,
		},
		{
			data:  `"2020-01-30"`,
			value: d1,
			str:   "2020-01-30T00:00:00Z",
		},
		{
			data:  `"2020-01-30 14:24:06"`,
			value: d2,
			str:   "2020-01-30T14:24:06Z",
		},
		{
			data:  "null",
			empty: true,
			nil:   true,
		},
		{
			data:  `"blaaa"`,
			empty: true,
			err:   forms.ErrInvalidValue,
		},
		{
			data:  "3600",
			value: must(time.Parse(time.RFC3339, "1970-01-01T01:00:00Z")),
			str:   "1970-01-01T01:00:00Z",
		},
	}))

	t.Run("values", runValuesField[time.Time, forms.DatetimeField](nil, []fieldTest[[]string, time.Time]{
		{
			data:  []string{""},
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"2020-01-30"},
			value: d1,
			str:   "2020-01-30T00:00:00Z",
		},
		{
			data:  []string{"2020-01-30 14:24:06"},
			value: d2,
			str:   "2020-01-30T14:24:06Z",
		},
		{
			data:  []string{"\uff00"},
			value: time.Time{},
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"blaaa"},
			value: time.Time{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))
}

func TestURLField(t *testing.T) {
	t.Run("json", runJSONField[url.URL, forms.URLField](nil, []fieldTest[string, url.URL]{
		{
			data:  "null",
			value: url.URL{},
			empty: true,
			nil:   true,
		},
		{
			data:  `"http://example.org/"`,
			value: *must(url.Parse("http://example.org/")),
			str:   "http://example.org/",
		},
		{
			data:  `"\u0000"`,
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))

	t.Run("values", runValuesField[url.URL, forms.URLField](nil, []fieldTest[[]string, url.URL]{
		{
			data:  []string{""},
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"\uff00"},
			value: url.URL{},
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"http://example.org/"},
			value: *must(url.Parse("http://example.org/")),
			str:   "http://example.org/",
		},
		{
			data:  []string{"\u0000"},
			value: url.URL{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))
}

func TestTextListField(t *testing.T) {
	t.Run("json", runJSONField[[]string, forms.TextListField](nil, []fieldTest[string, []string]{
		{
			data:  "null",
			value: []string{},
			empty: true,
			nil:   true,
		},
		{
			data: "//",
			err:  forms.ErrInvalidValue,
		},
		{
			data:  `[]`,
			value: []string{},
			empty: true,
		},
		{
			data:  `["a", "b", "c"]`,
			value: []string{"a", "b", "c"},
			str:   "a, b, c",
		},
		{
			data:  `["a", null, "b", "c"]`,
			value: []string{"a", "b", "c"},
			str:   "a, b, c",
		},
		{
			data:  `[null]`,
			value: []string{},
			empty: true,
		},
		{
			data:  `[""]`,
			value: []string{""},
		},
		{
			data:  `123`,
			value: []string{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
		{
			data:  `["a", 1, 2, null]`,
			value: []string{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
		{
			data:  `[1, 2, 3]`,
			value: []string{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))

	t.Run("values", runValuesField[[]string, forms.TextListField](nil, []fieldTest[[]string, []string]{
		{
			data:  []string{},
			value: []string{},
			empty: true,
		},
		{
			data:  []string{"\uff00"},
			value: []string{},
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"a"},
			value: []string{"a"},
			str:   "a",
		},
		{
			data:  []string{"a", "b", "12"},
			value: []string{"a", "b", "12"},
			str:   "a, b, 12",
		},
	}))

	fieldTrim := func() forms.TypedBinder[[]string] {
		f := newField[[]string, forms.TextListField]()
		f.(forms.ValidatorsProvider).SetValidators([]forms.Validator{forms.Trim})
		return f
	}

	t.Run("json trimmed", runJSONField[[]string, forms.TextListField](fieldTrim, []fieldTest[string, []string]{
		{
			data:  `[]`,
			value: []string{},
			empty: true,
		},
		{
			data:  `["\ta  ", "  b\r\n", "c"]`,
			value: []string{"a", "b", "c"},
			str:   "a, b, c",
		},
		{
			data:  `[1, 2, 3]`,
			value: []string{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))

	t.Run("values trimmed", runValuesField[[]string, forms.TextListField](fieldTrim, []fieldTest[[]string, []string]{
		{
			data:  []string{},
			value: []string{},
			empty: true,
		},
		{
			data:  []string{"\uff00"},
			value: []string{},
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"\ta  ", "   b\r\n", "12"},
			value: []string{"a", "b", "12"},
			str:   "a, b, 12",
		},
	}))

	fieldDiscardEmpty := func() forms.TypedBinder[[]string] {
		f := newField[[]string, forms.TextListField]()
		f.(forms.ValidatorsProvider).SetValidators([]forms.Validator{forms.DiscardEmpty})
		return f
	}

	t.Run("json discard empty", runJSONField[[]string, forms.TextListField](fieldDiscardEmpty, []fieldTest[string, []string]{
		{
			data:  `[""]`,
			value: []string{},
			empty: true,
		},
		{
			data:  `["", "abc", ""]`,
			value: []string{"abc"},
			str:   "abc",
			empty: false,
		},
	}))

	t.Run("values discard empty", runValuesField[[]string, forms.TextListField](fieldDiscardEmpty, []fieldTest[[]string, []string]{
		{
			data:  []string{""},
			value: []string{},
			empty: true,
		},
		{
			data:  []string{"", "abc", ""},
			value: []string{"abc"},
			str:   "abc",
			empty: false,
		},
	}))
}

func TestIntegerListField(t *testing.T) {
	t.Run("json", runJSONField[[]int, forms.IntegerListField](nil, []fieldTest[string, []int]{
		{
			data:  "null",
			value: []int{},
			empty: true,
			nil:   true,
		},
		{
			data:  "//",
			value: []int(nil),
			err:   forms.ErrInvalidValue,
		},
		{
			data:  `[]`,
			value: []int{},
			empty: true,
		},
		{
			data:  `[1, 2, 3]`,
			value: []int{1, 2, 3},
			str:   "1, 2, 3",
		},
		{
			data:  `[1, null, 2, 3]`,
			value: []int{1, 2, 3},
			str:   "1, 2, 3",
		},
		{
			data:  `[null]`,
			value: []int{},
			empty: true,
		},
		{
			data:  `123`,
			value: []int{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
		{
			data:  `["a", 1, 2, null]`,
			value: []int{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
		{
			data:  `["a", "b", "c"]`,
			value: []int{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))

	t.Run("values", runValuesField[[]int, forms.IntegerListField](nil, []fieldTest[[]string, []int]{
		{
			data:  []string{},
			value: []int{},
			empty: true,
		},
		{
			data:  []string{"\uff00"},
			value: []int{},
			empty: true,
			nil:   true,
		},
		{
			data:  []string{"1", "2", "3"},
			value: []int{1, 2, 3},
			str:   "1, 2, 3",
		},
		{
			data:  []string{"1", "\uff00", "2", "3"},
			value: []int{1, 2, 3},
			str:   "1, 2, 3",
		},
		{
			data:  []string{"1", "2", "c"},
			value: []int{},
			empty: true,
			err:   forms.ErrInvalidValue,
		},
	}))
}

func TestURLListField(t *testing.T) {
	t.Run("json", runJSONField[[]url.URL, forms.URLListField](nil, []fieldTest[string, []url.URL]{
		{
			data:  "null",
			value: []url.URL{},
			empty: true,
			nil:   true,
		},
		{
			data: `["http://example.org/", "https://example.net"]`,
			value: []url.URL{
				*must(url.Parse("http://example.org/")),
				*must(url.Parse("https://example.net")),
			},
			str: "http://example.org/, https://example.net",
		},
	}))

	t.Run("values trimmed", runValuesField[[]url.URL, forms.URLListField](func() forms.TypedBinder[[]url.URL] {
		f := newField[[]url.URL, forms.URLListField]()
		f.(forms.ValidatorsProvider).SetValidators([]forms.Validator{forms.Trim})
		return f
	}, []fieldTest[[]string, []url.URL]{
		{
			data:  []string{"\uff00"},
			value: []url.URL(nil),
			empty: true,
		},
		{
			data: []string{"  http://example.org/", "https://example.net\n"},
			value: []url.URL{
				*must(url.Parse("http://example.org/")),
				*must(url.Parse("https://example.net")),
			},
			str: "http://example.org/, https://example.net",
		},
	}))
}
