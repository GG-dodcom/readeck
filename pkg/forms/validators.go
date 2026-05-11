// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"
)

// Error definitions.
var (
	ErrRequired     = Gettext("field is required")
	ErrInvalidEmail = Gettext("not a valid email address")
	ErrInvalidURL   = Gettext("invalid URL")

	// ErrSkipValidation is an error that is not returned and stop
	// any subsequent validator.
	ErrSkipValidation = errors.New("skip")
)

// FatalError is an error that has the effect to stop any
// subsequent validation.
func FatalError(err error) Errors {
	return Errors{err, ErrSkipValidation}
}

// ValidatorProvider describes a type that provides a validation check method.
type ValidatorProvider interface {
	IsValid() bool
}

// Validator describes a generic validator.
// By default, it can be anything but, once attached to a field, relevant
// interfaces are called during cleanup and validation steps.
type Validator any

// ValidatorsProvider is an interface implemented by types than can
// store and return a list of [Validator].
type ValidatorsProvider interface {
	Validators() []Validator
	SetValidators(validators []Validator)
}

// ChoicesProvider is an interface implemented by types than can
// store and return [ValueChoices].
type ChoicesProvider[T comparable] interface {
	Choices() ValueChoices[T]
	SetChoices(ValueChoices[T])
}

// ValueCleaner describes a value cleaner.
type ValueCleaner[T any] interface {
	Clean(T) T
}

// FieldValidator describes a field validator (not its value).
type FieldValidator interface {
	ValidateField(f Binder) error
}

// ValueValidator describes a value validator.
type ValueValidator[T any] interface {
	ValidateValue(f Binder, v T) error
}

// CleanerFunc is a [ValueCleaner].
type CleanerFunc[T any] func(v T) T

// Clean implements [ValueCleaner].
func (c CleanerFunc[T]) Clean(v T) T {
	return c(v)
}

// FieldValidatorFunc is a [FieldValidator].
type FieldValidatorFunc func(f Binder) error

// ValidateField implements [FieldValidator].
func (c FieldValidatorFunc) ValidateField(f Binder) error {
	return c(f)
}

// ValueValidatorFunc is a [ValueValidator].
type ValueValidatorFunc[T any] func(f Binder, v T) error

// ValidateValue implements [ValueValidator].
func (c ValueValidatorFunc[T]) ValidateValue(f Binder, v T) error {
	return c(f, v)
}

// TypedValidator is a helper function that returns a [ValueValidator] from a
// validation function and an error message.
// The resulting validator only applies on a bound or not null field.
func TypedValidator[T any](validator func(T) bool, err error) ValueValidator[T] {
	return ValueValidatorFunc[T](func(f Binder, v T) error {
		if !f.IsBound() || f.IsNil() {
			return nil
		}

		if !validator(v) {
			return err
		}
		return nil
	})
}

// ApplyCleaners applies the p's [ValueCleaner]s.
// It returns the cleaned up value.
func ApplyCleaners[T any](p ValidatorsProvider, v T) T {
	for _, validator := range p.Validators() {
		if cleaner, ok := validator.(ValueCleaner[T]); ok {
			v = cleaner.Clean(v)
		}
	}
	return v
}

// ApplyValidators applies the given validators to the field
// and returns all found errors.
// It can run at any time, including during a form or field
// custom validation.
//
// Every [FieldValidator] is applied.
// Every [ValueValidator] for T and is applied.
//
// A validator that returns a [FatalError] or [ErrSkipValidation] will stops any further
// validation.
func ApplyValidators[T any](f Binder, v any, validators ...Validator) (errs []error) {
	// Don't validate a field with [ErrInvalidValue]
	if err := f.Errors(); len(err) == 1 && errors.Is(err[0], ErrInvalidValue) {
		return nil
	}

	for _, validator := range validators {
		var fn func() error

		switch validator := validator.(type) {
		case FieldValidator:
			fn = func() error {
				return validator.ValidateField(f)
			}
		case ValueValidator[T]:
			if v, ok := v.(T); ok {
				fn = func() error {
					return validator.ValidateValue(f, v)
				}
			}
		case ValueValidator[any]:
			fn = func() error {
				return validator.ValidateValue(f, v)
			}
		}

		if fn != nil {
			err := fn()
			if err == nil {
				continue
			}

			// We unwrap the error as a list so we can catch
			// any embedded skip or fatal error.
			for err := range IterErrors(err) {
				// A [ErrSkipValidation] stops the validation
				// and doesn't register the error.
				if errors.Is(err, ErrSkipValidation) {
					return errs
				}
				errs = append(errs, err)
			}
		}
	}

	return errs
}

// Trim is a [ValueCleaner] that trims spaces on string values.
var Trim = CleanerFunc[string](strings.TrimSpace)

// DiscardEmpty is a [ValueCleaner] that removes empty values
// from a list of string.
var DiscardEmpty = CleanerFunc[[]string](func(v []string) []string {
	return slices.DeleteFunc(v, func(s string) bool {
		return s == ""
	})
})

// Required is a [FieldValidator] that returns an error when a field is null, not bound or empty.
var Required = FieldValidatorFunc(func(f Binder) error {
	if !f.IsBound() || f.IsEmpty() || f.IsNil() {
		return FatalError(ErrRequired)
	}
	return nil
})

// RequiredOrNil is a [FieldValidator] that returns an error when the field is empty but not null.
var RequiredOrNil = FieldValidatorFunc(func(f Binder) error {
	if !f.IsNil() && f.IsEmpty() {
		return FatalError(ErrRequired)
	}
	return nil
})

