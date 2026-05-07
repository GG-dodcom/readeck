// SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package users contains the models and functions to manage users.
package users

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/doug-martin/goqu/v9"
	"github.com/hlandau/passlib"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/acls"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/db/exp"
	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/locales"
	"codeberg.org/readeck/readeck/pkg/base58"
	"codeberg.org/readeck/readeck/pkg/totp"
)

func init() {
	if err := passlib.UseDefaults(passlib.Defaults20180601); err != nil {
		panic(err)
	}
}

var (
	// Users is the user manager.
	Users = Manager{}

	// ErrNotFound is returned when a user record was not found.
	ErrNotFound = errors.New("not found")
)

// User is a user record in database.
type User struct {
	locked     bool
	ID         int           `db:"id"          goqu:"skipinsert,skipupdate"`
	UID        string        `db:"uid"`
	Created    time.Time     `db:"created"     goqu:"skipupdate"`
	Updated    time.Time     `db:"updated"`
	LastLogin  time.Time     `db:"last_login"`
	Username   string        `db:"username"`
	Email      string        `db:"email"`
	Password   string        `db:"password"`
	Group      string        `db:"group"`
	Settings   *UserSettings `db:"settings"`
	Seed       int           `db:"seed"`
	TOTPSecret []byte        `db:"totp_secret"`
}

// Manager is a query helper for user entries.
type Manager struct{}

// Query returns a prepared goqu SelectDataset that can be extended later.
func (m *Manager) Query() *goqu.SelectDataset {
	return db.Q().From(goqu.T(db.TableUser).As("u")).Prepared(true)
}

// GetOne executes the a select query and returns the first result or an error
// when there's no result.
func (m *Manager) GetOne(expressions ...goqu.Expression) (*User, error) {
	var u User
	found, err := m.Query().Where(expressions...).ScanStruct(&u)

	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, ErrNotFound
	}

	return &u, nil
}

// Count returns the number of user in the database.
func (m *Manager) Count() (int64, error) {
	return db.Q().From(db.TableUser).Count()
}

// Create insert a new user in the database. The password
// must be present. It will be hashed and updated before insertion.
func (m *Manager) Create(user *User) error {
	if strings.TrimSpace(user.Password) == "" {
		return errors.New("password is empty")
	}
	hash, err := passlib.Hash(user.Password)
	if err != nil {
		return err
	}
	user.Password = hash

	user.Created = time.Now().UTC()
	user.Updated = user.Created
	user.UID = base58.NewUUID()
	user.SetSeed()

	ds := db.Q().Insert(db.TableUser).
		Rows(user).
		Prepared(true)

	id, err := db.InsertWithID(ds, "id")
	if err != nil {
		return err
	}

	user.ID = id
	return nil
}

// Update updates some user values.
func (u *User) Update(v any) error {
	if u.ID == 0 {
		return errors.New("no ID")
	}

	if v, ok := v.(goqu.Record); ok && len(v) == 0 {
		return nil
	}

	_, err := db.Q().Update(db.TableUser).Prepared(true).
		Set(v).
		Where(goqu.C("id").Eq(u.ID)).
		Executor().Exec()

	return err
}

// Save updates all the user values.
func (u *User) Save() error {
	u.Updated = time.Now().UTC()
	return u.Update(u)
}

// Delete removes a user from the database.
func (u *User) Delete() error {
	_, err := db.Q().Delete(db.TableUser).Prepared(true).
		Where(goqu.C("id").Eq(u.ID)).
		Executor().Exec()

	return err
}

// UpdateEtag implements [server.Etagger] interface.
func (u *User) UpdateEtag(h hash.Hash) {
	io.WriteString(h, u.UID+strconv.FormatInt(u.Updated.UTC().UnixNano(), 10))
}

// GetLastModified implements LastModer interface.
func (u *User) GetLastModified() []time.Time {
	return []time.Time{u.Updated}
}

// CheckPassword checks if the given password matches the
// current user password.
func (u *User) CheckPassword(password string) bool {
	newhash, err := passlib.Verify(password, u.Password)
	if err != nil {
		return false
	}

	// Update the password when needed
	if newhash != "" {
		_ = u.Update(goqu.Record{"password": newhash, "updated": time.Now().UTC()})
	}

	return true
}

// HashPassword returns a new hashed password.
func (u *User) HashPassword(password string) (string, error) {
	return passlib.Hash(password)
}

// SetPassword set a new user password. It does *not* save the user with its new hashed password.
func (u *User) SetPassword(password string) error {
	var err error
	if u.Password, err = u.HashPassword(password); err != nil {
		return err
	}

	return nil
}

// SetSeed sets a new seed to the user. It returns the seed as an integer value
// and does *not* save the data but the seed is accessible on the user instance.
func (u *User) SetSeed() int {
	s, _ := rand.Int(rand.Reader, big.NewInt(32767))
	u.Seed = int(s.Int64())
	return u.Seed
}

// HasTOTP returns true if the user has a totp secret.
func (u *User) HasTOTP() bool {
	return len(u.TOTPSecret) > 0
}

// RequiresMFA returns true if an MFA method is required upon sign-in.
func (u *User) RequiresMFA() bool {
	return u.HasTOTP()
}

