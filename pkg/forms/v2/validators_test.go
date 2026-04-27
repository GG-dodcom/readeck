// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type fieldValidatorTest[T any] struct {
	validators []forms.Validator
	data       string
	expect     T
	errors     []error
}

func (test fieldValidatorTest[T]) assert(t *testing.T, f forms.TypedBinder[T]) {
	assert := assert.New(t)
	require := require.New(t)

	require.NoError(json.Unmarshal([]byte(test.data), f))
	valid := f.(forms.ValidatorProvider).IsValid()

	if len(test.errors) > 0 {
		assert.False(valid)
		require.Len(f.Errors(), len(test.errors))
		for i, e := range f.Errors() {
			require.EqualError(e, test.errors[i].Error())
		}
	} else if !assert.True(valid, "field is valid") {
		t.Logf("field errors: %s\n", f.Errors())
	}
	assert.Equal(test.expect, f.Value())
}

func runValidatorTests[T any](fn func() forms.TypedBinder[T], tests []fieldValidatorTest[T]) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				field := fn()
				if test.validators != nil {
					field.(forms.ValidatorsProvider).SetValidators(test.validators)
				}

				test.assert(t, field)
			})
		}
	}
}

func TestRequired(t *testing.T) {
	t.Run("string", runValidatorTests(newField[string, forms.TextField], []fieldValidatorTest[string]{
		{
			validators: []forms.Validator{forms.Required},
			data:       `null`,
			expect:     "",
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.Required},
			data:       `""`,
			expect:     "",
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.Trim, forms.Required},
			data:       `"  \t   \r\n"`,
			expect:     "",
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.Trim, forms.Required},
			data:       `"abc"`,
			expect:     "abc",
		},
	}))

	t.Run("int", runValidatorTests(newField[int, forms.IntegerField], []fieldValidatorTest[int]{
		{
			validators: []forms.Validator{forms.Required},
			data:       `null`,
			expect:     0,
			errors:     []error{forms.ErrRequired},
		},
	}))

	t.Run("bool", runValidatorTests(newField[bool, forms.BooleanField], []fieldValidatorTest[bool]{
		{
			validators: []forms.Validator{forms.Required},
			data:       `null`,
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.Required},
			data:       `true`,
			expect:     true,
		},
		{
			validators: []forms.Validator{forms.Required},
			data:       `false`,
		},
	}))

	t.Run("list", runValidatorTests(newField[[]string, forms.TextListField], []fieldValidatorTest[[]string]{
		{
			validators: []forms.Validator{forms.Required},
			data:       `null`,
			expect:     []string{},
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.Required},
			data:       `[]`,
			expect:     []string{},
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.Required},
			data:       `[""]`,
			expect:     []string{""},
		},
		{
			validators: []forms.Validator{forms.Trim, forms.Required},
			data:       `["   ", "\t  \n"]`,
			expect:     []string{"", ""},
		},
		{
			validators: []forms.Validator{forms.Trim, forms.DiscardEmpty, forms.Required},
			data:       `["   ", "\t  \n"]`,
			expect:     []string{},
			errors:     []error{forms.ErrRequired},
		},
	}))
}

func TestRequiredOrNil(t *testing.T) {
	t.Run("string", runValidatorTests(newField[string, forms.TextField], []fieldValidatorTest[string]{
		{
			validators: []forms.Validator{forms.RequiredOrNil},
			data:       `""`,
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.Trim, forms.RequiredOrNil},
			data:       `"  \t  \r\n"`,
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.RequiredOrNil},
			data:       `null`,
		},
	}))

	t.Run("bool", runValidatorTests(newField[bool, forms.BooleanField], []fieldValidatorTest[bool]{
		{
			validators: []forms.Validator{forms.RequiredOrNil},
			data:       `null`,
		},
		{
			validators: []forms.Validator{forms.RequiredOrNil},
			data:       `false`,
			expect:     false,
		},
	}))
}

