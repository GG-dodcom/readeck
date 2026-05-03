// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package users

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
	"codeberg.org/readeck/readeck/pkg/glob"
)

// Error definitions.
var (
	ErrInvalidUsername  = forms.Gettext(`username is not valid`)
	ErrBlockedUsername  = forms.Gettext("username is not available")
	ErrBlockedEmailAddr = forms.ErrInvalidEmail
)

func init() {
	forms.RegisterTaggedValidator(func(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
		switch name {
		case "is_valid_password":
			return forms.TypedValidator(
				func(v string) bool {
					if strings.TrimSpace(v) == "" {
						return false
					}
					return len(v) >= 8
				},
				forms.Gettext("password must be at least 8 character long"),
			), true
		case "is_valid_email":
			return forms.ValueValidatorFunc[string](func(f forms.Binder, v string) error {
				if err := forms.IsEmail.ValidateValue(f, v); err != nil {
					return err
				}

				for _, blocked := range configs.Config.Accounts.EmailDenyList {
					if glob.Glob(blocked, v) {
						return ErrBlockedEmailAddr
					}
				}

				return nil
			}), true
		case "is_valid_username":
			return forms.ValueValidatorFunc[string](func(f forms.Binder, v string) error {
				if f.IsNil() {
					return nil
				}

				if len(v) < 3 {
					return ErrInvalidUsername
				}

				for _, x := range v {
					if unicode.Is(unicode.C, x) || unicode.Is(unicode.Space, x) {
						return ErrInvalidUsername
					}
				}

				for _, blocked := range configs.Config.Accounts.UsernameDenyList {
					if glob.Glob(blocked, v) {
						return ErrBlockedUsername
					}
				}

				if !strings.ContainsRune(v, '@') {
					return nil
				}

				// Username contains "@". There must be an email field and both
				// values must be equal.
				email, ok := tc.Form.Fields()["email"]
				if !ok || email.V() != f.V() {
					return ErrInvalidUsername
				}

				return nil
			}), true
		case "group_choices":
			g := GroupList(forms.GetTranslator(tc.Context), "@group", nil)
			choices := make([]forms.ValueChoice[string], 0, len(g)+1)
			choices = append(choices, forms.Choice(
				forms.GetTranslator(tc.Context).Pgettext("role", "no group"),
				"none",
			))

			for _, x := range g {
				choices = append(choices, forms.Choice(x[1], x[0]))
			}
			forms.Choices(tc.Field, choices...)
			tc.Field.(*forms.TextField).Set("user")

			return nil, true
		default:
			return nil, false
		}
	})
}

// ProvisioningForm is a form that can provision a new user.
type ProvisioningForm struct {
	forms.Form
	Username forms.TextField `json:"username" validate:"trim is_valid_username"`
	Email    forms.TextField `json:"email"    validate:"trim is_valid_email"`
	Group    forms.TextField `json:"group"    validate:"required_or_nil group_choices"`
}

// LoadUser loads a user based on its username or email.
// When it exists, there must be only one result for the tupple username + email.
// If the user needs an update, a non empty [goqu.Record] is returned so any process
// calling this method can perform the update.
// When the user doesn't exist, the returned [User] has an ID 0 and can be immediately
// created with [Users.Create]. It already contains a generated password.
func (f *ProvisioningForm) LoadUser(username, email, group string) (*User, goqu.Record, error) {
	values := url.Values{"username": {username}, "email": {email}}
	if group != "" {
		values.Set("group", group)
	}

	forms.BindValues(values, f)

	if !f.IsValid() {
		if len(f.Errors()) > 0 {
			return nil, nil, f.Errors()
		}
		for name, field := range f.Fields() {
			if len(field.Errors()) > 0 {
				return nil, nil, forms.Errors{fmt.Errorf("%s: %s", name, field.Errors())}
			}
		}
	}

	username = f.Username.Value()
	email = f.Email.Value()

	res := []*User{}
	err := Users.Query().Where(
		goqu.Or(
			goqu.C("username").Eq(username),
			goqu.C("email").Eq(email),
		),
	).ScanStructs(&res)
	if err != nil {
		return nil, nil, err
	}

	if len(res) > 1 {
		return nil, nil, fmt.Errorf("more than one user is associated with %s and %s", username, email)
	}

	user := new(User)
	rec := goqu.Record{}
	if len(res) == 0 {
		if group == "" {
			group = "user"
		}
		user.Username = username
		user.Email = email
		user.Group = group
		user.Password = MakePassword(64)
	} else {
		user = res[0]
		if user.Username != username {
			rec["username"] = username
		}
		if user.Email != email {
			rec["email"] = email
		}
		if group != "" && user.Group != group {
			rec["group"] = group
		}
	}

	return user, rec, nil
}
