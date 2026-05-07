// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/pkg/forms"
)

type testForm struct {
	forms.Form
	Bool forms.BooleanField  `json:"bool"`
	Text forms.TextField     `json:"text"`
	Int  forms.IntegerField  `json:"int"`
	Time forms.DatetimeField `json:"time"`
}

func newTestFormDefaults() *testForm {
	form := forms.New[testForm](context.Background())
	form.Bool.Set(true)
	form.Text.Set("abc")
	form.Int.Set(123)
	form.Time.Set(must(time.Parse(time.DateTime, "2024-01-04 12:35:02")))

	return form
}

type customValidationForm struct {
	forms.Form
	Name forms.TextField
	City customValidationField
}

func (f *customValidationForm) Validate() error {
	if f.Name.Value() == "bob" {
		f.Name.AddErrors(forms.Gettext("forbidden name"))
		return forms.Gettext("form error")
	}
	return nil
}

type customValidationField struct {
	forms.TextField
}

func (f *customValidationField) Validate() error {
	if f.Value() == "paris" {
		return forms.Gettext("invalid city")
	}
	return nil
}

type customUnmarshalForm struct {
	forms.Form
	unmarshaledBy string
}

func (f *customUnmarshalForm) UnmarshalValues(url.Values) error {
	f.unmarshaledBy = "values"
	return nil
}

func (f *customUnmarshalForm) UnmarshalJSON([]byte) error {
	f.unmarshaledBy = "json"
	return nil
}

type testResult string

func (result testResult) assert(t *testing.T, f forms.FormBinder) {
	assert.True(t, f.IsBound())
	data, err := json.MarshalIndent(f, "", "  ")
	require.NoError(t, err)

	if !assert.JSONEq(t, string(result), string(data)) {
		t.Logf("GOT:\n%s", data)
	}
}

type formTest struct {
	data   string
	result testResult
}

type multipartTest struct {
	data   func(*multipart.Writer) error
	result testResult
}

func runRequestForm(
	contentType forms.MimeType,
	constructor func() forms.FormBinder,
	tests []formTest) func(t *testing.T,
) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				r, err := http.NewRequest(http.MethodPost, "/", strings.NewReader(test.data))
				require.NoError(t, err)
				r.Header.Set("content-type", string(contentType))

				form := constructor()
				assert.False(t, form.IsBound())
				forms.Bind(r, form)
				test.result.assert(t, form)
			})
		}
	}
}

func runMultipartForm(
	constructor func() forms.FormBinder,
	tests []multipartTest,
) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				body := new(bytes.Buffer)
				mp := multipart.NewWriter(body)
				require.NoError(t, test.data(mp))
				require.NoError(t, mp.Close())

				r, err := http.NewRequest(http.MethodPost, "/", body)
				require.NoError(t, err)
				r.Header.Set("content-type", mp.FormDataContentType())

				form := constructor()
				assert.False(t, form.IsBound())
				forms.Bind(r, form)
				test.result.assert(t, form)
			})
		}
	}
}

func TestNewForm(t *testing.T) {
	type PanicForm int

	type NonBinder struct {
		A forms.TextField
	}

	type OkForm struct {
		forms.Form
		A forms.TextField `json:"a"`
	}

	assert.Panics(t, func() {
		forms.New[PanicForm](context.Background())
	})

	assert.Panics(t, func() {
		forms.New[NonBinder](context.Background())
	})

	f := forms.New[OkForm](context.Background())

	assert.Equal(t,
		map[string]forms.Binder{
			"a": &f.A,
		},
		f.Fields(),
	)

	assert.Equal(t, "a", f.A.Name())
}

func TestFormValues(t *testing.T) {
	type FormX struct {
		C forms.TextField
	}

	type SimpleForm struct {
		forms.Form
		FormX
		A forms.TextField `json:"a"`
		B forms.IntegerField
	}

	type NestedForm struct {
		SimpleForm
		D struct {
			S forms.TextField `json:"s"`
		} `json:"nested"`
	}

	tests := []struct {
		fn       func() forms.FormBinder
		expected map[string]any
	}{
		{
			func() forms.FormBinder {
				f := forms.New[SimpleForm](context.Background())
				f.A.Set("a value")
				f.B.Set(10)
				f.C.Set("c value")
				return f
			},
			map[string]any{
				"B": 10,
				"C": "c value",
				"a": "a value",
			},
		},
		{
			func() forms.FormBinder {
				f := forms.New[NestedForm](context.Background())
				f.A.Set("a value")
				f.D.S.Set("s value")
				return f
			},
			map[string]any{
				"B": 0,
				"C": "",
				"a": "a value",
				"nested": map[string]any{
					"s": "s value",
				},
			},
		},
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			f := test.fn()
			assert.Equal(t, test.expected, forms.MarshalValues(f))
		})
	}
}

