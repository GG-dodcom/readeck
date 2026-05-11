// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package signin

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/pkg/forms"
)

var errInvalidLogin = forms.Gettext("Invalid user and/or password")

type loginForm struct {
	forms.Form
	Username forms.TextField `json:"username" validate:"trim required max_len:128"`
	Password forms.TextField `json:"password" validate:"required"`
	Redirect forms.TextField `json:"r"        validate:"trim max_len:512"`
}

func (f *loginForm) checkUser() *users.User {
	col := goqu.C("username")
	if strings.Contains(f.Username.Value(), "@") {
		// A username cannot contain a "@" so if we have one here,
		// we can check on the email instead of the username.
		col = goqu.C("email")
	}

	user, err := users.Users.GetOne(col.Eq(f.Username.Value()))
	if err != nil {
		f.AddErrors(errInvalidLogin)
		return nil
	}

	if !user.CheckPassword(f.Password.Value()) {
		f.AddErrors(errInvalidLogin)
		return nil
	}

	return user
}

type totpForm struct {
	forms.Form
	Code     forms.TextField `json:"code" validate:"required len:6"`
	Redirect forms.TextField `json:"r"    validate:"trim max_len:512"`
}

type recoverForm struct {
	forms.Form
	Step     forms.IntegerField `json:"step"     validate:"required gte:0 lte:3"`
	Email    forms.TextField    `json:"email"    validate:"trim max_len:128 only_step:0,1 required is_valid_email"`
	Password forms.TextField    `json:"password" validate:"only_step:2,3 required is_valid_password"`
	ttl      time.Duration
	prefix   string
}

func newRecoverForm(ctx context.Context) *recoverForm {
	f := forms.New[recoverForm](ctx)
	f.ttl = time.Duration(1 * time.Hour)
	f.prefix = "recover_code"

	return f
}

func (f *recoverForm) GetTaggedValidator(name, args string, _ *forms.TagContext) (forms.Validator, bool) {
	if name != "only_step" {
		return nil, false
	}
	steps := strings.Split(args, ",")

	return forms.FieldValidatorFunc(func(_ forms.Binder) error {
		if !slices.Contains(steps, f.Step.String()) {
			return forms.ErrSkipValidation
		}
		return nil
	}), true
}
