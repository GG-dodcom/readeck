// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package profile

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"golang.org/x/text/language"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/tokens"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/portability"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/sessions"
	"codeberg.org/readeck/readeck/locales"
	"codeberg.org/readeck/readeck/pkg/forms"
	"codeberg.org/readeck/readeck/pkg/totp"
)

type (
	ctxUserFormKey struct{}
)

// profileForm is the form used by the profile update routes.
type profileForm struct {
	*forms.Form
}

var (
	errInvalidUserOrEmail = forms.Gettext("invalid username and/or email")
	errInvalidPassword    = forms.Gettext("invalid password")
)

// newProfileForm returns a ProfileForm instance.
func newProfileForm(tr forms.Translator) *profileForm {
	var tag language.Tag
	if tr, ok := tr.(*locales.Locale); ok {
		tag = tr.Tag
	} else {
		tag = language.Make("en")
	}

	return &profileForm{forms.Must(
		forms.WithTranslator(context.Background(), tr),
		forms.NewTextField("username",
			forms.Trim, forms.RequiredOrNil, forms.MaxLen(128), users.IsValidUsername),
		forms.NewTextField("email",
			forms.Trim, forms.RequiredOrNil, forms.MaxLen(128), users.IsValidUserEmail),
		forms.NewTextField("settings_lang",
			forms.Trim,
			forms.ChoicesPairs(locales.Available(tag)),
		),
		forms.NewBooleanField("settings_addon_reminder"),
		forms.NewTextField("settings_email_reply_to",
			forms.Trim, forms.Optional[string](forms.IsEmail)),
		forms.NewTextField("settings_email_epub_to",
			forms.Trim, forms.Optional[string](forms.IsEmail)),
		forms.NewIntegerField("settings_reader_width",
			forms.RequiredOrNil, forms.Gte(1), forms.Lte(3),
		),
		forms.NewTextField("settings_reader_font",
			forms.Trim, forms.RequiredOrNil, forms.MaxLen(64),
		),
		forms.NewIntegerField("settings_reader_font_size",
			forms.RequiredOrNil, forms.Gte(1), forms.Lte(6),
		),
		forms.NewIntegerField("settings_reader_line_height",
			forms.RequiredOrNil, forms.Gte(1), forms.Lte(6),
		),
		forms.NewIntegerField("settings_reader_justify"),
		forms.NewIntegerField("settings_reader_hyphenation"),
	)}
}

// setUser sets the form's values from a user instance.
func (f *profileForm) setUser(u *users.User) {
	ctx := context.WithValue(f.Context(), ctxUserFormKey{}, u)
	f.SetContext(ctx)

	f.Get("username").Set(u.Username)
	f.Get("email").Set(u.Email)
	f.Get("settings_lang").Set(u.Lang())
	f.Get("settings_email_reply_to").Set(u.Settings.EmailSettings.ReplyTo)
	f.Get("settings_email_epub_to").Set(u.Settings.EmailSettings.EpubTo)
}

// Validate performs extra validation.
func (f *profileForm) Validate() {
	u, _ := f.Context().Value(ctxUserFormKey{}).(*users.User)

	// A username can be an email address only if both match
	username := f.Get("username").String()
	email := f.Get("email").String()
	if strings.ContainsRune(username, '@') && username != email {
		f.AddErrors("username", users.ErrInvalidUsername)
		return
	}

	for _, field := range f.Fields() {
		if !field.IsBound() || field.IsNil() {
			continue
		}

		if u == nil {
			continue
		}

		switch field.Name() {
		// check if username and/or email is not already in use.
		case "username", "email":
			c, err := users.Users.Query().Where(
				goqu.C(field.Name()).Eq(field.String()),
				goqu.C("id").Neq(u.ID),
			).Count()
			if err != nil {
				f.AddErrors("", forms.ErrUnexpected)
				return
			}
			if c > 0 {
				f.AddErrors("", errInvalidUserOrEmail)
				return
			}
		}
	}
}

