// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/pkg/forms"
)

func runValuesFile[T any, V forms.Valuer[T]](tests []valueTest[[]*multipart.FileHeader, T]) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				value := *new(V)
				var err error
				if value, ok := any(&value).(forms.FilesUnmarshaler); ok {
					err = value.UnmarshalFiles(test.data)
				} else {
					panic("value is not a FileUnmarshaler")
				}
				test.assert(t, value, err)
			})
		}
	}
}

func TestFileValue(t *testing.T) {
	fh := &multipart.FileHeader{
		Filename: "test.txt",
		Header:   textproto.MIMEHeader{"Content-Type": []string{"text/plain"}},
		Size:     int64(14),
	}

	fhEmpty := &multipart.FileHeader{
		Filename: "test.txt",
		Header:   textproto.MIMEHeader{"Content-Type": []string{"text/plain"}},
		Size:     int64(0),
	}

	t.Run("fileHeader", runValuesFile[forms.File, forms.FileValue]([]valueTest[[]*multipart.FileHeader, forms.File]{
		{
			data:  []*multipart.FileHeader{},
			flags: forms.IsBound | forms.IsEmpty,
			str:   "<file>",
		},
		{
			data:  []*multipart.FileHeader{fhEmpty},
			value: &forms.MultipartFileOpener{fhEmpty},
			flags: forms.IsBound | forms.IsEmpty,
			str:   "<file test.txt>",
		},
		{
			data:  []*multipart.FileHeader{fh},
			value: &forms.MultipartFileOpener{fh},
			flags: forms.IsBound,
			str:   "<file test.txt>",
		},
	}))

	t.Run("json", runJSONValue[forms.File, forms.FileValue]([]valueTest[string, forms.File]{
		{
			data:  "null",
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
			value: nil,
			str:   "<file>",
		},
		{
			data:  `""`,
			flags: forms.IsBound | forms.IsEmpty,
			value: forms.StringOpener(""),
			str:   "<file>",
		},
		{
			data:  `"abc"`,
			flags: forms.IsBound,
			value: forms.StringOpener("abc"),
			str:   "<file>",
		},
	}))

	t.Run("values", runValuesValue[forms.File, forms.FileValue]([]valueTest[[]string, forms.File]{
		{
			data:  []string{},
			flags: forms.IsBound | forms.IsEmpty,
			str:   "<file>",
		},
		{
			data:  []string{"\uff00"},
			flags: forms.IsBound | forms.IsEmpty | forms.IsNil,
			str:   "<file>",
		},
		{
			data:  []string{""},
			flags: forms.IsBound | forms.IsEmpty,
			value: forms.StringOpener(""),
			str:   "<file>",
		},
		{
			data:  []string{"abc"},
			flags: forms.IsBound,
			value: forms.StringOpener("abc"),
			str:   "<file>",
		},
		{
			data:  []string{"xyz", "abc"},
			flags: forms.IsBound,
			value: forms.StringOpener("xyz"),
			str:   "<file>",
		},
	}))
}