func TestSequenceValidation(t *testing.T) {
	runValidatorTests(newField[string, forms.TextField], []fieldValidatorTest[string]{
		{
			validators: []forms.Validator{forms.Skip, forms.IsEmail},
			data:       `null`,
		},
		{
			validators: []forms.Validator{forms.Skip, forms.IsEmail},
			data:       `""`,
		},
		{
			validators: []forms.Validator{forms.Skip, forms.IsEmail},
			data:       `"alice@example.org"`,
			expect:     "alice@example.org",
		},
		{
			validators: []forms.Validator{forms.Skip, forms.IsEmail},
			data:       `"alice"`,
			expect:     "alice",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.Required, forms.Len(5), forms.Len(8)},
			data:       `""`,
			errors:     []error{forms.ErrRequired},
		},
		{
			validators: []forms.Validator{forms.Required, forms.Len(5), forms.Len(8)},
			data:       `"a"`,
			expect:     "a",
			errors: []error{
				errors.New("text must contain 5 characters"),
				errors.New("text must contain 8 characters"),
			},
		},
	})(t)
}

func TestEmailValidation(t *testing.T) {
	t.Run("string", runValidatorTests(newField[string, forms.TextField], []fieldValidatorTest[string]{
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `"test@example.org"`,
			expect:     "test@example.org",
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `"test@example@.org"`,
			expect:     "test@example@.org",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `"@test"`,
			expect:     "@test",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `"test@"`,
			expect:     "test@",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `"test @localhost"`,
			expect:     "test @localhost",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `"test@\nlocalhost"`,
			expect:     "test@\nlocalhost",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `"test@\u001Flocalhost"`,
			expect:     "test@\u001Flocalhost",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `"foo"`,
			expect:     "foo",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `""`,
			expect:     "",
			errors:     []error{forms.ErrInvalidEmail},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `null`,
			expect:     "",
		},
	}))

	t.Run("list", runValidatorTests(newField[[]string, forms.TextListField], []fieldValidatorTest[[]string]{
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `["test@example.net"]`,
			expect:     []string{"test@example.net"},
		},
		{
			validators: []forms.Validator{forms.IsEmail},
			data:       `["test@example.net", "foo"]`,
			expect:     []string{"test@example.net", "foo"},
			errors:     []error{forms.ErrInvalidEmail},
		},
	}))
}

func TestURLValidation(t *testing.T) {
	t.Run("string", runValidatorTests(newField[string, forms.TextField], []fieldValidatorTest[string]{
		{
			validators: []forms.Validator{forms.IsURL("http")},
			data:       `"http://example.net/"`,
			expect:     "http://example.net/",
		},
		{
			validators: []forms.Validator{forms.IsURL("http", "https")},
			data:       `"https://example.net/"`,
			expect:     "https://example.net/",
		},
		{
			validators: []forms.Validator{forms.IsURL()},
			data:       `"http://example.net/"`,
			expect:     "http://example.net/",
		},
		{
			validators: []forms.Validator{forms.IsURL("http")},
			data:       `"http://` + string(rune(0x7f)) + `example.net/"`,
			expect:     "http://" + string(rune(0x7f)) + "example.net/",
			errors:     []error{forms.ErrInvalidURL},
		},
		{
			validators: []forms.Validator{forms.IsURL("https")},
			data:       `"http://example.net/"`,
			expect:     "http://example.net/",
			errors:     []error{forms.ErrInvalidURL},
		},
		{
			validators: []forms.Validator{forms.IsURL("http")},
			data:       `"http://"`,
			expect:     "http://",
			errors:     []error{forms.ErrInvalidURL},
		},
		{
			validators: []forms.Validator{forms.IsURL()},
			data:       `"ftp://example.net/"`,
			expect:     "ftp://example.net/",
			errors:     []error{forms.ErrInvalidURL},
		},
	}))

	t.Run("url.URL", runValidatorTests(newField[url.URL, forms.URLField], []fieldValidatorTest[url.URL]{
		{
			validators: []forms.Validator{forms.IsURL("http")},
			data:       `"http://example.net/"`,
			expect:     *must(url.Parse("http://example.net/")),
		},
		{
			validators: []forms.Validator{forms.IsURL("http", "https")},
			data:       `"https://example.net/"`,
			expect:     *must(url.Parse("https://example.net/")),
		},
		{
			validators: []forms.Validator{forms.IsURL()},
			data:       `"http://example.net/"`,
			expect:     *must(url.Parse("http://example.net/")),
		},
		{
			validators: []forms.Validator{forms.IsURL("http")},
			data:       `"http://` + string(rune(0x7f)) + `example.net/"`,
			expect:     url.URL{},
			errors:     []error{forms.ErrInvalidValue},
		},
		{
			validators: []forms.Validator{forms.IsURL("https")},
			data:       `"http://example.net/"`,
			expect:     *must(url.Parse("http://example.net/")),
			errors:     []error{forms.ErrInvalidURL},
		},
		{
			validators: []forms.Validator{forms.IsURL("http")},
			data:       `"http://"`,
			expect:     *must(url.Parse("http://")),
			errors:     []error{forms.ErrInvalidURL},
		},
		{
			validators: []forms.Validator{forms.IsURL()},
			data:       `"ftp://example.net/"`,
			expect:     *must(url.Parse("ftp://example.net/")),
			errors:     []error{forms.ErrInvalidURL},
		},
	}))
}

