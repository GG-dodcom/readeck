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

	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestUnmarshalValues(t *testing.T) {
	tests := []struct {
		values   []string
		dest     any
		expected any
	}{
		{
			[]string{},
			new(string),
			new(string),
		},
		{
			[]string{"abc"},
			new(string),
			new("abc"),
		},
		{
			[]string{"abc", "def"},
			new([]string),
			new([]string{"abc", "def"}),
		},
		{
			[]string{"0", "t", "F"},
			new([]bool),
			new([]bool{false, true, false}),
		},
		{
			[]string{"-123"},
			new(int),
			new(-123),
		},
		{
			[]string{"123"},
			new(uint),
			new(uint(123)),
		},
		{
			[]string{"1.23"},
			new(float32),
			new(float32(1.23)),
		},
		{
			[]string{"1.23"},
			new(float64),
			new(float64(1.23)),
		},
		{
			[]string{"2026-03-13T18:29:10Z"},
			new(time.Time),
			new(must(time.Parse(time.RFC3339, "2026-03-13T18:29:10Z"))),
		},
		{
			[]string{"https://example.org/test"},
			new(url.URL),
			must(url.Parse("https://example.org/test")),
		},
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			require.NoError(t, forms.UnmarshalValues(test.values, test.dest))
			assert.Exactly(t, test.expected, test.dest)
		})
	}
}

func TestUnmarshalURLValues(t *testing.T) {
	type ValuesSimple struct {
		Name  string `json:"name"`
		Count int32
	}

	type ValuesMeta struct {
		Date    time.Time `json:"date"`
		Website url.URL   `json:"website"`
		Truc    string    `json:"-"`
	}
	type ValuesNested struct {
		Name  string `json:"name"`
		Count uint
		Meta  ValuesMeta `json:"meta"`
	}

	type ValuesCombined struct {
		ValuesSimple
		ValuesMeta
	}

	type ValuesCombinedNested struct {
		ValuesSimple
		Meta  ValuesMeta `json:"meta"`
		Extra []string   `json:"extra"`
	}

	tests := []struct {
		values   url.Values
		dest     any
		expected any
	}{
		{
			url.Values{
				"name":  {"test"},
				"Count": {"10"},
			},
			new(ValuesSimple),
			&ValuesSimple{
				Name:  "test",
				Count: 10,
			},
		},
		{
			url.Values{
				"name":  {"test", "abc"},
				"Count": {"10", "abc"},
			},
			new(ValuesSimple),
			&ValuesSimple{
				Name:  "test",
				Count: 10,
			},
		},
		{
			url.Values{
				"name":         {"test"},
				"Count":        {"25"},
				"meta.date":    {"2026-03-13T18:29:10Z"},
				"meta.website": {"https://example.org/"},
			},
			new(ValuesNested),
			&ValuesNested{
				Name:  "test",
				Count: 25,
				Meta: ValuesMeta{
					Date:    must(time.Parse(time.RFC3339, "2026-03-13T18:29:10Z")),
					Website: *must(url.Parse("https://example.org/")),
				},
			},
		},
		{
			url.Values{
				"name":         {"test"},
				"Count":        {"25"},
				"meta.unknown": {"2026-03-13T18:29:10Z"},
				"meta.":        {"https://example.org/"},
			},
			new(ValuesNested),
			&ValuesNested{
				Name:  "test",
				Count: 25,
				Meta: ValuesMeta{
					Date:    time.Time{},
					Website: url.URL{},
				},
			},
		},
		{
			url.Values{
				"name":         {"test"},
				"Count":        {"10"},
				"meta.date":    {"2026-03-21T17:07:00Z"},
				"meta.website": {"https://example.org/"},
				"extra":        {"a", "b"},
			},
			new(ValuesCombinedNested),
			&ValuesCombinedNested{
				ValuesSimple: ValuesSimple{
					Name:  "test",
					Count: 10,
				},
				Meta: ValuesMeta{
					Date:    must(time.Parse(time.RFC3339, "2026-03-21T17:07:00Z")),
					Website: *must(url.Parse("https://example.org/")),
				},
				Extra: []string{"a", "b"},
			},
		},
		{
			url.Values{
				"name":    {"test"},
				"Count":   {"25"},
				"date":    {"2026-03-13T18:29:10Z"},
				"website": {"https://example.org/"},
			},
			new(ValuesCombined),
			&ValuesCombined{
				ValuesSimple: ValuesSimple{
					Name:  "test",
					Count: 25,
				},
				ValuesMeta: ValuesMeta{
					Date:    must(time.Parse(time.RFC3339, "2026-03-13T18:29:10Z")),
					Website: *must(url.Parse("https://example.org/")),
				},
			},
		},
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			require.NoError(t, forms.UnmarshalURLValues(test.values, test.dest))
			t.Logf("%s", must(json.MarshalIndent(test.dest, "", "  ")))
			require.Exactly(t, test.expected, test.dest)
		})
	}
}