func TestFileField(t *testing.T) {
	type mpForm struct {
		forms.Form
		Name forms.TextField `json:"name"`
		File forms.FileField `json:"file"`
	}

	t.Run("single file", func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)

		body := new(bytes.Buffer)
		mp := multipart.NewWriter(body)

		// Normal field
		require.NoError(mp.WriteField("name", "alice"))

		// File field
		w, err := mp.CreateFormFile("file", "file.txt")
		require.NoError(err)
		_, err = w.Write([]byte("test\ncontent\n"))
		require.NoError(err)

		require.NoError(mp.Close())

		r, _ := http.NewRequest(http.MethodPost, "/", body)
		r.Header.Set("content-type", mp.FormDataContentType())

		form := forms.BindAs[mpForm](r)

		assert.True(form.IsBound())
		assert.True(form.IsValid())

		assert.False(form.Name.IsNil())
		assert.Equal("alice", form.Name.Value())

		assert.False(form.File.IsEmpty())

		assert.Equal("<file file.txt>", fmt.Sprint(form.File))

		var content io.ReadCloser
		buf := new(strings.Builder)
		content, err = form.File.Value().Open()
		require.NoError(err)
		io.Copy(buf, content)
		require.NoError(content.Close())
		assert.Equal("test\ncontent\n", buf.String())
	})

	t.Run("single file json", func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)

		body := new(bytes.Buffer)
		enc := json.NewEncoder(body)
		require.NoError(enc.Encode(map[string]string{
			"name": "alice",
			"file": "test\ncontent\n",
		}))

		r, _ := http.NewRequest(http.MethodPost, "/", body)
		r.Header.Set("content-type", "application/json")

		form := forms.BindAs[mpForm](r)

		assert.True(form.IsBound())
		assert.True(form.IsValid())

		assert.False(form.Name.IsNil())
		assert.Equal("alice", form.Name.Value())

		assert.False(form.File.IsEmpty())

		assert.Equal("<file>", fmt.Sprint(form.File))

		buf := new(strings.Builder)
		content, err := form.File.Value().Open()
		require.NoError(err)
		io.Copy(buf, content)
		require.NoError(content.Close())
		assert.Equal("test\ncontent\n", buf.String())
	})

	t.Run("file list", func(t *testing.T) {
		type lForm struct {
			forms.Form
			Name  forms.TextField     `json:"name"`
			Files forms.FileListField `json:"file"`
		}

		assert := assert.New(t)
		require := require.New(t)

		body := new(bytes.Buffer)
		mp := multipart.NewWriter(body)

		// Normal field
		require.NoError(mp.WriteField("name", "alice"))

		// File field
		w, err := mp.CreateFormFile("file", "file.txt")
		require.NoError(err)
		_, err = w.Write([]byte("test 1\n"))
		require.NoError(err)

		w, err = mp.CreateFormFile("file", "file.txt")
		require.NoError(err)
		_, err = w.Write([]byte("test 2\n"))
		require.NoError(err)

		require.NoError(mp.Close())

		r, _ := http.NewRequest(http.MethodPost, "/", body)
		r.Header.Set("content-type", mp.FormDataContentType())

		form := forms.BindAs[lForm](r)

		assert.True(form.IsBound())
		assert.True(form.IsValid())

		assert.False(form.Name.IsNil())
		assert.Equal("alice", form.Name.Value())

		assert.False(form.Files.IsEmpty())

		require.Len(form.Files.Value(), 2)

		var content io.ReadCloser
		buf := new(strings.Builder)
		content, err = form.Files.Value()[0].Open()
		require.NoError(err)
		io.Copy(buf, content)
		require.NoError(content.Close())
		assert.Equal("test 1\n", buf.String())

		buf.Reset()
		content, err = form.Files.Value()[1].Open()
		require.NoError(err)
		io.Copy(buf, content)
		require.NoError(content.Close())
		assert.Equal("test 2\n", buf.String())
	})
}