func TestGteLte(t *testing.T) {
	t.Run("int", runValidatorTests(newField[int, forms.IntegerField], []fieldValidatorTest[int]{
		{
			validators: []forms.Validator{forms.Gte(10)},
			data:       "10",
			expect:     10,
		},
		{
			validators: []forms.Validator{forms.Gte(10)},
			data:       "15",
			expect:     15,
		},
		{
			validators: []forms.Validator{forms.Gte(10)},
			data:       "2",
			expect:     2,
			errors:     []error{errors.New("must be greater or equal than 10")},
		},
		{
			validators: []forms.Validator{forms.Lte(10)},
			data:       "10",
			expect:     10,
		},
		{
			validators: []forms.Validator{forms.Lte(10)},
			data:       "15",
			expect:     15,
			errors:     []error{errors.New("must be lower or equal than 10")},
		},
		{
			validators: []forms.Validator{forms.Lte(10)},
			data:       "2",
			expect:     2,
		},
	}))

	t.Run("float", runValidatorTests(newField[float64, forms.NumberField[float64]], []fieldValidatorTest[float64]{
		{
			validators: []forms.Validator{forms.Gte(10)},
			data:       "10.2",
			expect:     10.2,
		},
		{
			validators: []forms.Validator{forms.Gte(10)},
			data:       "15",
			expect:     15,
		},
		{
			validators: []forms.Validator{forms.Gte(10.5)},
			data:       "2.4",
			expect:     2.4,
			errors:     []error{errors.New("must be greater or equal than 10.5")},
		},
		{
			validators: []forms.Validator{forms.Lte(10)},
			data:       "10",
			expect:     10,
		},
		{
			validators: []forms.Validator{forms.Lte(10.5)},
			data:       "15.3",
			expect:     15.3,
			errors:     []error{errors.New("must be lower or equal than 10.5")},
		},
		{
			validators: []forms.Validator{forms.Lte(10)},
			data:       "2",
			expect:     2,
		},
	}))
}

