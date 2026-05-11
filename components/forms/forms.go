// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package forms provide form related components.
package forms

import (
	"maps"
	"slices"
	"time"

	"github.com/a-h/templ"

	"codeberg.org/readeck/readeck/pkg/forms"
)

// Binder is a [forms.Binder] with a Name method.
type Binder interface {
	forms.Binder
	Name() string
}

// TypedBinder is a typed [Binder].
type TypedBinder[T any] interface {
	forms.TypedBinder[T]
	Binder
}

type baseWidget struct {
	name          string
	value         any
	label         string
	help          string
	required      bool
	classes       templ.CSSClasses
	controllAttrs templ.Attributes
	inputType     string
	inputClasses  templ.CSSClasses
	inputAttrs    templ.Attributes
}

// FieldOption is a function to set [baseWidget] properties.
type FieldOption func(f *baseWidget)

type fieldWidget struct {
	Binder
	baseWidget
}

func widget(field Binder, options ...FieldOption) fieldWidget {
	f := fieldWidget{
		Binder: field,
		baseWidget: baseWidget{
			name:          field.Name(),
			value:         field.V(),
			classes:       templ.CSSClasses{},
			controllAttrs: templ.Attributes{},
			inputClasses:  templ.CSSClasses{},
			inputAttrs:    templ.Attributes{},
		},
	}

	for _, option := range options {
		option(&f.baseWidget)
	}

	return f
}

func (f *fieldWidget) ariaAttrs() (attrs templ.Attributes) {
	attrs = templ.Attributes{}

	if f.help != "" {
		attrs["aria-describedby"] = "description-" + f.name
	}
	if len(f.Errors()) > 0 {
		attrs["aria-errormessage"] = "errors-" + f.name
		attrs["aria-invalid"] = "true"
	}
	return attrs
}

// Value set the field's value.
func Value(v any) FieldOption {
	return func(f *baseWidget) {
		f.value = v
	}
}

// Name sets the field's name.
func Name(v string) FieldOption {
	return func(f *baseWidget) {
		f.name = v
	}
}

// Label sets the field's label.
func Label(v string) FieldOption {
	return func(f *baseWidget) {
		f.label = v
	}
}

// Help sets the field's help.
func Help(v string) FieldOption {
	return func(f *baseWidget) {
		f.help = v
	}
}

// Required sets the field's required flag.
func Required(v bool) FieldOption {
	return func(f *baseWidget) {
		f.required = v
	}
}

// Classes sets the field's classes.
func Classes(args ...any) FieldOption {
	return func(f *baseWidget) {
		f.classes = templ.Classes(args...)
	}
}

// ControlAttrs sets the field's control attributes.
func ControlAttrs(attrs templ.Attributes) FieldOption {
	return func(f *baseWidget) {
		maps.Copy(f.controllAttrs, attrs)
	}
}

// InputType sets the field's input type.
// Works only for <input> elements.
func InputType(v string) FieldOption {
	return func(f *baseWidget) {
		f.inputType = v
	}
}

// InputClasses sets the field's input classes.
func InputClasses(args ...any) FieldOption {
	return func(f *baseWidget) {
		f.inputClasses = templ.Classes(args...)
	}
}

// InputAttr adds a attribute to the field's input.
func InputAttr(name string, value any) FieldOption {
	return func(f *baseWidget) {
		f.inputAttrs[name] = value
	}
}

// InputAttrs sets the field's input attributes.
func InputAttrs(attrs templ.Attributes) FieldOption {
	return func(f *baseWidget) {
		maps.Copy(f.inputAttrs, attrs)
	}
}

type textField struct {
	fieldWidget
}

// TextField renders a text field (text, email, etc.)
func TextField(field Binder, options ...FieldOption) templ.Component {
	return (&textField{widget(
		field,
		append([]FieldOption{InputType("text")}, options...)...,
	)}).component()
}

// DateField renders a [TextField] with a date type.
func DateField(field Binder, options ...FieldOption) templ.Component {
	if v, ok := field.V().(time.Time); ok && !v.IsZero() {
		options = slices.Insert(options, 0, Value(v.Format(time.DateOnly)))
	}
	return TextField(
		field,
		append([]FieldOption{InputType("date")}, options...)...,
	)
}

type textAreaField struct {
	fieldWidget
}

// TextAreaField renders a textarea field.
func TextAreaField(field Binder, options ...FieldOption) templ.Component {
	return (&textAreaField{widget(field, options...)}).component()
}

type checkboxField struct {
	fieldWidget
}

// CheckboxField renders a checkbox field.
func CheckboxField(field TypedBinder[bool], options ...FieldOption) templ.Component {
	return (&checkboxField{widget(field, options...)}).component()
}

type selectField[T comparable] struct {
	fieldWidget
}

// SelectField renders a select field with options.
func SelectField[T comparable](field Binder, options ...FieldOption) templ.Component {
	return (&selectField[T]{widget(field, options...)}).component()
}

type multiSelectField[T comparable] struct {
	fieldWidget
}

// MultiSelectField renders a list of checkboxes.
func MultiSelectField[T comparable](field Binder, options ...FieldOption) templ.Component {
	return (&multiSelectField[T]{widget(field, options...)}).component()
}

type passwordField struct {
	fieldWidget
}

// PasswordField renders a password field with a reveal controller.
func PasswordField(field Binder, options ...FieldOption) templ.Component {
	return (&passwordField{widget(
		field,
		append([]FieldOption{InputType("text")}, options...)...,
	)}).component()
}

type timeTokenField struct {
	fieldWidget
}

// TimeTokenField renders a field with a helper to select time tokens.
func TimeTokenField(field Binder, options ...FieldOption) templ.Component {
	return (&timeTokenField{widget(field, options...)}).component()
}
