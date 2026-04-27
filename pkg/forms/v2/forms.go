// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
)

var (
	_ FormBinder    = (*Form)(nil)
	_ ContextHolder = (*Form)(nil)
)

// Error definitions.
var (
	ErrFormIsBound        = errors.New("form is already bound")
	ErrFormInvalidInput   = errors.New("invalid input data")
	ErrUnknownContentType = errors.New("unknown content type")
	ErrUnexpected         = Gettext("an unexpected error has occurred")
)

// MimeType is a mime type forms can be loaded from.
type MimeType string

// Common input mime types.
const (
	MimeJSON       MimeType = "application/json"
	MimeURLEncoded MimeType = "application/x-www-form-urlencoded"
	MimeMultipart  MimeType = "multipart/form-data"
)

type marshalledForm struct {
	IsValid bool              `json:"is_valid"`
	Errors  Errors            `json:"errors"`
	Fields  map[string]Binder `json:"fields"`
}

// ValidateChecker describes a type that can bring its own validation method.
// The returned error is added to the error list. To return several errors at once,
// one can use [Errors] or [errors.Join].
type ValidateChecker interface {
	Validate() error
}

// FormBinder is the interface implented by types that can
// act as a form.
type FormBinder interface {
	Fields() map[string]Binder
	Errors() Errors
	AddErrors(...error)
	IsBound() bool
	Bind()
	IsValid() bool
}

// Form is our based type for form composition.
type Form struct {
	bound    bool
	valid    *bool
	instance FormBinder
	fields   map[string]Binder
	errors   Errors
	ctx      context.Context
}

// New prepares and returns a new instance of T.
// It panics if T is not a struct implementing [FormBinder]
// or when a field's "validate" tag does not exist.
func New[T any](ctx context.Context, options ...func(FormBinder)) *T {
	t := reflect.TypeFor[T]()

	// Must be a struct
	if t.Kind() != reflect.Struct {
		panic("type is not a struct")
	}

	// Must be a [FormBinder].
	if !reflect.PointerTo(t).Implements(reflect.TypeFor[FormBinder]()) {
		panic("type is not a forms.FormBinder")
	}

	form := new(T)
	if form, ok := any(form).(ContextHolder); ok {
		form.SetContext(ctx)
	}

	// Set each field and a field list for the form
	fields := map[string]Binder{}
	for info := range recurseStructFields(reflect.ValueOf(form).Elem(), "") {
		if info.field.Tag.Get("json") == "-" {
			continue
		}

		// Only [Binder] fields are allowed.
		if !info.value.Addr().Type().Implements(reflect.TypeFor[Binder]()) {
			continue
		}

		// Prepare field
		field := info.value.Addr().Interface()
		if field, ok := field.(interface{ SetName(string) }); ok {
			field.SetName(info.name)
		}

		if field, ok := field.(ContextHolder); ok {
			field.SetContext(ctx)
		}

		// Add validators from tag
		if t := info.field.Tag.Get(ValidateTagName); len(t) > 0 {
			if field, ok := field.(ValidatorsProvider); ok {
				// Note: we don't set validators at once with [slices.Collect] because each
				// iteration of [collectValidators] can have side effects.
				// Thus, we must append to the existing validators, each time.
				for v := range collectValidators(ctx, any(form).(FormBinder), field.(Binder), strings.Fields(t)...) {
					field.SetValidators(append(field.Validators(), v))
				}
			}
		}

		fields[info.name] = field.(Binder)
	}

	// Set fields
	if form, ok := any(form).(interface{ SetFields(map[string]Binder) }); ok {
		form.SetFields(fields)
	}

	// Set form
	if f, ok := any(form).(interface{ SetInstance(FormBinder) }); ok {
		f.SetInstance(any(form).(FormBinder))
	}

	for _, fn := range options {
		fn(any(form).(FormBinder))
	}

	return form
}

// Fields returns the form's registered [Binder] fields.
// Their respective name matches the name used during
// [url.Values] unmarshaling
// (with dot separated prefix and name for nested values).
func (f *Form) Fields() map[string]Binder {
	return f.fields
}

// SetFields is used by [New] and will panic if called more than once.
func (f *Form) SetFields(fields map[string]Binder) {
	if f.fields != nil {
		panic("can't mutate form's field list")
	}
	f.fields = fields
}

// SetInstance is used by [New] and will panic if called more than once.
func (f *Form) SetInstance(instance FormBinder) {
	if f.instance != nil {
		panic("can't set Form.instance more than once")
	}
	f.instance = instance
}

// Context returns the form's context.
func (f *Form) Context() context.Context {
	return f.ctx
}

// SetContext sets the form's context. It implements [ContextHolder].
func (f *Form) SetContext(ctx context.Context) {
	f.ctx = ctx
}