func TestValuePriority(t *testing.T) {
	tests := []struct {
		method string
		url    string
		body   url.Values
		expect any
	}{
		{
			http.MethodGet,
			"/?text=abc",
			nil,
			"abc",
		},
		{
			http.MethodGet,
			"/?text=abc",
			url.Values{},
			"abc",
		},
		{
			http.MethodPost,
			"/?text=abc",
			url.Values{"text": {"xyz"}},
			"xyz",
		},
		{
			http.MethodPost,
			"/",
			url.Values{"text": {"xyz"}},
			"xyz",
		},
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			body := new(bytes.Buffer)
			if test.body != nil {
				body.WriteString(test.body.Encode())
			}
			r, _ := http.NewRequest(test.method, test.url, body)
			r.Header.Set("Content-Type", string(forms.MimeURLEncoded))

			f := forms.BindAs[testForm](r)

			assert.True(t, f.IsValid())
			assert.Equal(t, test.expect, f.Text.Value())
		})
	}
}

func TestBindValues(t *testing.T) {
	tests := []struct {
		url    string
		expect any
	}{
		{
			"/?text=abc",
			"abc",
		},
		{
			"/",
			"",
		},
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, test.url, nil)

			f := forms.BindAs[testForm](r)

			assert.True(t, f.IsValid())
			assert.Equal(t, test.expect, f.Text.Value())
		})
	}
}