// Skip skips subsequent validators when the field is null or empty.
var Skip = FieldValidatorFunc(func(f Binder) error {
	if f.IsNil() || f.IsEmpty() || f.String() == "" {
		return ErrSkipValidation
	}
	return nil
})

// IsEmail performs a rough check of the email address. That is, it
// only checks for the presence of "@", only once and in the string.
// Control characters and spaces are not allowed.
var IsEmail = TypedValidator(func(v string) bool {
	l := len(v)
	c := 0

	for i, x := range v {
		if x == '@' {
			if c > 0 {
				return false
			}
			if i == 0 {
				return false
			}
			if i == l-1 {
				return false
			}
			c++
		}

		if unicode.Is(unicode.C, x) || unicode.Is(unicode.Space, x) {
			return false
		}
	}

	return c == 1
}, ErrInvalidEmail)

// IsURL checks that the input value is a valid URL
// that matches the given schemes and has a hostname.
// It works on [string] and [url.URL] values.
func IsURL(schemes ...string) ValueValidator[any] {
	if len(schemes) == 0 {
		schemes = []string{"http", "https"}
	}

	checkURL := func(u *url.URL) bool {
		if !slices.Contains(schemes, u.Scheme) {
			return false
		}
		return u.Hostname() != ""
	}

	return TypedValidator(func(v any) bool {
		switch v := v.(type) {
		case string:
			u, err := url.Parse(v)
			if err != nil {
				return false
			}
			return checkURL(u)
		case url.URL:
			return checkURL(&v)
		default:
			return false
		}
	}, ErrInvalidURL)
}

// Gte is an integer validator that checks
// if a value is greater or equal than a parameter.
func Gte(n float64) ValueValidator[any] {
	return TypedValidator(func(v any) bool {
		switch v := v.(type) {
		case float64:
			return v >= n
		case int:
			return float64(v) >= n
		case uint:
			return float64(v) >= n
		default:
			return true
		}
	}, Gettext("must be greater or equal than %g", n))
}

// Lte is an integer validator that checks
// if a value is lower or equal than a parameter.
func Lte(n float64) ValueValidator[any] {
	return TypedValidator(func(v any) bool {
		switch v := v.(type) {
		case float64:
			return v <= n
		case int:
			return float64(v) <= n
		case uint:
			return float64(v) <= n
		default:
			return true
		}
	}, Gettext("must be lower or equal than %g", n))
}

// MinLen is a string validator thats checks
// if it contains at least n characters.
func MinLen(n int) ValueValidator[string] {
	return TypedValidator(func(s string) bool {
		return len([]rune(s)) >= n
	}, Gettext("text must contain at least %d characters", n))
}

// MaxLen is a string validator thats checks
// if it contains at most n characters.
func MaxLen(n int) ValueValidator[string] {
	return TypedValidator(func(s string) bool {
		return len([]rune(s)) <= n
	}, Gettext("text must contain at most %d characters", n))
}

// Len is a string validator thats checks
// if it contains exactly n characters.
func Len(n int) ValueValidator[string] {
	return TypedValidator(func(s string) bool {
		return len([]rune(s)) == n
	}, Gettext("text must contain %d characters", n))
}

// SplitLines works on any []string value and populates
// the field after spliting each item's lines.
// It will trim spaces on each value.
var SplitLines = ValueValidatorFunc[[]string](func(f Binder, value []string) error {
	field, ok := f.(Setter[[]string])
	if !ok {
		return nil
	}

	res := []string{}
	for _, x := range value {
		for l := range strings.Lines(x) {
			if s := strings.TrimSpace(l); s != "" {
				res = append(res, s)
			}
		}
	}
	field.Set(res)
	return nil
})

// ValueChoice is a key/value pair used by [ValueChoices].
type ValueChoice[T comparable] struct {
	Name  string
	Value T
}

// ValueChoices is a key/value pair and also a [ValueValidator].
// It can be set directly as a validator to limit the possible
// choices.
// To set the validator and have the choice list available,
// see [Choices].
type ValueChoices[T comparable] []ValueChoice[T]

func (c ValueChoices[T]) String() string {
	res := make([]string, len(c))
	for i, x := range c {
		res[i] = fmt.Sprintf(`"%v"`, x.Value)
	}

	return strings.Join(res, ", ")
}

// In returns true when the choice is present in a list of values.
func (c ValueChoice[T]) In(values []T) bool {
	return slices.ContainsFunc(values, func(x T) bool {
		return x == c.Value
	})
}

// ValidateValue checks that the value v exists in the choices.
func (c ValueChoices[T]) ValidateValue(f Binder, v T) error {
	if f.IsNil() {
		return nil
	}
	for _, choice := range c {
		if choice.Value == v {
			return nil
		}
	}
	return Gettext("%v is not one of %s", v, c)
}

// Choice returns a new [ValueChoice] instance.
func Choice[T comparable](name string, value T) ValueChoice[T] {
	return ValueChoice[T]{Name: name, Value: value}
}

// Choices adds a list of [ValueChoice] to the field f.
// If it implements [ChoicesProvider], the choice list is added to the field.
//
// When it implements [ValidatorProvider], a validator is added
// so only valid choices are accepted.
func Choices[T comparable](f Binder, choices ...ValueChoice[T]) {
	if f, ok := f.(ChoicesProvider[T]); ok {
		f.SetChoices(choices)
	}

	if f, ok := f.(ValidatorsProvider); ok {
		f.SetValidators(append(f.Validators(), ValueChoices[T](choices)))
	}
}
