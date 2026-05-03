// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"errors"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type userForm struct {
	forms.Form
	Username forms.TextField `json:"username" validate:"trim optional max_len:128 is_valid_username"`
	Email    forms.TextField `json:"email"    validate:"trim optional max_len:128 is_valid_email"`
	Password forms.TextField `json:"password" validate:"is_valid_password"`
	Group    forms.TextField `json:"group"    validate:"trim optional group_choices"`

	user *users.User
}

func (f *userForm) GetTaggedValidator(name, _ string, _ *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "is_valid_password":
		// We override this validator for a password set by an admin has no size requirement.
		return forms.ValueValidatorFunc[string](func(field forms.Binder, v string) error {
			if f.user == nil {
				if err := forms.Required(field); err != nil {
					return err
				}
			}
			if field.IsBound() && v != "" && strings.TrimSpace(v) == "" {
				return forms.Gettext("password is empty")
			}
			return nil
		}), true
	case "optional":
		return forms.FieldValidatorFunc(func(field forms.Binder) error {
			if f.user == nil {
				return forms.Required(field)
			}
			return forms.RequiredOrNil(field)
		}), true
	default:
		return nil, false
	}
}

func (f *userForm) SetUser(u *users.User) {
	f.user = u
	if u != nil {
		f.Username.Set(u.Username)
		f.Email.Set(u.Email)
		f.Group.Set(u.Group)
	}
}

func (f *userForm) Validate() error {
	userDS := users.Users.Query().Where(
		goqu.C("username").Eq(f.Username.Value()),
	)
	emailDS := users.Users.Query().Where(
		goqu.C("email").Eq(f.Email.Value()),
	)

	if f.user != nil {
		userDS = userDS.Where(goqu.C("id").Neq(f.user.ID))
		emailDS = emailDS.Where(goqu.C("id").Neq(f.user.ID))
	}

	// Check that username is not already in use
	if c, err := userDS.Count(); err != nil {
		return forms.ErrUnexpected
	} else if c > 0 {
		f.Username.AddErrors(forms.Gettext("username is already in use"))
	}

	// Check that email is not already in use
	if c, err := emailDS.Count(); err != nil {
		return forms.ErrUnexpected
	} else if c > 0 {
		f.Email.AddErrors(forms.Gettext("email address is already in use"))
	}

	return nil
}

// createUser performs the user creation.
func (f *userForm) createUser() (*users.User, error) {
	u := &users.User{
		Username: f.Username.Value(),
		Email:    f.Email.Value(),
		Password: f.Password.Value(),
		Group:    f.Group.Value(),
	}

	err := users.Users.Create(u)
	if err != nil {
		f.AddErrors(forms.ErrUnexpected)
	}

	return u, err
}

// updateUser performs a user update and returns a mapping of
// updated fields.
func (f *userForm) updateUser(u *users.User) (res map[string]any, err error) {
	if !f.IsBound() {
		err = errors.New("form is not bound")
		return
	}

	res = make(map[string]any)
	for name, field := range f.Fields() {
		switch name {
		case "password":
			if field.IsNil() || strings.TrimSpace(field.String()) == "" {
				continue
			}
			p, err := u.HashPassword(field.String())
			if err != nil {
				f.AddErrors(forms.ErrUnexpected)
				return nil, err
			}
			res[name] = p
		default:
			if field.IsBound() && !field.IsNil() {
				res[name] = field.V()
			}
		}
	}

	if len(res) > 0 {
		res["updated"] = time.Now().UTC()
		res["seed"] = u.SetSeed()
		if err = u.Update(res); err != nil {
			f.AddErrors(forms.ErrUnexpected)
			return
		}
		if _, ok := res["password"]; ok {
			res["password"] = "-"
		}
	}
	res["id"] = u.ID
	delete(res, "seed")
	return
}

type deleteForm struct {
	forms.Form
	Cancel forms.BooleanField `json:"cancel"`
	To     forms.TextField    `json:"_to"`
}

// trigger launch the user deletion or cancel task.
func (f *deleteForm) trigger(u *users.User) error {
	if !f.Cancel.IsNil() && f.Cancel.Value() {
		return deleteUserTask.Cancel(u.ID)
	}

	return deleteUserTask.Run(u.ID, u.ID)
}