func TestSimpleForm(t *testing.T) {
	type SimpleForm struct {
		forms.Form
		Name forms.TextField    `json:"name"`
		ID   forms.IntegerField `json:"id"   validate:"required"`
	}

	t.Run("json", runRequestForm(forms.MimeJSON, func() forms.FormBinder {
		return forms.New[SimpleForm](context.Background())
	}, []formTest{
		{
			`{"name": "test", "id": 2}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"id": {
						"is_null": false,
						"is_bound": true,
						"value": 2,
						"errors": null
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "test",
						"errors": null
					}
				}
			}`,
		},
		{
			"",
			`{
				"is_valid": false,
				"errors": [
					"invalid input data"
				],
				"fields": {
					"id": {
						"is_null": false,
						"is_bound": false,
						"value": 0,
						"errors": ["field is required"]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					}
				}
			}`,
		},
		{
			`{"name": 123}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"id": {
						"is_null": false,
						"is_bound": false,
						"value": 0,
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"invalid value"
						]
					}
				}
			}`,
		},
	}))

	t.Run("values", runRequestForm(forms.MimeURLEncoded, func() forms.FormBinder {
		return forms.New[SimpleForm](context.Background())
	}, []formTest{
		{
			`name=test&name=alice&id=2`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"id": {
						"is_null": false,
						"is_bound": true,
						"value": 2,
						"errors": null
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "test",
						"errors": null
					}
				}
			}`,
		},
		{
			"",
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"id": {
						"is_null": false,
						"is_bound": false,
						"value": 0,
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					}
				}
			}`,
		},
		{
			`name=123`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"id": {
						"is_null": false,
						"is_bound": false,
						"value": 0,
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "123",
						"errors": null
					}
				}
			}`,
		},
	}))

	t.Run("multipart values", runMultipartForm(func() forms.FormBinder {
		return forms.New[SimpleForm](context.Background())
	}, []multipartTest{
		{
			func(_ *multipart.Writer) error { return nil },
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"id": {
						"is_null": false,
						"is_bound": false,
						"value": 0,
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					}
				}
			}`,
		},
		{
			func(mp *multipart.Writer) error {
				_ = mp.WriteField("name", "alice")
				_ = mp.WriteField("name", "test")
				_ = mp.WriteField("id", "2")
				return nil
			},
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"id": {
						"is_null": false,
						"is_bound": true,
						"value": 2,
						"errors": null
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "alice",
						"errors": null
					}
				}
			}`,
		},
	}))
}

func TestDefaultValues(t *testing.T) {
	t.Run("json", runRequestForm(forms.MimeJSON, func() forms.FormBinder {
		return newTestFormDefaults()
	}, []formTest{
		{
			`{}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"bool": {
						"is_null": false,
						"is_bound": false,
						"value": true,
						"errors": null
					},
					"int": {
						"is_null": false,
						"is_bound": false,
						"value": 123,
						"errors": null
					},
					"text": {
						"is_null": false,
						"is_bound": false,
						"value": "abc",
						"errors": null
					},
					"time": {
						"is_null": false,
						"is_bound": false,
						"value": "2024-01-04T12:35:02Z",
						"errors": null
					}
				}
			}`,
		},
		{
			`{
					"bool": false,
					"int": 5,
					"text": "xyz",
					"time": "2024-02-05 11:23:45"
				}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"bool": {
						"is_null": false,
						"is_bound": true,
						"value": false,
						"errors": null
					},
					"int": {
						"is_null": false,
						"is_bound": true,
						"value": 5,
						"errors": null
					},
					"text": {
						"is_null": false,
						"is_bound": true,
						"value": "xyz",
						"errors": null
					},
					"time": {
						"is_null": false,
						"is_bound": true,
						"value": "2024-02-05T11:23:45Z",
						"errors": null
					}
				}
			}`,
		},
		{
			`{
					"bool": 12,
					"int": true,
					"text": 55,
					"time": "abc"
				}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"bool": {
						"is_null": false,
						"is_bound": false,
						"value": true,
						"errors": [
							"invalid value"
						]
					},
					"int": {
						"is_null": false,
						"is_bound": false,
						"value":123,
						"errors": [
							"invalid value"
						]
					},
					"text": {
						"is_null": false,
						"is_bound": false,
						"value": "abc",
						"errors": [
							"invalid value"
						]
					},
					"time": {
						"is_null": false,
						"is_bound": false,
						"value": "2024-01-04T12:35:02Z",
						"errors": [
							"invalid value"
						]
					}
				}
			}`,
		},
	}))

	t.Run("values", runRequestForm(forms.MimeURLEncoded, func() forms.FormBinder {
		return newTestFormDefaults()
	}, []formTest{
		{
			``,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"bool": {
						"is_null": false,
						"is_bound": false,
						"value": true,
						"errors": null
					},
					"int": {
						"is_null": false,
						"is_bound": false,
						"value": 123,
						"errors": null
					},
					"text": {
						"is_null": false,
						"is_bound": false,
						"value": "abc",
						"errors": null
					},
					"time": {
						"is_null": false,
						"is_bound": false,
						"value": "2024-01-04T12:35:02Z",
						"errors": null
					}
				}
			}`,
		},
		{
			"bool=f&int=5&text=xyz&time=2024-02-05%2011:23:45",
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"bool": {
						"is_null": false,
						"is_bound": true,
						"value": false,
						"errors": null
					},
					"int": {
						"is_null": false,
						"is_bound": true,
						"value": 5,
						"errors": null
					},
					"text": {
						"is_null": false,
						"is_bound": true,
						"value": "xyz",
						"errors": null
					},
					"time": {
						"is_null": false,
						"is_bound": true,
						"value": "2024-02-05T11:23:45Z",
						"errors": null
					}
				}
			}`,
		},
		{
			"bool=12&int=true&text=55&time=abc",
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"bool": {
						"is_null": false,
						"is_bound": false,
						"value": true,
						"errors": [
							"invalid value"
						]
					},
					"int": {
						"is_null": false,
						"is_bound": false,
						"value":123,
						"errors": [
							"invalid value"
						]
					},
					"text": {
						"is_null": false,
						"is_bound": true,
						"value": "55",
						"errors": null
					},
					"time": {
						"is_null": false,
						"is_bound": false,
						"value": "2024-01-04T12:35:02Z",
						"errors": [
							"invalid value"
						]
					}
				}
			}`,
		},
	}))
}