func TestMultipartForm(t *testing.T) {
	type mpForm struct {
		forms.Form
		Name forms.TextField `json:"name" validate:"trim required"`
		File forms.FileField `json:"file" validate:"required"`
	}

	type mpListForm struct {
		forms.Form
		Name  forms.TextField     `json:"name"  validate:"trim required"`
		Files forms.FileListField `json:"files" validate:"required"`
	}

	t.Run("single file", runMultipartForm(func() forms.FormBinder {
		return forms.New[mpForm](context.Background())
	}, []multipartTest{
		{
			func(_ *multipart.Writer) error {
				return nil
			},
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"file": {
						"is_null": false,
						"is_bound": false,
						"value": null,
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"field is required"
					]
					}
				}
			}`,
		},
		{
			func(w *multipart.Writer) error {
				_ = w.WriteField("name", " alice  ")
				fw, err := w.CreateFormFile("file", "file.txt")
				if err != nil {
					return err
				}
				_, err = fw.Write([]byte("test\ncontent\n"))
				if err != nil {
					return err
				}
				return nil
			},
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"file": {
						"is_null": false,
						"is_bound": true,
						"value": {
							"content-type": "application/octet-stream",
							"name": "file.txt",
							"size": 13
						},
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

	t.Run("file list", runMultipartForm(func() forms.FormBinder {
		return forms.New[mpListForm](context.Background())
	}, []multipartTest{
		{
			func(_ *multipart.Writer) error {
				return nil
			},
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"files": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"field is required"
					]
					}
				}
			}`,
		},
		{
			func(w *multipart.Writer) error {
				_ = w.WriteField("name", " alice  ")
				for _, content := range []string{"test\n", "test\nsecond file\n"} {
					fw, err := w.CreateFormFile("files", "file.txt")
					if err != nil {
						return err
					}
					_, err = fw.Write([]byte(content))
					if err != nil {
						return err
					}
				}

				return nil
			},
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"files": {
						"is_null": false,
						"is_bound": true,
						"value": [
							{
								"content-type": "application/octet-stream",
								"name": "file.txt",
								"size": 5
							},
							{
								"content-type": "application/octet-stream",
								"name": "file.txt",
								"size": 17
							}
						],
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

	t.Run("single file json", runRequestForm(forms.MimeJSON, func() forms.FormBinder {
		return forms.New[mpForm](context.Background())
	}, []formTest{
		{
			`{}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"file": {
						"is_null": false,
						"is_bound": false,
						"value": null,
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"field is required"
					]
					}
				}
			}`,
		},
		{
			`{
				"name": " alice ",
				"file": "test\ncontent\n"
			}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"file": {
						"is_null": false,
						"is_bound": true,
						"value": {
							"name": "",
							"size": 13,
							"content-type": "text/plain"
						},
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
			`{
				"name": " alice ",
				"file": 12
			}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"file": {
						"is_null": false,
						"is_bound": false,
						"value": null,
						"errors": [
							"invalid value"
						]
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
			`{
				"name": " alice ",
				"file": null
			}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"file": {
						"is_null": true,
						"is_bound": true,
						"value": null,
						"errors": [
							"field is required"
						]
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

	t.Run("single file values", runRequestForm(forms.MimeURLEncoded, func() forms.FormBinder {
		return forms.New[mpForm](context.Background())
	}, []formTest{
		{
			"",
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"file": {
						"is_null": false,
						"is_bound": false,
						"value": null,
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"field is required"
					]
					}
				}
			}`,
		},
		{
			"name=alice&file=",
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"file": {
						"is_null": false,
						"is_bound": true,
						"value": {
							"name": "",
							"size": 0,
							"content-type": "text/plain"
						},
						"errors": [
							"field is required"
						]
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
			"name=alice&file=test",
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"file": {
						"is_null": false,
						"is_bound": true,
						"value": {
							"name": "",
							"size": 4,
							"content-type": "text/plain"
						},
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

	t.Run("file list json", runRequestForm(forms.MimeJSON, func() forms.FormBinder {
		return forms.New[mpListForm](context.Background())
	}, []formTest{
		{
			`{}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"files": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"field is required"
						]
					}
				}
			}`,
		},
		{
			`{"files": []}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"files": {
						"is_null": false,
						"is_bound": true,
						"value": [],
						"errors": [
							"field is required"
						]
					},
					"name": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": [
							"field is required"
						]
					}
				}
			}`,
		},
		{
			`{
				"name": "alice",
				"files": ["abc"]
			}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"files": {
						"is_null": false,
						"is_bound": true,
						"value": [
							{
								"name": "",
								"content-type": "text/plain",
								"size": 3
							}
						],
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
			`{
				"name": "alice",
				"files": ["abc", "abcdefgh"]
			}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"files": {
						"is_null": false,
						"is_bound": true,
						"value": [
							{
								"name": "",
								"content-type": "text/plain",
								"size": 3
							},
							{
								"name": "",
								"content-type": "text/plain",
								"size": 8
							}
						],
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
			`{
				"name": "alice",
				"files": ["abc", null]
			}`,
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"files": {
						"is_null": false,
						"is_bound": true,
						"value": [
							{
								"name": "",
								"content-type": "text/plain",
								"size": 3
							}
						],
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
			`{
				"name": "alice",
				"files": ["abc", 12]
			}`,
			`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"files": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": [
							"invalid value"
						]
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
