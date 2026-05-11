// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"slices"
	"time"
)

var (
	_ Binder             = (*Field[any, BaseValue[any]])(nil)
	_ TypedBinder[any]   = (*Field[any, BaseValue[any]])(nil)
	_ ValidatorsProvider = (*Field[any, BaseValue[any]])(nil)
	_ ContextHolder      = (*Field[any, BaseValue[any]])(nil)
)

// ErrInvalidValue is the error for invalid value.
var ErrInvalidValue = errors.New("invalid value")

// FieldFlags is a field's flag list.
type FieldFlags byte

const (
	// ValidatedField indicates a field has been validated.
	ValidatedField FieldFlags = 1 << iota
)

type marshalledField[T any, V Valuer[T]] struct {
	IsNil   bool   `json:"is_null"`
	IsBound bool   `json:"is_bound"`
	Value   V      `json:"value"`
	Errors  Errors `json:"errors"`
}

// Binder describes a type that is "bound" to some data by the means of
// appropriate unmarshaling. It then provides informations about its state.
type Binder interface {
	fmt.Stringer
	IsBound() bool
	IsEmpty() bool
	IsNil() bool
	Errors() Errors
	V() any
}

// TypedBinder describes a [Binder] with its Value method.
type TypedBinder[T any] interface {
	Binder
	Value() T
}

// Field is a generic field that holds a value of the
// given type and implements [Binder].
// It's the common building block for a specialized field.
type Field[T any, V Valuer[T]] struct {
	flags      FieldFlags
	name       string
	errors     Errors
	validators []Validator
	ctx        context.Context
	value      V
}

// Name returns the field's name.
func (f Field[T, V]) Name() string {
	return f.name
}

// SetName sets the field's name.
func (f *Field[T, V]) SetName(name string) {
	f.name = name
}

// Value returns the field's [Valuer] value.
func (f Field[T, V]) Value() T {
	return f.value.Value()
}

// V returns the field's value with "any" type.
func (f Field[T, V]) V() any {
	return f.value.Value()
}

// String returns the field's [Valuer] string value.
func (f Field[T, V]) String() string {
	return f.value.String()
}

// Set sets the [Valuer]'s value if it implements [Setter].
func (f *Field[T, V]) Set(v T) {
	if t, ok := any(&f.value).(Setter[T]); ok {
		t.Set(v)
	}
}

// SetNil sets the [Valuer] to nil if it implements [Setter] and [FlagSetter].
// It only sets the nil flag and doesn't empty the value.
func (f *Field[T, V]) SetNil() {
	if t, ok := any(&f.value).(interface {
		Setter[T]
		FlagSetter
	}); ok {
		t.SetFlags(IsEmpty | IsNil)
	}
}

// Errors return the field's [Errors].
func (f Field[T, V]) Errors() Errors {
	return slices.Collect(IterErrorsTr(f.ctx, f.errors))
}

func (f Field[T, V]) hasErrors() bool {
	return len(f.errors) > 0
}

// AddErrors add errors to the field.
func (f *Field[T, V]) AddErrors(errs ...error) {
	for _, err := range errs {
		if err != nil {
			f.errors = append(f.errors, err)
		}
	}
}

// SetContext sets the field's context. It implements [ContextHolder].
func (f *Field[T, V]) SetContext(ctx context.Context) {
	f.ctx = ctx
}

// IsBound returns true if the field is bound.
func (f Field[T, V]) IsBound() bool {
	return f.value.IsBound()
}

// IsNil returns true if the field's value is null.
func (f Field[T, V]) IsNil() bool {
	return f.value.IsNil()
}

// IsEmpty returns true if the field's value is empty.
func (f Field[T, V]) IsEmpty() bool {
	return f.value.IsEmpty()
}

// Validators implements [ValidatorsProvider] and returns
// the value's validators.
func (f Field[T, V]) Validators() []Validator {
	return f.validators
}

// SetValidators implements [ValidatorsProvider]
// and sets the field's validators.
func (f *Field[T, V]) SetValidators(validators []Validator) {
	f.validators = validators
}

// ApplyValidators applies the given validators to the field
// and add the resulting errors to the field's error list.
// It can run at any time, including during a form or field
// custom validation.
func (f *Field[T, V]) ApplyValidators(validators ...Validator) {
	f.AddErrors(ApplyValidators[T](f, f.Value(), validators...)...)
}

// UnmarshalJSON implements [json.Unmarshaler]. It always returns nil
// and errors, if any, are added to the field's error list.
func (f *Field[T, V]) UnmarshalJSON(in []byte) error {
	if v, ok := any(&f.value).(ValidatorsProvider); ok {
		v.SetValidators(f.validators)
	}

	if err := json.Unmarshal(in, &f.value); err != nil {
		f.AddErrors(ErrInvalidValue)
	}
	return nil
}