func TestLenValidators(t *testing.T) {
	t.Run("maxLen", runValidatorTests(newField[string, forms.TextField], []fieldValidatorTest[string]{
		{
			validators: []forms.Validator{forms.MaxLen(10)},
			data:       `"abc"`,
			expect:     "abc",
		},
		{
			validators: []forms.Validator{forms.MaxLen(10)},
			data:       `"問掃玉光尤向入神間示"`,
			expect:     "問掃玉光尤向入神間示",
		},
		{
			validators: []forms.Validator{forms.MaxLen(10)},
			data:       `"abcdefghijk"`,
			expect:     "abcdefghijk",
			errors:     []error{errors.New("text must contain at most 10 characters")},
		},
	}))

	t.Run("minLen", runValidatorTests(newField[string, forms.TextField], []fieldValidatorTest[string]{
		{
			validators: []forms.Validator{forms.MinLen(10)},
			data:       `"abc"`,
			expect:     "abc",
			errors:     []error{errors.New("text must contain at least 10 characters")},
		},
		{
			validators: []forms.Validator{forms.MinLen(10)},
			data:       `"問掃玉光尤向入神間"`,
			expect:     "問掃玉光尤向入神間",
			errors:     []error{errors.New("text must contain at least 10 characters")},
		},
		{
			validators: []forms.Validator{forms.MinLen(10)},
			data:       `"abcdefghijk"`,
			expect:     "abcdefghijk",
		},
	}))

	t.Run("len", runValidatorTests(newField[string, forms.TextField], []fieldValidatorTest[string]{
		{
			validators: []forms.Validator{forms.Len(10)},
			data:       `"abc"`,
			expect:     "abc",
			errors:     []error{errors.New("text must contain 10 characters")},
		},
		{
			validators: []forms.Validator{forms.Len(10)},
			data:       `"問掃玉光尤向入神間示"`,
			expect:     "問掃玉光尤向入神間示",
		},
		{
			validators: []forms.Validator{forms.Len(10)},
			data:       `"abcdefghijk"`,
			expect:     "abcdefghijk",
			errors:     []error{errors.New("text must contain 10 characters")},
		},
	}))
}

func TestSplitLinesValidator(t *testing.T) {
	runValidatorTests(newField[[]string, forms.TextListField], []fieldValidatorTest[[]string]{
		{
			validators: []forms.Validator{forms.SplitLines},
			data:       `["abc\ndef", "ii", " jj \n gg"]`,
			expect:     []string{"abc", "def", "ii", "jj", "gg"},
		},
	})(t)
}

func TestChoicesValidator(t *testing.T) {
	textField := func() forms.TypedBinder[string] {
		f := new(forms.TextField)
		forms.Choices(f, forms.Choice("A", "a"), forms.Choice("B", "b"))
		return f
	}

	textListField := func() forms.TypedBinder[[]string] {
		f := new(forms.TextListField)
		forms.Choices(f, forms.Choice("A", "a"), forms.Choice("B", "b"))
		return f
	}

	intField := func() forms.TypedBinder[int] {
		f := new(forms.IntegerField)
		forms.Choices(f, forms.Choice("A", 1), forms.Choice("B", 2))
		return f
	}

	t.Run("string", runValidatorTests(textField, []fieldValidatorTest[string]{
		{
			data:   `"a"`,
			expect: "a",
		},
		{
			data:   `null`,
			expect: "",
		},
		{
			data:   `"x"`,
			expect: "x",
			errors: []error{
				errors.New(`x is not one of "a", "b"`),
			},
		},
	}))

	t.Run("int", runValidatorTests(intField, []fieldValidatorTest[int]{
		{
			data:   `2`,
			expect: 2,
		},
		{
			data:   `3`,
			expect: 3,
			errors: []error{
				errors.New(`3 is not one of "1", "2"`),
			},
		},
	}))

	t.Run("string list", runValidatorTests(textListField, []fieldValidatorTest[[]string]{
		{
			data:   `["a", "b"]`,
			expect: []string{"a", "b"},
		},
		{
			data:   `["a", "x"]`,
			expect: []string{"a", "x"},
			errors: []error{errors.New(`x is not one of "a", "b"`)},
		},
		{
			data:   `["a", "x", "y"]`,
			expect: []string{"a", "x", "y"},
			errors: []error{
				errors.New(`x is not one of "a", "b"`),
				errors.New(`y is not one of "a", "b"`),
			},
		},
	}))

	t.Run("choice list", func(t *testing.T) {
		f1 := new(forms.TextField)
		forms.Choices(f1, forms.Choice("A", "a"), forms.Choice("B", "b"))
		assert.Exactly(t, forms.ValueChoices[string]{
			forms.Choice("A", "a"),
			forms.Choice("B", "b"),
		}, f1.Choices())

		f2 := new(forms.TextListField)
		forms.Choices(f2, forms.Choice("A", "a"), forms.Choice("B", "b"))
		assert.Exactly(t, forms.ValueChoices[string]{
			forms.Choice("A", "a"),
			forms.Choice("B", "b"),
		}, f2.Choices())

		f3 := new(forms.IntegerField)
		forms.Choices(f3, forms.Choice("A", 1), forms.Choice("B", 2))
		assert.Exactly(t, forms.ValueChoices[int]{
			forms.Choice("A", 1),
			forms.Choice("B", 2),
		}, f3.Choices())
	})
}