// updateUser updates the given user using the form's values.
func (f *profileForm) updateUser(u *users.User) (res map[string]any, err error) {
	if !f.IsBound() {
		err = errors.New("form is not bound")
		return
	}

	resetSeed := false
	res = make(map[string]any)
	for _, field := range f.Fields() {
		if !field.IsBound() || field.IsNil() {
			continue
		}

		switch n := field.Name(); {
		case strings.HasPrefix(n, "settings_reader_"):
			name := strings.TrimPrefix(n, "settings_reader_")
			switch name {
			case "width":
				u.Settings.ReaderSettings.Width = field.Value().(int)
			case "font":
				u.Settings.ReaderSettings.Font = field.String()
			case "font_size":
				u.Settings.ReaderSettings.FontSize = field.Value().(int)
			case "line_height":
				u.Settings.ReaderSettings.LineHeight = field.Value().(int)
			case "justify":
				u.Settings.ReaderSettings.Justify = field.Value().(int)
			case "hyphenation":
				u.Settings.ReaderSettings.Hyphenation = field.Value().(int)
			}
			res["settings"] = u.Settings
		case strings.HasPrefix(n, "settings_email_"):
			name := strings.TrimPrefix(n, "settings_email_")
			switch name {
			case "reply_to":
				u.Settings.EmailSettings.ReplyTo = field.String()
			case "epub_to":
				u.Settings.EmailSettings.EpubTo = field.String()
			}
			res["settings"] = u.Settings
		case n == "settings_lang":
			u.Settings.Lang = field.String()
			res["settings"] = u.Settings
		case n == "settings_addon_reminder":
			u.Settings.AddonReminder = field.Value().(bool)
			res["settings"] = u.Settings
		case n == "email" && field.String() != u.Email:
			if !u.Locked() {
				res["email"] = field.String()
				resetSeed = true
			}
		case n == "username" && field.String() != u.Username:
			if !u.Locked() {
				res["username"] = field.String()
				resetSeed = true
			}
		default:
			res[field.Name()] = field.Value()
		}

	}

	if len(res) > 0 {
		res["updated"] = time.Now().UTC()
		if resetSeed {
			res["seed"] = u.SetSeed()
		}
		if err = u.Update(res); err != nil {
			f.AddErrors("", forms.ErrUnexpected)
			return
		}

	}
	res["id"] = u.ID
	delete(res, "seed")
	return
}

// changePasswordForm is a form to update a user's password.
type changePasswordForm struct {
	*forms.Form
}

// newChangePasswordForm returns a [changePasswordForm] instance.
func newChangePasswordForm(tr forms.Translator, user *users.User) *changePasswordForm {
	ctx := context.WithValue(context.Background(), ctxUserFormKey{}, user)
	ctx = forms.WithTranslator(ctx, tr)

	return &changePasswordForm{forms.Must(
		ctx,
		forms.NewTextField("current",
			forms.Required,
			forms.ValueValidatorFunc[string](func(f forms.Field, value string) error {
				// If a user was passed in context, then "current"
				// is mandatory and must match the current user
				// password.
				user := forms.GetForm(f).Context().Value(ctxUserFormKey{}).(*users.User)

				if !user.CheckPassword(value) {
					return errInvalidPassword
				}

				return nil
			})),
		forms.NewTextField("password",
			forms.Required, users.IsValidPassword,
		),
	)}
}

// updatePassword performs the user's password update.
func (f *changePasswordForm) updatePassword() (err error) {
	defer func() {
		if err != nil {
			f.AddErrors("", forms.ErrUnexpected)
		}
	}()

	user := f.Context().Value(ctxUserFormKey{}).(*users.User)
	if err = user.SetPassword(f.Get("password").String()); err != nil {
		return
	}
	err = user.Update(goqu.Record{
		"password": user.Password,
		"seed":     user.SetSeed(),
		"updated":  time.Now().UTC(),
	})
	return
}

// totpForm manages user's TOTP.
type totpForm struct {
	*forms.Form
	code totp.Code
}

// newTOTPForm returns a new [totpForm] instance.
func newTOTPForm(tr forms.Translator, user *users.User) *totpForm {
	ctx := context.WithValue(context.Background(), ctxUserFormKey{}, user)
	ctx = forms.WithTranslator(ctx, tr)

	return &totpForm{
		Form: forms.Must(
			ctx,
			forms.NewTextField("secret"),
			forms.NewTextField("otp", forms.Required, forms.Len(6)),
		),
	}
}

// Validate sets the code from the secret.
func (f *totpForm) Validate() {
	if f.Get("secret").IsEmpty() {
		f.AddErrors("", errors.New("secret missing"))
		return
	}

	f.code = totp.NewCode(f.Get("secret").String())
	if f.IsValid() {
		ok, err := f.code.Verify(f.Get("otp").String(), time.Now().UTC(), 1)
		if err != nil {
			f.AddErrors("", forms.ErrUnexpected)
			return
		}
		if !ok {
			f.AddErrors("otp", forms.Gettext("Invalid code"))
		}
	}
}

// generate creates a new [totp.Code] an sets the secret field.
func (f *totpForm) generate() {
	f.code = totp.Generate()
	f.code.Issuer = "Readeck"
	f.code.Account = f.Context().Value(ctxUserFormKey{}).(*users.User).Username
	f.Get("secret").Set(f.code.Secret)
}

// save performs the user's totp update.
func (f *totpForm) save() error {
	user := f.Context().Value(ctxUserFormKey{}).(*users.User)
	if err := user.SetTOTPCode(&f.code); err != nil {
		return err
	}

	return user.Update(goqu.Record{
		"totp_secret": user.TOTPSecret,
		"seed":        user.SetSeed(),
		"updated":     time.Now().UTC(),
	})
}

type sessionPrefForm struct {
	*forms.Form
}

