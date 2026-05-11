// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"context"
	"errors"
	"fmt"
)

type ctxTranslatorKey struct{}

// WithTranslator adds a [Translator] to the given [context.Context].
func WithTranslator(ctx context.Context, tr Translator) context.Context {
	return context.WithValue(ctx, ctxTranslatorKey{}, tr)
}

// GetTranslator returns the [Translator] from the context.
func GetTranslator(ctx context.Context) Translator {
	if ctx == nil {
		return NoopTranslator{}
	}

	if tr, ok := ctx.Value(ctxTranslatorKey{}).(Translator); ok {
		return tr
	}
	return NoopTranslator{}
}

// Translator describes a type that implements a translation method.
type Translator interface {
	Gettext(string, ...interface{}) string
	Pgettext(ctx, str string, vars ...interface{}) string
}

// ContextHolder is an interface implemented by types that can carry a [context.Context].
type ContextHolder interface {
	SetContext(context.Context)
}

// TranslatorProvider describes a type that can store and
// return a [Translator].
type TranslatorProvider interface {
	Translator() Translator
	SetTranslator(Translator)
}

// NoopTranslator is a dummy translator.
type NoopTranslator struct{}

// Gettext implements [Translator].
func (NoopTranslator) Gettext(s string, args ...any) string {
	return fmt.Sprintf(s, args...)
}

// Pgettext implements [Translator].
func (NoopTranslator) Pgettext(_, s string, args ...any) string {
	return fmt.Sprintf(s, args...)
}

// FormError is a form's or field's error that contains an error message
// and arguments.
type FormError struct {
	ctx  string
	err  error
	args []any
}

// Error returns the untranslated error.
func (p FormError) Error() string {
	return fmt.Sprintf(p.err.Error(), p.args...)
}

// Is implements error identification.
func (p FormError) Is(err error) bool {
	return errors.Unwrap(err) == p.err
}

// Unwrap implements error unwrap.
func (p FormError) Unwrap() error {
	return p.err
}

// Translate returns the translated error using
// the given translator.
func (p FormError) Translate(tr Translator) string {
	if p.ctx == "" {
		return tr.Gettext(p.err.Error(), p.args...)
	}
	return tr.Pgettext(p.ctx, p.err.Error(), p.args...)
}

// newError returns a new FormError.
func newError(msg string, args ...any) FormError {
	return FormError{err: errors.New(msg), args: args}
}

func newErrorCtx(ctx string, msg string, args ...any) FormError {
	return FormError{ctx: ctx, err: errors.New(msg), args: args}
}

var (
	// Gettext is an alias for newError (for locales extractor).
	Gettext = newError
	// Pgettext is an alias for newError (for locales extractor).
	Pgettext = newErrorCtx
)

// localizedError associates an error with a translator.
// If the error implements a Translate(translator) method
// it will be used to translate the error when it's added
// to the form's error list.
type localizedError struct {
	err error
	tr  Translator
}

func (le localizedError) Unwrap() error {
	return le.err
}

func (le localizedError) Error() string {
	if le.tr == nil {
		return le.err.Error()
	}

	if err, ok := le.err.(interface{ Translate(Translator) string }); ok && le.tr != nil {
		return err.Translate(le.tr)
	}

	return le.err.Error()
}

// WrapTrError wraps a given error into a new l10n aware error.
func WrapTrError(ctx context.Context, err error) error {
	if _, ok := err.(localizedError); ok {
		return err
	}
	tr := GetTranslator(ctx)
	if tr == nil {
		return err
	}

	return localizedError{
		err: err,
		tr:  tr,
	}
}
