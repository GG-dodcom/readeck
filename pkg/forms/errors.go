// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
)

// Errors is an error list.
type Errors []error

func (e Errors) Error() string {
	return e.String()
}

func (e Errors) String() string {
	if len(e) == 0 {
		return ""
	}

	res := make([]string, len(e))
	for i, x := range e {
		res[i] = x.Error()
	}
	return strings.Join(res, ", ")
}

// MarshalJSON implements [json.Marshaler].
func (e Errors) MarshalJSON() ([]byte, error) {
	if len(e) == 0 {
		return json.Marshal(nil)
	}

	res := make([]string, len(e))
	for i := range e {
		res[i] = e[i].Error()
	}

	return json.Marshal(res)
}

func (e Errors) Unwrap() []error {
	return e
}

// IterErrors returns an iterator over an error and flatens the result.
// It recursively yields errors contained in the error when it implements
// Unwrap() []error (like [errors.Join] or [Errors] do).
// Every result is wrapped in a [localizedError] so its call to Error()
// produces a translated error.
func IterErrors(err error) iter.Seq[error] {
	return func(yield func(error) bool) {
		if !pushErrors(err, yield) {
			return
		}
	}
}

// IterErrorsTr returns an iterator that yields errors wrapped as
// localized error so they can be translated when calling their
// Error() method.
func IterErrorsTr(ctx context.Context, err error) iter.Seq[error] {
	return func(yield func(error) bool) {
		for err := range IterErrors(err) {
			if !yield(WrapTrError(ctx, err)) {
				return
			}
		}
	}
}

func pushErrors(err error, yield func(error) bool) bool {
	if err, ok := err.(interface{ Unwrap() []error }); ok {
		for _, err := range err.Unwrap() {
			if !pushErrors(err, yield) {
				return false
			}
		}
		return true
	}

	return yield(err)
}
