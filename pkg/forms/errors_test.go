// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package forms_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/pkg/forms"
)

type prefixTranslator string

func (tr prefixTranslator) Gettext(s string, vars ...interface{}) string {
	return fmt.Sprintf("%s:%s", tr, fmt.Sprintf(s, vars...))
}

func (tr prefixTranslator) Pgettext(ctx string, str string, vars ...interface{}) string {
	return fmt.Sprintf("%s:%s", ctx, tr.Gettext(str, vars...))
}

func TestErrors(t *testing.T) {
	err := forms.Errors{}
	assert.Empty(t, err.Error())

	err = forms.Errors{forms.Gettext("test")}
	assert.Equal(t, "test", err.Error())

	err = forms.Errors{errors.New("error 1"), errors.New("error 2")}
	assert.Equal(t, "error 1, error 2", err.Error())
	assert.Equal(t, "error 1, error 2", err.String())
}

func TestTranslatedErrors(t *testing.T) {
	tests := []struct {
		tr       forms.Translator
		errors   []error
		expected []string
	}{
		{
			nil,
			[]error{errors.New("test"), forms.Gettext("values %s", "a")},
			[]string{"test", "values a"},
		},
		{
			prefixTranslator("prefix"),
			[]error{errors.New("test"), forms.Gettext("values %s", "a")},
			[]string{"test", "prefix:values a"},
		},
		{
			prefixTranslator("prefix"),
			[]error{errors.New("test"), forms.Pgettext("ctx", "values %s", "a")},
			[]string{"test", "ctx:prefix:values a"},
		},
	}

	for i, test := range tests {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			assert := require.New(t)
			ctx := forms.WithTranslator(context.Background(), test.tr)

			errs := []string{}
			for err := range forms.IterErrorsTr(ctx, forms.Errors(test.errors)) {
				errs = append(errs, err.Error())
			}

			assert.Equal(test.expected, errs)
		})
	}
}

func TestIterErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected []string
	}{
		{
			"simple",
			errors.New("simple error"),
			[]string{"simple error"},
		},
		{
			"list",
			forms.Errors{errors.New("error1"), errors.New("error2")},
			[]string{"error1", "error2"},
		},
		{
			"nested",
			forms.Errors{
				errors.New("errorA"),
				forms.Errors{
					errors.New("errorB1"),
					errors.New("errorB2"),
				},
				errors.Join(errors.New("errorC1"), errors.New("errorC2")),
				fmt.Errorf("%w: %s", errors.New("errorD"), "test"),
				fmt.Errorf("%w: %w", errors.New("errorE1"), errors.New("errorE2")),
			},
			[]string{
				"errorA",
				"errorB1", "errorB2",
				"errorC1", "errorC2",
				"errorD: test",
				"errorE1", "errorE2",
			},
		},
		{
			"form error",
			forms.Gettext("form error"),
			[]string{"translated:form error"},
		},
		{
			"form errors",
			forms.Errors{
				forms.Gettext("errorA"),
				forms.Errors{
					forms.Gettext("errorB1"),
					errors.New("errorB2"),
				},
			},
			[]string{
				"translated:errorA",
				"translated:errorB1",
				"errorB2",
			},
		},
	}

	collect := func(s iter.Seq[error]) iter.Seq[string] {
		return func(yield func(string) bool) {
			for err := range s {
				if !yield(err.Error()) {
					return
				}
			}
		}
	}

	ctx := forms.WithTranslator(context.Background(), prefixTranslator("translated"))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t,
				test.expected,
				slices.Collect(collect(forms.IterErrorsTr(ctx, test.err))),
			)
		})
	}
}

func TestIterErrorsBreak(t *testing.T) {
	err := forms.Errors{
		forms.Gettext("errorA"),
		forms.Errors{
			forms.Gettext("errorB1"),
			errors.New("errorB2"),
		},
	}
	var res []string

	for e := range forms.IterErrors(err) {
		res = []string{e.Error()}
		break
	}
	assert.Equal(t, []string{"errorA"}, res)

	ctx := forms.WithTranslator(context.Background(), prefixTranslator("prefix"))
	for e := range forms.IterErrorsTr(ctx, err) {
		res = []string{e.Error()}
		break
	}
	assert.Equal(t, []string{"prefix:errorA"}, res)
}