// SetTOTPCode encodes the user's totp secret. It does **not** save the user.
func (u *User) SetTOTPCode(code *totp.Code) error {
	if code == nil {
		u.TOTPSecret = nil
	} else {
		data, err := configs.Keys.TOTPKey().EncodeJSON(code)
		if err != nil {
			return err
		}
		u.TOTPSecret = data
	}
	return nil
}

// IsAnonymous returns true when the instance is not set to any existing user
// (when ID is 0).
func (u *User) IsAnonymous() bool {
	return u.ID == 0
}

// Permissions returns all the user's implicit permissions.
func (u *User) Permissions() []string {
	return acls.GetPermissions(u.Group)
}

// HasPermission returns true if the user can perform "act" action
// on "obj" object.
func (u *User) HasPermission(obj, act string) bool {
	return acls.Enforce(u.Group, obj, act)
}

// Lang returns the user's language code. Unlike [UserSettings.Lang], the code
// is guaranteed to exist in the available locales. It default to "en" when no
// suitable language was found.
func (u *User) Lang() string {
	return locales.LoadTranslation(u.Settings.Lang).Tag.String()
}

// Lock locks the user's username, email and password change.
func (u *User) Lock(v bool) {
	u.locked = v
}

// Locked returns the user's locked status.
func (u *User) Locked() bool {
	return u.locked
}

// LastActivity returns a [time.Time] of the last known user activity.
// It retrieves the most recent time from the last login, token use, bookmark update
// and bookmark deletion.
func (u *User) LastActivity() (time.Time, error) {
	ds := db.Q().Select(
		exp.Greatest(
			goqu.C("last_login").Table("u"),
			goqu.Case().When(goqu.C("x").Table("b").IsNotNull(), goqu.C("x").Table("b")).Else(goqu.V("0001-01-01")),
			goqu.Case().When(goqu.C("x").Table("br").IsNotNull(), goqu.C("x").Table("br")).Else(goqu.V("0001-01-01")),
			goqu.Case().When(goqu.C("x").Table("t").IsNotNull(), goqu.C("x").Table("t")).Else(goqu.V("0001-01-01")),
		),
	).From(
		goqu.T(db.TableUser).As("u"),
		goqu.Select(goqu.MAX(exp.DateTime(goqu.C("updated"))).As("x")).
			From(db.TableBookmark).
			Where(goqu.C("user_id").Eq(u.ID)).
			As("b"),
		goqu.Select(goqu.MAX(exp.DateTime(goqu.C("deleted"))).As("x")).
			From(db.TableBookmarkRemoved).
			Where(goqu.C("user_id").Eq(u.ID)).
			As("br"),
		goqu.Select(
			goqu.MAX(exp.DateTime(
				goqu.Case().When(goqu.C("last_used").IsNull(), goqu.V("0001-01-01")).Else(goqu.C("last_used")),
			)).As("x"),
		).
			From(db.TableToken).
			Where(goqu.C("user_id").Eq(u.ID)).
			As("t"),
	).Where(
		goqu.C("id").Table("u").Eq(u.ID),
	)

	var res time.Time
	var err error

	if db.Driver().Dialect() == "sqlite3" {
		s := ""
		if _, err = ds.ScanVal(&s); err == nil {
			res, err = dateparse.ParseStrict(s)
		}
	} else {
		_, err = ds.ScanVal(&res)
	}

	return res, err
}

// MakePassword generates a password of the given length.
func MakePassword(n int) string {
	alphabet := "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz!@$%&<>?"
	bytes := make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphabet[b%byte(len(alphabet))]
	}
	return string(bytes)
}

// UserSettings contains some user settings.
type UserSettings struct {
	DebugInfo      bool           `json:"debug_info"`
	Lang           string         `json:"lang"`
	AddonReminder  bool           `json:"addon_reminder"`
	EmailSettings  EmailSettings  `json:"email_settings"`
	ReaderSettings ReaderSettings `json:"reader_settings"`
}

// EmailSettings contains the user's email settings.
type EmailSettings struct {
	ReplyTo string `json:"reply_to"`
	EpubTo  string `json:"epub_to"`
}

// ReaderSettings contains the reader settings.
type ReaderSettings struct {
	Width       int    `json:"width"`
	Font        string `json:"font"`
	FontSize    int    `json:"font_size"`
	LineHeight  int    `json:"line_height"`
	Justify     int    `json:"justify"`
	Hyphenation int    `json:"hyphenation"`
}

// Scan loads a UserSettings instance from a column.
func (s *UserSettings) Scan(value any) error {
	if value == nil {
		return nil
	}

	// Default values
	s.AddonReminder = true
	s.ReaderSettings.Font = "lora"
	s.ReaderSettings.FontSize = 3
	s.ReaderSettings.LineHeight = 3

	v, err := types.JSONBytes(value)
	if err != nil {
		return err
	}
	json.Unmarshal(v, s) //nolint:errcheck
	return nil
}

// Value encodes a UserSettings value for storage.
func (s *UserSettings) Value() (driver.Value, error) {
	v, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(v), nil
}
