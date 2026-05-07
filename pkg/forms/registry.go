// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"strconv"
	"strings"
)

// RequestLoaders contains the functions used to load a request's into
// a [FormBinder].
//
// Adding or removing items from [RequestLoaders] should only be done
// in a module's init function.
//
// For example, if you need to support "text/json" as "application/json", you
// can add this in a module:
//
//	func init() {
//		RequestLoaders["text/json"] = RequestLoaders[string(forms.MimeJSON)]
//	}
var RequestLoaders = map[string]func(r *http.Request, f FormBinder) error{
	// application/json
	string(MimeJSON): func(r *http.Request, f FormBinder) error {
		if err := json.NewDecoder(r.Body).Decode(f); err != nil {
			return ErrFormInvalidInput
		}
		return nil
	},
	// application/x-www-form-urlencoded
	string(MimeURLEncoded): func(r *http.Request, f FormBinder) error {
		if err := r.ParseForm(); err != nil {
			return ErrFormInvalidInput
		}
		if err := UnmarshalURLValues(r.Form, f); err != nil {
			return ErrFormInvalidInput
		}
		return nil
	},
	// multipart/form-data
	string(MimeMultipart): func(r *http.Request, f FormBinder) error {
		if err := unmarshalMultipart(r, f); err != nil {
			return ErrFormInvalidInput
		}
		return nil
	},
}

// ValidateTagName is the struct field tag name used to declare validators.
// It can be changed in an init function if needed.
var ValidateTagName = "validate"

// TagContext is the parameter passed to a tagged validation function.
type TagContext struct {
	Form    FormBinder
	Field   Binder
	Context context.Context
}

// TaggedValidatorFunc is a function called to get a [Validator] from a tag.
// The function returns a [Validator] (can be nil) and whether the name was found.
type TaggedValidatorFunc func(name, args string, tc *TagContext) (Validator, bool)

// RegisterTaggedValidator adds a new [TaggedValidatorFunc] to the tagged
// validators registry. It can be called in an init function. Any tag added
// here will be global.
func RegisterTaggedValidator(fn TaggedValidatorFunc) {
	taggedValidators = append(taggedValidators, fn)
}

// taggedValidators is a function that can be overridden to provide global
// validate tags to an application importing the forms module.
// Should be done in an init function.
var taggedValidators []TaggedValidatorFunc

// TaggedValidatorProvider is an interface that describes a type providing
// its custom tagged validators.
type TaggedValidatorProvider interface {
	GetTaggedValidator(name, args string, tc *TagContext) (Validator, bool)
}

// getTaggedValidator returns a [Validator] and whether it was found.
// If the [FormBinder] passed in "tc" implements [TaggedValidatorProvider]
// its declared validators have priority. This means, a form can override
// any default validator defined here.
func getTaggedValidator(name, args string, tc *TagContext) (Validator, bool) {
	// 1. [FormBinder]'s own tags, if any.
	if f, ok := tc.Form.(TaggedValidatorProvider); ok {
		if v, ok := f.GetTaggedValidator(name, args, tc); ok {
			return v, ok
		}
	}

	// 2. extra global tags
	for _, fn := range taggedValidators {
		if v, ok := fn(name, args, tc); ok {
			return v, ok
		}
	}

	// 3. base tags
	switch name {
	case "trim":
		return Trim, true
	case "required":
		return Required, true
	case "required_or_nil":
		return RequiredOrNil, true
	case "skip":
		return Skip, true
	case "is_email":
		return IsEmail, true
	case "is_url":
		schemes := []string{}
		for x := range strings.SplitSeq(args, ",") {
			if x := strings.TrimSpace(x); x != "" {
				schemes = append(schemes, x)
			}
		}
		return IsURL(schemes...), true
	case "discard_empty":
		return DiscardEmpty, true
	case "split_lines":
		return SplitLines, true
	case "gte":
		if n, err := strconv.ParseFloat(args, 64); err == nil {
			return Gte(n), true
		}
		return nil, false
	case "lte":
		if n, err := strconv.ParseFloat(args, 64); err == nil {
			return Lte(n), true
		}
		return nil, false
	case "len":
		if n, err := strconv.ParseInt(args, 10, 64); err == nil {
			return Len(int(n)), true
		}
		return nil, false
	case "max_len":
		if n, err := strconv.ParseInt(args, 10, 64); err == nil {
			return MaxLen(int(n)), true
		}
		return nil, false
	case "min_len":
		if n, err := strconv.ParseInt(args, 10, 64); err == nil {
			return MinLen(int(n)), true
		}
		return nil, false
	default:
		return nil, false
	}
}

func collectValidators(ctx context.Context, form FormBinder, field Binder, names ...string) iter.Seq[Validator] {
	return func(yield func(Validator) bool) {
		tc := &TagContext{Form: form, Field: field, Context: ctx}

		for _, name := range names {
			fname, args, _ := strings.Cut(name, ":")
			v, found := getTaggedValidator(fname, args, tc)
			if !found {
				panic(`Validator "` + name + `" not found`)
			}
			if v != nil {
				if !yield(v) {
					return
				}
			}
		}
	}
}
