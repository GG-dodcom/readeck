// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/textproto"
)

// FilesUnmarshaler is an interface implemented by types than can load
// a [multipart.FileHeader] list.
type FilesUnmarshaler interface {
	UnmarshalFiles([]*multipart.FileHeader) error
}

// FileOpener describes an opener interface. Its [Open] function must return an [io.ReadCloser].
type FileOpener interface {
	Open() (io.ReadCloser, error)
	Filename() string
	Size() int64
	Header() textproto.MIMEHeader
}

// MultipartFileOpener is a [FileOpener] implementation wrapping [multipart.FileHeader].
type MultipartFileOpener struct {
	*multipart.FileHeader
}

// Open implements [FileOpener].
func (o *MultipartFileOpener) Open() (io.ReadCloser, error) {
	return o.FileHeader.Open()
}

// Filename implements [FileOpener].
func (o *MultipartFileOpener) Filename() string {
	return o.FileHeader.Filename
}

// Size implements [FileOpener].
func (o *MultipartFileOpener) Size() int64 {
	return o.FileHeader.Size
}

// Header implements [FileOpener].
func (o *MultipartFileOpener) Header() textproto.MIMEHeader {
	return o.FileHeader.Header
}

// MarshalJSON implement [json.Marshaler].
func (o *MultipartFileOpener) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name":         o.Filename(),
		"size":         o.Size(),
		"content-type": o.Header().Get("content-type"),
	})
}

// StringOpener is a [FileOpener] implementation using bytes.
type StringOpener []byte

// Open implements [FileOpener].
func (o StringOpener) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(o)), nil
}

// Filename implements [FileOpener].
func (o StringOpener) Filename() string {
	return ""
}

// Size implements [FileOpener].
func (o StringOpener) Size() int64 {
	return int64(len(o))
}

// Header implements [FileOpener].
func (o StringOpener) Header() textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Type": []string{"text/plain"},
	}
}

// MarshalJSON implement [json.Marshaler].
func (o StringOpener) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name":         o.Filename(),
		"size":         o.Size(),
		"content-type": o.Header().Get("content-type"),
	})
}

// File is a [FileOpener] holder.
type File FileOpener

// FileField is a field that holds a [File] value.
type FileField struct {
	Field[File, FileValue]
}

// FileListField is a field that holds a list of [File] values.
type FileListField ListField[File, ListValue[File, FileValue]]

// FileValue is a [Valuer] for uploaded files.
// It can open files submitted as [multipart.FileHeader] or strings
// from JSON or url values.
type FileValue struct {
	BaseValue[File]
}

func (v FileValue) String() string {
	res := "<file"

	if v.value != nil {
		if f := v.value.Filename(); f != "" {
			res += " " + f
		}
	}
	return res + ">"
}

// UnmarshalJSON implements [json.Unmarshaler].
// It decodes the content as a string and produces a [StringOpener].
func (v *FileValue) UnmarshalJSON(in []byte) error {
	return DecodeValueData(v, in, func(data []byte) (res *File, err error) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}

		return new(File(StringOpener(s))), nil
	})
}

// UnmarshalValues implements [ValuesUnmarshaler].
// It decodes the content as a string and produces a [StringOpener].
func (v *FileValue) UnmarshalValues(values []string) error {
	return DecodeValueData(v, values, func(data []string) (res *File, err error) {
		if len(data) == 0 || data[0] == nilText {
			return nil, nil
		}

		return new(File(StringOpener([]byte(values[0])))), nil
	})
}

// UnmarshalFiles imlements [FilesUnmarshaler].
// It decodes a the first file into a [MultipartFileOpener].
func (v *FileValue) UnmarshalFiles(files []*multipart.FileHeader) error {
	v.SetFlags(v.Flags() | IsEmpty)

	if len(files) == 0 {
		v.SetFlags(IsEmpty | IsBound)
		return nil
	}

	v.value = &MultipartFileOpener{files[0]}
	v.SetFlags(v.Flags() | IsBound)
	if v.value.Size() > 0 {
		v.SetFlags(v.Flags() &^ IsEmpty)
	}
	return nil
}