func TestTaggedValidators(t *testing.T) {
	type testForm struct {
		forms.Form
		Required      forms.TextField     `json:"required"        validate:"required"`
		RequiredOrNil forms.TextField     `json:"required_or_nil" validate:"required_or_nil"`
		Trim          forms.TextField     `json:"trim"            validate:"trim"`
		Gte           forms.IntegerField  `json:"gte"             validate:"gte:10"`
		Lte           forms.IntegerField  `json:"lte"             validate:"lte:10"`
		MinLen        forms.TextField     `json:"min_len"         validate:"min_len:10"`
		MaxLen        forms.TextField     `json:"max_len"         validate:"max_len:10"`
		Len           forms.TextField     `json:"len"             validate:"len:10"`
		IsEmail       forms.TextField     `json:"is_email"        validate:"is_email"`
		IsURL         forms.TextField     `json:"is_url"          validate:"is_url"`
		SplitLines    forms.TextListField `json:"split_lines"     validate:"split_lines"`
	}

	tests := []struct {
		name   string
		data   url.Values
		assert func(t *testing.T, f *testForm)
	}{
		{
			name: "required",
			data: url.Values{},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.Required.Errors(), "field is required")
			},
		},
		{
			name: "required_or_nil",
			data: url.Values{"required_or_nil": {""}},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.RequiredOrNil.Errors(), "field is required")
			},
		},
		{
			name: "trim",
			data: url.Values{"trim": {" abc   "}},
			assert: func(t *testing.T, f *testForm) {
				assert.Empty(t, f.Trim.Errors())
				assert.Equal(t, "abc", f.Trim.Value())
			},
		},
		{
			name: "gte",
			data: url.Values{"gte": {"5"}},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.Gte.Errors(), "must be greater or equal than 10")
			},
		},
		{
			name: "lte",
			data: url.Values{"lte": {"15"}},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.Lte.Errors(), "must be lower or equal than 10")
			},
		},
		{
			name: "min_len",
			data: url.Values{"min_len": {"abc"}},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.MinLen.Errors(), "text must contain at least 10 characters")
			},
		},
		{
			name: "max_len",
			data: url.Values{"max_len": {"abcdefghijkl"}},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.MaxLen.Errors(), "text must contain at most 10 characters")
			},
		},
		{
			name: "len",
			data: url.Values{"len": {"abc"}},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.Len.Errors(), "text must contain 10 characters")
			},
		},
		{
			name: "is_email",
			data: url.Values{"is_email": {"abcd"}},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.IsEmail.Errors(), "not a valid email address")
			},
		},
		{
			name: "is_url",
			data: url.Values{"is_url": {"http://example.org/"}},
			assert: func(t *testing.T, f *testForm) {
				assert.Empty(t, f.IsURL.Errors())
			},
		},
		{
			name: "is_url:ftp",
			data: url.Values{"is_url": {"ftp://example.org/"}},
			assert: func(t *testing.T, f *testForm) {
				assert.ErrorContains(t, f.IsURL.Errors(), "invalid URL")
			},
		},
		{
			name: "split_lines",
			data: url.Values{"split_lines": {"abc\ndef", "xyz"}},
			assert: func(t *testing.T, f *testForm) {
				assert.Empty(t, f.SplitLines.Errors())
				assert.Equal(t, []string{"abc", "def", "xyz"}, f.SplitLines.Value())
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodPost, "/", strings.NewReader(test.data.Encode()))
			require.NoError(t, err)
			r.Header.Set("content-type", string(forms.MimeURLEncoded))

			f := forms.BindAs[testForm](r)
			f.IsValid()
			require.Empty(t, f.Errors())
			test.assert(t, f)
		})
	}
}
