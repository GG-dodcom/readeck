// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package profile

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/doug-martin/goqu/v9"

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

var errInvalidPassword = forms.Gettext("invalid password")

type profileForm struct {
	// TODO: move settings to a nested struct
	forms.Form
	Username                  forms.TextField    `json:"username"                    validate:"trim required_or_nil max_len:128 is_valid_username"`
	Email                     forms.TextField    `json:"email"                       validate:"trim required_or_nil max_len:128 is_valid_email"`
	SettingsLang              forms.TextField    `json:"settings_lang"               validate:"trim lang_choices"`
	SettingsAddonReminder     forms.BooleanField `json:"settings_addon_reminder"`
	SettingsEmailReplyTo      forms.TextField    `json:"settings_email_reply_to"     validate:"trim skip is_email"`
	SettingsEmailEpubTo       forms.TextField    `json:"settings_email_epub_to"      validate:"trim skip is_email"`
	SettingsReaderWidth       forms.IntegerField `json:"settings_reader_width"       validate:"required_or_nil gte:1 lte:3"`
	SettingsReaderFont        forms.TextField    `json:"settings_reader_font"        validate:"required_or_nil max_len:64"`
	SettingsReaderFontSize    forms.IntegerField `json:"settings_reader_font_size"   validate:"required_or_nil gte:1 lte:6"`
	SettingsReaderLineHeight  forms.IntegerField `json:"settings_reader_line_height" validate:"required_or_nil gte:1 lte:6"`
	SettingsReaderJustify     forms.IntegerField `json:"settings_reader_justify"`
	SettingsReaderHyphenation forms.IntegerField `json:"settings_reader_hyphenation"`
}

func (f *profileForm) GetTaggedValidator(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "lang_choices":
		var tag language.Tag
		tr := forms.GetTranslator(tc.Context)
		if tr, ok := tr.(*locales.Locale); ok {
			tag = tr.Tag
		} else {
			tag = language.Make("en")
		}

		choices := make([]forms.ValueChoice[string], 0, len(locales.Available(tag)))
		for _, x := range locales.Available(tag) {
			choices = append(choices, forms.Choice(x[1], x[0]))
		}
		forms.Choices(tc.Field, choices...)

		return nil, true
	default:
		return nil, false
	}
}

func withProfileUser(u *users.User) func(forms.FormBinder) {
	return func(b forms.FormBinder) {
		f := b.(*profileForm)

		f.Username.Set(u.Username)
		f.Email.Set(u.Email)
		f.SettingsLang.Set(u.Lang())
		f.SettingsEmailReplyTo.Set(u.Settings.EmailSettings.ReplyTo)
		f.SettingsEmailEpubTo.Set(u.Settings.EmailSettings.EpubTo)
	}
}