// UnmarshalValues implement [ValuesUnmarshaler]. It always returns nil
// and errors, if any, are added to the field's error list.
func (f *Field[T, V]) UnmarshalValues(values []string) error {
	if v, ok := any(&f.value).(ValidatorsProvider); ok {
		v.SetValidators(f.validators)
	}

	if err := UnmarshalValues(values, &f.value); err != nil {
		f.AddErrors(ErrInvalidValue)
	}
	return nil
}

// UnmarshalFiles implements [FilesUnmarshaler]. It only works on
// [Valuer]s implementing [FilesUnmarshaler] themselves.
func (f *Field[T, V]) UnmarshalFiles(files []*multipart.FileHeader) error {
	if v, ok := any(&f.value).(ValidatorsProvider); ok {
		v.SetValidators(f.validators)
	}

	if v, ok := any(&f.value).(FilesUnmarshaler); ok {
		if err := v.UnmarshalFiles(files); err != nil {
			f.AddErrors(ErrInvalidValue)
		}
	}
	return nil
}

// MarshalJSON implements [json.Marshaler].
func (f Field[T, V]) MarshalJSON() ([]byte, error) {
	return json.Marshal(marshalledField[T, V]{
		IsNil:   f.IsNil(),
		IsBound: f.IsBound(),
		Value:   f.value,
		Errors:  slices.Collect(IterErrorsTr(f.ctx, f.errors)),
	})
}

// IsValid applies the field's [FieldValidator] and [ValueValidator].
// Each returned error is added to the field's error list.
// It returns false when the error list is not empty.
func (f *Field[T, V]) IsValid() bool {
	if f.flags&ValidatedField > 0 {
		return !f.hasErrors()
	}

	defer func() {
		f.flags |= ValidatedField
	}()

	if err := ApplyValidators[T](f, f.Value(), f.validators...); len(err) > 0 {
		f.AddErrors(err...)
	}

	return !f.hasErrors()
}

// ChoicesField implements [ChoicesProvider]. It can be used to augment
// a [Field] for comparable types.
type ChoicesField[T comparable] struct {
	choices ValueChoices[T]
}

// Choices returns the stored choices.
func (f *ChoicesField[T]) Choices() ValueChoices[T] {
	return f.choices
}

// SetChoices sets the stored choices.
func (f *ChoicesField[T]) SetChoices(choices ValueChoices[T]) {
	f.choices = choices
}

// TextField is a field that holds a [string] value.
type TextField struct {
	Field[string, StringValue]
	ChoicesField[string]
}

// BooleanField is a field that holds a [bool] value.
type BooleanField = Field[bool, BooleanValue]

// NumberField is a field that holds a given number type.
type NumberField[T numberType] struct {
	Field[T, NumberValue[T]]
	ChoicesField[T]
}

// IntegerField is a field that holds an [int] value.
type IntegerField = NumberField[int]

// DatetimeField is a field that holds a [time.Time] value.
type DatetimeField = Field[time.Time, DatetimeValue]

// URLField is a field that holds a [url.URL] value.
type URLField = Field[url.URL, URLValue]

/*  List fields
    --------------------------------------------------------------- */

// ListField is a list field of items T with a matching [Valuer].
// A ListField is not necessary for unmarshaling and you can simply
// use Field[[]T, ListValue[T, V]] for it to work. This type, however,
// applies the validators to each item.
type ListField[T any, V Valuer[[]T]] struct {
	Field[[]T, V]
}

// IsValid first applies [Field.IsValid] and then perform the validation
// on each item.
func (f *ListField[T, V]) IsValid() bool {
	if f.flags&ValidatedField > 0 {
		return !f.hasErrors()
	}

	if !f.Field.IsValid() { // note: this will add the [ValidatedField] flag.
		return false
	}

	for _, val := range f.value.Value() {
		f.AddErrors(ApplyValidators[T](f, val, f.validators...)...)
	}

	return !f.hasErrors()
}

// TextListField is a field that holds a list of [string] values.
type TextListField struct {
	ListField[string, ListValue[string, StringValue]]
	ChoicesField[string]
}

// NumberListField is a field that holds a list of number values.
type NumberListField[T numberType] struct {
	ListField[T, ListValue[T, NumberValue[T]]]
	ChoicesField[T]
}

// IntegerListField is a field that holds a list of [int] values.
type IntegerListField = NumberListField[int]

// DatetimeListField is a field that holds a list of [time.Time] values.
type DatetimeListField = ListField[time.Time, ListValue[time.Time, DatetimeValue]]

// URLListField is a field that holds a list of [url.URL] values.
type URLListField = ListField[url.URL, PivotListValue[url.URL, URLValue, string, StringValue]]