func TestCustomValidation(t *testing.T) {
	runRequestForm(forms.MimeJSON, func() forms.FormBinder {
		return forms.New[customValidationForm](context.Background())
	}, []formTest{
		{
			`{}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"City": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"Name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					}
				}
			}`,
		},
		{
			`{"name":"alice", "city":"amsterdam"}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"City": {
						"is_null": false,
						"is_bound": true,
						"value": "amsterdam",
						"errors": null
					},
					"Name": {
						"is_null": false,
						"is_bound": true,
						"value": "alice",
						"errors": null
					}
				}
			}`,
		},
		{
			`{"name":"bob", "city":"amsterdam"}`,
			`{
				"is_valid": false,
				"errors": [
					"form error"
				],
				"fields": {
					"City": {
						"is_null": false,
						"is_bound": true,
						"value": "amsterdam",
						"errors": null
					},
					"Name": {
						"is_null": false,
						"is_bound": true,
						"value": "bob",
						"errors": [
							"forbidden name"
						]
					}
				}
			}`,
		},
		{
			`{"name":"eve", "city":"paris"}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"City": {
						"is_null": false,
						"is_bound": true,
						"value": "paris",
						"errors": [
							"invalid city"
						]
					},
					"Name": {
						"is_null": false,
						"is_bound": true,
						"value": "eve",
						"errors": null
					}
				}
			}`,
		},
	})(t)
}

func TestCustomUnmarshl(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		f := forms.New[customUnmarshalForm](context.Background())
		require.NoError(t, json.Unmarshal([]byte("{}"), f))
		assert.Equal(t, "json", f.unmarshaledBy)
	})

	t.Run("values", func(t *testing.T) {
		f := forms.New[customUnmarshalForm](context.Background())
		require.NoError(t, forms.UnmarshalURLValues(url.Values{}, f))
		assert.Equal(t, "values", f.unmarshaledBy)
	})
}

func TestChoiceForm(t *testing.T) {
	type choiceForm struct {
		forms.Form
		Name  forms.TextField     `json:"name"  validate:"trim required"`
		Group forms.TextField     `json:"group" validate:"trim"`
		Acls  forms.TextListField `json:"acls"  validate:"trim"`
	}

	newChoiceForm := func() *choiceForm {
		form := forms.New[choiceForm](
			forms.WithTranslator(context.Background(), prefixTranslator("E")),
		)

		vf := forms.ValueValidatorFunc[string](func(_ forms.Binder, v string) error {
			if strings.Contains(v, "xx") {
				return forms.Gettext("value contains xx")
			}
			return nil
		})
		form.Name.SetValidators(append(form.Name.Validators(), vf))

		forms.Choices(&form.Group, forms.Choice("User", "user"), forms.Choice("Admin", "admin"))
		form.Group.Set("user")

		forms.Choices(&form.Acls, forms.Choice("Read", "r"), forms.Choice("Write", "w"))

		return form
	}
	runRequestForm(forms.MimeJSON, func() forms.FormBinder {
		return newChoiceForm()
	}, []formTest{
		{
			`{}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"acls": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"group": {
						"is_null": false,
						"is_bound": false,
						"value": "user",
						"errors": null
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					}
				}
        	}`,
		},
		{
			`{"name": "alice"}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"acls": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"group": {
						"is_null": false,
						"is_bound": false,
						"value": "user",
						"errors": null
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "alice",
						"errors": null
					}
				}
			}`,
		},
		{
			`{"name": "alice", "group": null}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"acls": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"group": {
						"is_null": true,
						"is_bound": true,
						"value": "user",
						"errors": null
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "alice",
						"errors": null
					}
				}
			}`,
		},
		{
			`{"name": "alice", "group": "admin", "acls": ["r", "w"]}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"acls": {
						"is_null": false,
						"is_bound": true,
						"value": ["r", "w"],
						"errors": null
					},
					"group": {
						"is_null": false,
						"is_bound": true,
						"value": "admin",
						"errors": null
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "alice",
						"errors": null
					}
				}
			}`,
		},
		{
			`{"name": "alixxce", "group": "admin"}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"acls": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"group": {
						"is_null": false,
						"is_bound": true,
						"value": "admin",
						"errors": null
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "alixxce",
						"errors": [
							"E:value contains xx"
						]
					}
				}
			}`,
		},
		{
			`{"name": "alixxce", "group": "foo", "acls": ["r", "g"]}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"acls": {
						"is_null": false,
						"is_bound": true,
						"value": ["r", "g"],
						"errors": [
							"E:g is not one of \"r\", \"w\""
						]
					},
					"group": {
						"is_null": false,
						"is_bound": true,
						"value": "foo",
						"errors": [
							"E:foo is not one of \"user\", \"admin\""
						]
					},
					"name": {
						"is_null": false,
						"is_bound": true,
						"value": "alixxce",
						"errors": [
							"E:value contains xx"
						]
					}
				}
			}`,
		},
	})(t)
}

