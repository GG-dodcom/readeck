// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package users

import (
	"errors"
	"strings"
	"unicode"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/pkg/forms"
	"codeberg.org/readeck/readeck/pkg/glob"
)

// FIXME: remove this when dependent packages are migrated.

// IsValidPassword is the password validation rule.
var IsValidPassword = forms.TypedValidator(func(v string) bool {
	if strings.TrimSpace(v) == "" {
		return false
	}
	return len(v) >= 8
}, errors.New("password must be at least 8 character long"))

// IsValidUsername is the username validator.
// A valid username contains at least 3 characters from [a-z0-9_-]
// and start with a letter.
var IsValidUsername = forms.ValueValidatorFunc[string](func(f forms.Field, v string) error {
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

	return nil
})

// IsValidUserEmail is the user's email address validator.
var IsValidUserEmail = forms.ValueValidatorFunc[string](func(f forms.Field, v string) error {
	if err := forms.IsEmail.ValidateValue(f, v); err != nil {
		return err
	}

	for _, blocked := range configs.Config.Accounts.EmailDenyList {
		if glob.Glob(blocked, v) {
			return ErrBlockedEmailAddr
		}
	}

	return nil
})