func (f *profileForm) Validate() error {
	user := auth.GetUser(f.Context())

	userDS := users.Users.Query().Where(
		goqu.C("username").Eq(f.Username.Value()),
	)
	emailDS := users.Users.Query().Where(
		goqu.C("email").Eq(f.Email.Value()),
	)

	if user != nil {
		userDS = userDS.Where(goqu.C("id").Neq(user.ID))
		emailDS = emailDS.Where(goqu.C("id").Neq(user.ID))
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

func (f *profileForm) update() (res map[string]any, err error) {
	if !f.IsBound() {
		err = errors.New("form is not bound")
		return
	}

	user := auth.GetUser(f.Context())
	resetSeed := false
	res = make(map[string]any)

	for name, field := range f.Fields() {
		if !field.IsBound() || field.IsNil() {
			continue
		}

		switch n := name; {
		case strings.HasPrefix(n, "settings_reader_"):
			name := strings.TrimPrefix(n, "settings_reader_")
			switch name {
			case "width":
				user.Settings.ReaderSettings.Width = field.V().(int)
			case "font":
				user.Settings.ReaderSettings.Font = field.String()
			case "font_size":
				user.Settings.ReaderSettings.FontSize = field.V().(int)
			case "line_height":
				user.Settings.ReaderSettings.LineHeight = field.V().(int)
			case "justify":
				user.Settings.ReaderSettings.Justify = field.V().(int)
			case "hyphenation":
				user.Settings.ReaderSettings.Hyphenation = field.V().(int)
			}
			res["settings"] = user.Settings
		case strings.HasPrefix(n, "settings_email_"):
			name := strings.TrimPrefix(n, "settings_email_")
			switch name {
			case "reply_to":
				user.Settings.EmailSettings.ReplyTo = field.String()
			case "epub_to":
				user.Settings.EmailSettings.EpubTo = field.String()
			}
			res["settings"] = user.Settings
		case n == "settings_lang":
			user.Settings.Lang = field.String()
			res["settings"] = user.Settings
		case n == "settings_addon_reminder":
			user.Settings.AddonReminder = field.V().(bool)
			res["settings"] = user.Settings
		case n == "email" && field.String() != user.Email:
			if !user.Locked() {
				res["email"] = field.String()
				resetSeed = true
			}
		case n == "username" && field.String() != user.Username:
			if !user.Locked() {
				res["username"] = field.String()
				resetSeed = true
			}
		default:
			res[name] = field.V()
		}
	}

	if len(res) > 0 {
		res["updated"] = time.Now().UTC()
		if resetSeed {
			res["seed"] = user.SetSeed()
		}
		if err = user.Update(res); err != nil {
			f.AddErrors(forms.ErrUnexpected)
			return
		}
	}

	res["id"] = user.UID
	delete(res, "seed")
	return res, err
}

type sessionPrefForm struct {
	forms.Form
	BookmarkListDisplay   forms.TextField    `json:"bookmark_list_display"   validate:"skip display_choices"`
	BookmarkSidebarHidden forms.BooleanField `json:"bookmark_sidebar_hidden"`
}

func (f *sessionPrefForm) GetTaggedValidator(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "display_choices":
		forms.Choices(tc.Field,
			forms.Choice("grid", "grid"),
			forms.Choice("compact", "compact"),
			forms.Choice("mosaic", "mosaic"),
		)
		return nil, true
	default:
		return nil, false
	}
}

func (f sessionPrefForm) update(payload *sessions.Payload) (res map[string]any, err error) {
	if !f.IsBound() {
		err = errors.New("form is not bound")
		return
	}

	res = make(map[string]any)
	for name, field := range f.Fields() {
		if !field.IsBound() || field.IsNil() {
			continue
		}

		switch name {
		case "bookmark_list_display":
			payload.Preferences.BookmarkListDisplay = field.String()
			res[name] = field.String()
		case "bookmark_sidebar_hidden":
			payload.Preferences.BookmarkSidebarHidden = field.V().(bool)
			res[name] = payload.Preferences.BookmarkSidebarHidden
		}
	}

	if len(res) > 0 {
		payload.LastUpdate = time.Now().UTC()
	}

	return
}

type changePasswordForm struct {
	forms.Form
	Current  forms.TextField `json:"current"  validate:"required"`
	Password forms.TextField `json:"password" validate:"required is_valid_password"`
}

func (f *changePasswordForm) Validate() error {
	user := auth.GetUser(f.Context())

	if !user.CheckPassword(f.Current.Value()) {
		f.Current.AddErrors(errInvalidPassword)
	}

	return nil
}

func (f *changePasswordForm) update() (err error) {
	defer func() {
		if err != nil {
			f.AddErrors(forms.ErrUnexpected)
		}
	}()

	user := auth.GetUser(f.Context())
	if err = user.SetPassword(f.Password.Value()); err != nil {
		return err
	}
	err = user.Update(goqu.Record{
		"password": user.Password,
		"seed":     user.SetSeed(),
		"updated":  time.Now().UTC(),
	})
	return err
}

type totpForm struct {
	forms.Form
	Secret forms.TextField `json:"secret"`
	OTP    forms.TextField `json:"otp"    validate:"required len:6"`

	code totp.Code
}

func (f *totpForm) Validate() error {
	if f.Secret.IsEmpty() {
		return errors.New("secret missing")
	}

	f.code = totp.NewCode(f.Secret.Value())
	if f.IsValid() {
		ok, err := f.code.Verify(f.OTP.Value(), time.Now().UTC(), 1)
		if err != nil {
			return forms.ErrUnexpected
		}
		if !ok {
			f.OTP.AddErrors(forms.Gettext("Invalid code"))
		}
	}

	return nil
}

// generate creates a new [totp.Code] an sets the secret field.
func (f *totpForm) generate() {
	f.code = totp.Generate()
	f.code.Issuer = "Readeck"
	f.code.Account = auth.GetUser(f.Context()).Username
	f.Secret.Set(f.code.Secret)
}

// save performs the user's totp update.
func (f *totpForm) save() error {
	user := auth.GetUser(f.Context())
	if err := user.SetTOTPCode(&f.code); err != nil {
		return err
	}

	return user.Update(goqu.Record{
		"totp_secret": user.TOTPSecret,
		"seed":        user.SetSeed(),
		"updated":     time.Now().UTC(),
	})
}

type tokenForm struct {
	forms.Form
	Application forms.TextField     `json:"application" validate:"trim required max_len:128"`
	IsEnabled   forms.BooleanField  `json:"is_enabled"  validate:"required_or_nil"`
	Expires     forms.DatetimeField `json:"expires"`
	Roles       forms.TextListField `json:"roles"       validate:"role_choices"`
}

func (f *tokenForm) GetTaggedValidator(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "role_choices":
		var user *users.User
		if info, _ := auth.CheckAuthInfo(tc.Context); !info.User.IsAnonymous() {
			user = info.User
		}

		g := users.GroupList(server.LocaleContext(tc.Context), "@token_scope", user)
		choices := make([]forms.ValueChoice[string], 0, len(g))
		for _, x := range g {
			choices = append(choices, forms.Choice(x[1], x[0]))
		}
		forms.Choices(tc.Field, choices...)
		return nil, true
	default:
		return nil, false
	}
}