func newSessionPrefForm(tr forms.Translator) *sessionPrefForm {
	return &sessionPrefForm{forms.Must(
		forms.WithTranslator(context.Background(), tr),
		forms.NewTextField(
			"bookmark_list_display",
			forms.Skip,
			forms.Choices(
				forms.Choice("grid", "grid"),
				forms.Choice("compact", "compact"),
				forms.Choice("mosaic", "mosaic"),
			),
		),
		forms.NewBooleanField(
			"bookmark_sidebar_hidden",
			forms.Skip,
		),
	)}
}

func (f sessionPrefForm) updateSession(payload *sessions.Payload) (res map[string]any, err error) {
	if !f.IsBound() {
		err = errors.New("form is not bound")
		return
	}

	res = make(map[string]any)
	for _, field := range f.Fields() {
		if !field.IsBound() || field.IsNil() {
			continue
		}

		n := field.Name()
		switch n {
		case "bookmark_list_display":
			payload.Preferences.BookmarkListDisplay = field.String()
			res[n] = field.String()
		case "bookmark_sidebar_hidden":
			payload.Preferences.BookmarkSidebarHidden = field.(*forms.BooleanField).V()
			res[n] = payload.Preferences.BookmarkSidebarHidden
		}
	}

	if len(res) > 0 {
		payload.LastUpdate = time.Now().UTC()
	}

	return
}

// deleteTokenForm is the form used for token deletion.
type deleteTokenForm struct {
	*forms.Form
}

// newDeleteTokenForm returns a deleteForm instance.
func newDeleteTokenForm(tr forms.Translator) *deleteTokenForm {
	return &deleteTokenForm{forms.Must(
		forms.WithTranslator(context.Background(), tr),
		forms.NewBooleanField("cancel"),
		forms.NewTextField("_to", forms.MaxLen(512)),
	)}
}

// trigger launch the token deletion or cancel task.
func (f *deleteTokenForm) trigger(t *tokens.Token) error {
	if !f.Get("cancel").IsNil() && f.Get("cancel").Value().(bool) {
		return deleteTokenTask.Cancel(t.ID)
	}

	return deleteTokenTask.Run(t.ID, t.ID)
}

// tokenForm is the form used for token update.
type tokenForm struct {
	*forms.Form
}

// tokenForm returns a tokenForm instance.
func newTokenForm(tr forms.Translator, user *users.User) *tokenForm {
	return &tokenForm{forms.Must(
		forms.WithTranslator(context.Background(), tr),
		forms.NewTextField("application", forms.Trim, forms.Required, forms.MaxLen(128)),
		forms.NewBooleanField("is_enabled", forms.RequiredOrNil),
		forms.NewDatetimeField("expires"),
		forms.NewTextListField("roles", forms.ChoicesPairs(users.GroupList(tr, "@token_scope", user))),
	)}
}

// setToken set the token's values from an existing token.
func (f *tokenForm) setToken(t *tokens.Token) {
	f.Get("application").Set(t.Application)
	f.Get("is_enabled").Set(t.IsEnabled)
	f.Get("expires").Set(t.Expires)

	roles := make([]string, len(t.Roles))
	copy(roles, t.Roles)
	f.Get("roles").Set(roles)
}

// updateToken performs the token update.
func (f *tokenForm) updateToken(t *tokens.Token) error {
	for _, field := range f.Fields() {
		if !field.IsBound() {
			continue
		}
		switch field.Name() {
		case "application":
			t.Application = field.String()
		case "is_enabled":
			t.IsEnabled = field.(forms.TypedField[bool]).V()
		case "expires":
			if field.IsNil() {
				t.Expires = nil
				continue
			}
			v := field.(forms.TypedField[time.Time]).V()
			t.Expires = &v
		case "roles":
			if field.Value() != nil {
				t.Roles = field.(forms.TypedField[[]string]).V()
			} else {
				t.Roles = nil
			}
		}
	}

	if err := t.Save(); err != nil {
		f.AddErrors("", forms.ErrUnexpected)
		return err
	}
	return nil
}

type importForm struct {
	*forms.Form
}

func newImportForm(tr forms.Translator, user *users.User) *importForm {
	return &importForm{
		forms.Must(
			forms.WithTranslator(context.Background(), tr),
			forms.NewFileField("data", forms.Required),
			forms.NewTextField("check", forms.Trim, forms.ValueValidatorFunc[string](func(_ forms.Field, value string) error {
				if value != user.Username {
					return errors.New(tr.Gettext("username does not match"))
				}

				return nil
			})),
		),
	}
}

func (f *importForm) importFile(r *http.Request) error {
	fo := f.Get("data").(*forms.FileField).V()
	fd, err := fo.Open()
	if err != nil {
		return err
	}
	defer fd.Close() //nolint:errcheck

	zr, err := zip.NewReader(fd.(io.ReaderAt), fo.Size())
	if err != nil {
		return err
	}

	imp := portability.NewSingleUserImporter(zr, auth.GetRequestUser(r), server.Locale(r))
	imp.SetLogger(func(s string, a ...any) {
		server.Log(r).Info(fmt.Sprintf(s, a...))
	})
	return portability.Import(imp)
}