func TestFullForm(t *testing.T) {
	type UserInfoForm struct {
		Username forms.TextField `json:"username" validate:"trim required"`
		Email    forms.TextField `json:"email"    validate:"trim required"`
		Password forms.TextField `json:"password" validate:"trim required"`
		Address  struct {
			City    forms.TextField `json:"city"    validate:"trim required"`
			Country forms.TextField `json:"country" validate:"trim required"`
		} `json:"address"`
	}

	type OptionForm struct {
		Age   forms.NumberField[uint] `json:"age"   validate:"gte:18"`
		Links forms.URLListField      `json:"links" validate:"trim"`
	}

	type fullForm struct {
		forms.Form
		UserInfoForm
		OptionForm
	}

	newFullForm := func() *fullForm {
		f := forms.New[fullForm](forms.WithTranslator(context.Background(), prefixTranslator("E")))
		return f
	}

	t.Run("json", runRequestForm(forms.MimeJSON, func() forms.FormBinder {
		return newFullForm()
	}, []formTest{
		{
			`{}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"address.city": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					},
					"address.country": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					},
					"age": {
						"is_null": false,
						"is_bound": false,
						"value": 0,
						"errors": null
					},
					"email": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					},
					"links": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"password": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					},
					"username": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					}
				}
			}`,
		},
		{
			`{
				"username": "alice",
				"email": "alice@example.org",
				"password": "th1s 1s not safe",
				"address": {
					"city": "Brussels",
					"country": "Belgium"
				},
				"age": 20,
				"links": [
					"https://example.org",
					"http://example.net"
				]
			}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"address.city": {
						"is_null": false,
						"is_bound": true,
						"value": "Brussels",
						"errors": null
					},
					"address.country": {
						"is_null": false,
						"is_bound": true,
						"value": "Belgium",
						"errors": null
					},
					"age": {
						"is_null": false,
						"is_bound": true,
						"value": 20,
						"errors": null
					},
					"email": {
						"is_null": false,
						"is_bound": true,
						"value": "alice@example.org",
						"errors": null
					},
					"links": {
						"is_null": false,
						"is_bound": true,
						 "value": [
							"https://example.org",
							"http://example.net"
						],
						"errors": null
					},
					"password": {
						"is_null": false,
						"is_bound": true,
						"value": "th1s 1s not safe",
						"errors": null
					},
					"username": {
						"is_null": false,
						"is_bound": true,
						"value": "alice",
						"errors": null
					}
				}
			}`,
		},
	}))

	t.Run("values", runRequestForm(forms.MimeURLEncoded, func() forms.FormBinder {
		return newFullForm()
	}, []formTest{
		{
			"",
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"address.city": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					},
					"address.country": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					},
					"age": {
						"is_null": false,
						"is_bound": false,
						"value": 0,
						"errors": null
					},
					"email": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					},
					"links": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"password": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					},
					"username": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"E:field is required"
						]
					}
				}
			}`,
		},
		{
			url.Values{
				"username":        {"alice"},
				"email":           {"alice@example.org"},
				"password":        {"th1s 1s not safe"},
				"address.city":    {"Brussels"},
				"address.country": {"Belgium"},
				"age":             {"20"},
				"links":           {"https://example.org", "http://example.net"},
			}.Encode(),
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"address.city": {
						"is_null": false,
						"is_bound": true,
						"value": "Brussels",
						"errors": null
					},
					"address.country": {
						"is_null": false,
						"is_bound": true,
						"value": "Belgium",
						"errors": null
					},
					"age": {
						"is_null": false,
						"is_bound": true,
						"value": 20,
						"errors": null
					},
					"email": {
						"is_null": false,
						"is_bound": true,
						"value": "alice@example.org",
						"errors": null
					},
					"links": {
						"is_null": false,
						"is_bound": true,
						"value": [
							"https://example.org",
							"http://example.net"
						],
						"errors": null
					},
					"password": {
						"is_null": false,
						"is_bound": true,
						"value": "th1s 1s not safe",
						"errors": null
					},
					"username": {
						"is_null": false,
						"is_bound": true,
						"value": "alice",
						"errors": null
					}
				}
			}`,
		},
	}))
}