// setToken set the token's values from an existing token.
func withTokenInfo(t *tokens.Token) func(f forms.FormBinder) {
	return func(b forms.FormBinder) {
		f := b.(*tokenForm)

		f.Application.Set(t.Application)
		f.IsEnabled.Set(t.IsEnabled)
		f.Roles.Set(t.Roles)
		if t.Expires != nil {
			f.Expires.Set(*t.Expires)
		}
	}
}

// update performs the token update.
func (f *tokenForm) update(t *tokens.Token) error {
	for name, field := range f.Fields() {
		if !field.IsBound() {
			continue
		}
		switch name {
		case "application":
			t.Application = field.String()
		case "is_enabled":
			t.IsEnabled = field.V().(bool)
		case "expires":
			if field.IsNil() || field.IsEmpty() {
				t.Expires = nil
				continue
			}
			t.Expires = new(field.V().(time.Time))
		case "roles":
			if field.V() != nil {
				t.Roles = field.V().([]string)
			} else {
				t.Roles = nil
			}
		}
	}

	if err := t.Save(); err != nil {
		f.AddErrors(forms.ErrUnexpected)
		return err
	}
	return nil
}

type deleteTokenForm struct {
	forms.Form
	Cancel forms.BooleanField `json:"cancel"`
	To     forms.TextField    `json:"_to"    validate:"max_len:512"`
}

// trigger launch the token deletion or cancel task.
func (f *deleteTokenForm) trigger(t *tokens.Token) error {
	if !f.Cancel.IsNil() && f.Cancel.Value() {
		return deleteTokenTask.Cancel(t.ID)
	}

	return deleteTokenTask.Run(t.ID, t.ID)
}

type importForm struct {
	forms.Form
	Data  forms.FileField `json:"data"  validate:"required"`
	Check forms.TextField `json:"check" validate:"trim required"`
}

func (f *importForm) Validate() error {
	if f.Check.Value() != auth.GetUser(f.Context()).Username {
		f.Check.AddErrors(forms.Gettext("username does not match"))
	}

	return nil
}

func (f *importForm) load(r *http.Request) error {
	fd, err := f.Data.Value().Open()
	if err != nil {
		return err
	}
	defer fd.Close() //nolint:errcheck

	zr, err := zip.NewReader(fd.(io.ReaderAt), f.Data.Value().Size())
	if err != nil {
		return err
	}

	imp := portability.NewSingleUserImporter(zr, auth.GetRequestUser(r), server.Locale(r))
	imp.SetLogger(func(s string, a ...any) {
		server.Log(r).Info(fmt.Sprintf(s, a...))
	})
	return portability.Import(imp)
}
