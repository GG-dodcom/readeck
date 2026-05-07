// SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package onboarding

import (
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/pkg/forms"
)

type onboardingForm struct {
	forms.Form
	Username forms.TextField `json:"username" validate:"trim required max_len:128 is_valid_username"`
	Email    forms.TextField `json:"email"    validate:"trim skip max_len:128 is_valid_email"`
	Password forms.TextField `json:"password" validate:"required is_valid_password"`
}

func (f *onboardingForm) createUser(language string) (*users.User, error) {
	u := &users.User{
		Username: f.Username.Value(),
		Email:    f.Email.Value(),
		Password: f.Password.Value(),
		Group:    "admin",
		Settings: &users.UserSettings{
			Lang: language,
		},
	}

	if u.Email == "" {
		u.Email = u.Username + "@localhost"
	}

	err := users.Users.Create(u)
	if err != nil {
		f.AddErrors(forms.ErrUnexpected)
	}

	return u, err
}
