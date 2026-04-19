// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package signin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/bus"
	"codeberg.org/readeck/readeck/internal/email"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms"
)

const (
	rCodeSize     = 6
	rVerifierSize = 12
)

type recoverForm struct {
	*forms.Form
	ttl    time.Duration
	prefix string
}

// recoverCode is the data stored in the K/V store.
// We only store the user ID and a hash of the code+verifier.
// The code (base64 encoded) is the key in the K/V store.
type recoverCode struct {
	UserID   int                 `json:"u"`
	Hash     [sha256.Size]byte   `json:"h"`
	code     [rCodeSize]byte     // not stored
	verifier [rVerifierSize]byte // not stored
}

func newCode(userID int) *recoverCode {
	c := &recoverCode{UserID: userID}
	rand.Read(c.code[:])
	rand.Read(c.verifier[:])

	h := sha256.New()
	h.Write(c.code[:])
	h.Write(c.verifier[:])
	c.Hash = [sha256.Size]byte(h.Sum(nil))

	return c
}

func (c *recoverCode) String() string {
	r := make([]byte, rCodeSize+rVerifierSize)
	copy(r[0:rCodeSize], c.code[:])
	copy(r[rCodeSize:], c.verifier[:])
	return base64.RawURLEncoding.EncodeToString(r)
}

// save saves the recover code in the K/V store using the code
// as a key.
func (c *recoverCode) save(prefix string, ttl time.Duration) error {
	return bus.Set(prefix+"_"+c.key(), c, ttl)
}

// load retrieves the [recoverCode] from the K/V store
// using the full recovery code (code+verifier).
// It then checks that the input code hash matches the stored
// one (when it's found).
func (c *recoverCode) load(prefix string, code string) error {
	data, err := base64.RawURLEncoding.DecodeString(code)
	if err != nil {
		return err
	}
	if len(data) != rCodeSize+rVerifierSize {
		return errors.New("invalid code size")
	}

	if err = bus.Get(
		prefix+"_"+base64.RawURLEncoding.EncodeToString(data[0:rCodeSize]),
		c,
	); err != nil {
		return err
	}
	if c.UserID == 0 {
		return errors.New("code not found")
	}

	h := sha256.New()
	h.Write(data[0:rCodeSize])
	h.Write(data[rCodeSize:])
	if subtle.ConstantTimeCompare(h.Sum(nil), c.Hash[:]) != 1 {
		return errors.New("invalid code")
	}

	c.code = [rCodeSize]byte(data[0:rCodeSize])
	return nil
}

func (c *recoverCode) delete(prefix string) error {
	return bus.Store().Del(prefix + "_" + c.key())
}

func (c *recoverCode) key() string {
	return base64.RawURLEncoding.EncodeToString(c.code[:])
}

func newRecoverForm(tr forms.Translator) *recoverForm {
	return &recoverForm{
		Form: forms.Must(
			forms.WithTranslator(context.Background(), tr),
			forms.NewIntegerField("step",
				forms.Required,
				forms.TypedValidator(func(v int) bool {
					return 0 <= v || v <= 3
				}, errors.New("invalid step")),
			),
			forms.NewTextField("email",
				forms.Trim,
				forms.When(func(f forms.Field, _ string) bool {
					step := forms.GetForm(f).Get("step").(forms.TypedField[int]).V()
					return step == 0 || step == 1
				}).
					True(forms.Required),
				forms.MaxLen(128),
			),
			forms.NewTextField("password",
				forms.When(func(f forms.Field, _ string) bool {
					step := forms.GetForm(f).Get("step").(forms.TypedField[int]).V()
					return step == 2 || step == 3
				}).
					True(forms.Required, users.IsValidPassword),
			),
		),
		ttl:    time.Duration(1 * time.Hour),
		prefix: "recover_code",
	}
}

func (h *authHandler) recover(w http.ResponseWriter, r *http.Request) {
	f := newRecoverForm(server.Locale(r))
	f.Get("step").Set(0)

	var recoverErr error
	userCode := chi.URLParam(r, "code")

	step0 := func() {
		if !f.IsValid() {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}

		user, err := users.Users.GetOne(goqu.C("email").Eq(f.Get("email").String()))

		defer func() {
			if err != nil {
				server.Log(r).Error("recover step 0", slog.Any("err", err))
				f.AddErrors("", forms.ErrUnexpected)
			}
		}()

		if err != nil && !errors.Is(err, users.ErrNotFound) {
			return
		}

		mailTc := server.TC{
			"SiteURL":   urls.AbsoluteURL(r, "/"),
			"EmailAddr": f.Get("email").String(),
		}

		if user != nil {
			code := newCode(user.ID)
			if err = code.save(f.prefix, f.ttl); err != nil {
				return
			}

			mailTc["RecoverLink"] = urls.AbsoluteURL(r, "/login/recover", code.String())
		}

		msg, err := email.NewMsg(
			configs.Config.Email.FromNoReply.String(),
			f.Get("email").String(),
			"[Readeck] Password Recovery",
			email.WithMDTemplate(
				"/emails/recover.jet.md",
				server.TemplateVars(r),
				mailTc,
			),
		)
		if err != nil {
			server.Err(w, r, err)
			return
		}

		if err = email.Sender.SendEmail(msg); err != nil {
			server.Err(w, r, err)
			return
		}

		f.Get("step").Set(1)
	}

	step2 := func() {
		var err error
		var user *users.User

		code := new(recoverCode)
		if err = code.load(f.prefix, userCode); err != nil {
			server.Log(r).Warn("load code", slog.Any("err", err))
			recoverErr = errors.New("Invalid recovery code")
			return
		}

		user, err = users.Users.GetOne(goqu.C("id").Eq(code.UserID))
		if err != nil {
			recoverErr = errors.New("Invalid recovery code")
			server.Log(r).Error("get user", slog.Any("err", err))
			return
		}

		if r.Method == http.MethodGet {
			return
		}

		if !f.IsValid() {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}

		defer func() {
			if err != nil {
				server.Log(r).Error("password update", slog.Any("err", err))
				f.AddErrors("", forms.ErrUnexpected)
			}
		}()

		if err = user.SetPassword(f.Get("password").String()); err != nil {
			return
		}
		user.SetSeed()
		if err = user.Update(goqu.Record{
			"password": user.Password,
			"seed":     user.Seed,
			"updated":  time.Now().UTC(),
		}); err != nil {
			return
		}

		if err = code.delete(f.prefix); err != nil {
			return
		}
		f.Get("step").Set(3)
	}

	switch r.Method {
	case http.MethodGet:
		if userCode != "" {
			f.Get("step").Set(2)
			step2()
		}
	case http.MethodPost:
		forms.Bind(f, r)
		switch f.Get("step").Value() {
		case 0:
			step0()
		case 1:
			// Step 1 is a template only step
			if userCode == "" || !f.IsValid() {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		case 2:
			step2()
		case 3:
			// Step 3 is a template only step
			if userCode == "" || !f.IsValid() {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}

	}

	server.RenderComponent(w, r, http.StatusOK, Views{}.recover(f, recoverErr))
}