// Errors return a flat list of errors.
func (f Form) Errors() Errors {
	return slices.Collect(IterErrorsTr(f.ctx, f.errors))
}

// AddErrors adds errors to the form.
func (f *Form) AddErrors(errs ...error) {
	for _, err := range errs {
		if err != nil {
			f.errors = append(f.errors, err)
		}
	}
}

// IsBound returns whether the form is bound.
func (f *Form) IsBound() bool {
	return f.bound
}

// Bind marks the form as bound.
func (f *Form) Bind() {
	f.bound = true
}

// IsValid returns true when the form is valid.
func (f *Form) IsValid() bool {
	if !f.IsBound() {
		return true
	}

	if f.valid == nil {
		// We only run validators once
		f.valid = new(true)
		for _, field := range f.fields {
			// This runs the field's validators
			if field, ok := field.(ValidatorProvider); ok {
				field.IsValid()
			}

			// Call Validate() on each field when they implement it.
			if field, ok := field.(ValidateChecker); ok {
				if err := field.Validate(); err != nil {
					if field, ok := field.(interface{ AddErrors(...error) }); ok {
						field.AddErrors(err)
					}
				}
			}
		}

		// Call Validate() on the actual form if it implements it.
		if form, ok := f.instance.(ValidateChecker); ok {
			if err := form.Validate(); err != nil {
				f.AddErrors(err)
			}
		}

	}

	// Check for each field error list.
	for _, field := range f.fields {
		if len(field.Errors()) > 0 {
			*f.valid = false
			break
		}
	}

	return len(f.errors) == 0 && *f.valid
}

// MarshalJSON implement [json.Marshaler].
func (f *Form) MarshalJSON() ([]byte, error) {
	res := marshalledForm{
		IsValid: f.IsValid(),
		Errors:  slices.Collect(IterErrorsTr(f.ctx, f.Errors())),
		Fields:  f.fields,
	}

	return json.Marshal(res)
}

// MarshalValues calls [MarshalValues] on the form's concrete instance.
func (f *Form) MarshalValues() map[string]any {
	return MarshalValues(f.instance)
}

// MarshalValues returns a recursive map of all values implementing [Binder].
// It panics when "in" is not a struct.
func MarshalValues(in any) map[string]any {
	res := map[string]any{}

	for info := range iterStructFields(reflect.ValueOf(in).Elem()) {
		if v, ok := info.value.Interface().(Binder); ok {
			res[info.name] = v.V()
			continue
		}
		if info.field.Type.Kind() == reflect.Struct {
			res[info.name] = MarshalValues(info.value.Addr().Interface())
		}
	}
	return res
}

// Bind loads the data using the method tied
// to the request's content-type header.
func Bind(r *http.Request, f FormBinder) {
	if f.IsBound() {
		f.AddErrors(ErrFormIsBound)
		return
	}
	f.Bind()

	mediaType, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))

	// Default to application/x-www-form-urlencoded.
	if mediaType == "" {
		mediaType = string(MimeURLEncoded)
	}

	if fn, ok := RequestLoaders[mediaType]; ok {
		if err := fn(r, f); err != nil {
			f.AddErrors(err)
		}
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return
	}

	f.AddErrors(ErrUnknownContentType)
}

// BindValues loads the data from a [url.Values] input.
// This can be used to load values only from the URL's query string.
func BindValues(values url.Values, f FormBinder) {
	if f.IsBound() {
		f.AddErrors(ErrFormIsBound)
		return
	}
	f.Bind()

	if err := UnmarshalURLValues(values, f); err != nil {
		f.AddErrors(err)
	}
}

// BindAs combines [New] and [Bind] in one step, returning
// the newly created form.
func BindAs[T any](r *http.Request, options ...func(FormBinder)) *T {
	form := New[T](r.Context(), options...)
	Bind(r, any(form).(FormBinder))
	return form
}

func unmarshalMultipart(r *http.Request, v any) (err error) {
	if r.MultipartForm == nil {
		if err = r.ParseMultipartForm(16 << 20); err != nil {
			return err
		}
	}

	// Bind the file fields
	if v, ok := v.(FormBinder); ok {
		fields := v.Fields()
		if len(fields) == 0 {
			goto URLValues
		}

		for name, headers := range r.MultipartForm.File {
			if len(headers) == 0 {
				continue
			}
			field, ok := fields[name]
			if !ok {
				continue
			}

			if field, ok := field.(FilesUnmarshaler); ok {
				if err := field.UnmarshalFiles(headers); err != nil {
					return err
				}
			}
		}
	}

	// Bind the [url.Values]
URLValues:
	return UnmarshalURLValues(r.Form, v)
}
