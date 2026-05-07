// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package tokens contains the models and functions to manage
// user API tokens.
package tokens

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/base58"
)

var (
	// Tokens is the token manager.
	Tokens = Manager{}

	// ErrNotFound is returned when a token record was not found.
	ErrNotFound = errors.New("not found")
)

// Token is a token record in database.
type Token struct {
	ID          int           `db:"id"          goqu:"skipinsert,skipupdate"`
	UID         string        `db:"uid"`
	UserID      *int          `db:"user_id"`
	ClientInfo  *ClientInfo   `db:"client_info"`
	Created     time.Time     `db:"created"     goqu:"skipupdate"`
	LastUsed    *time.Time    `db:"last_used"`
	Expires     *time.Time    `db:"expires"`
	IsEnabled   bool          `db:"is_enabled"`
	Application string        `db:"application"`
	Roles       types.Strings `db:"roles"`
}

// Manager is a query helper for token entries.
type Manager struct{}

// Query returns a prepared goqu SelectDataset that can be extended later.
func (m *Manager) Query() *goqu.SelectDataset {
	return db.Q().From(goqu.T(db.TableToken).As("t")).Prepared(true)
}

// GetOne executes the a select query and returns the first result or an error
// when there's no result.
func (m *Manager) GetOne(expressions ...goqu.Expression) (*Token, error) {
	var t Token
	found, err := m.Query().Where(expressions...).ScanStruct(&t)

	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, ErrNotFound
	}

	return &t, nil
}

// GetUser returns the token and user owning a given token uid.
func (m *Manager) GetUser(uid string) (*TokenAndUser, error) {
	var res TokenAndUser
	ds := m.Query().
		Join(
			goqu.T(db.TableUser).As("u"),
			goqu.On(goqu.I("t.user_id").Eq(goqu.I("u.id"))),
		).
		Where(
			goqu.I("t.uid").Eq(uid),
			goqu.I("t.is_enabled").Eq(true),
		)

	found, err := ds.ScanStruct(&res)
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, ErrNotFound
	}

	return &res, nil
}

// Create insert a new token in the database.
func (m *Manager) Create(token *Token) error {
	if token.UserID == nil {
		return errors.New("no token user")
	}
	if strings.TrimSpace(token.Application) == "" {
		return errors.New("no application")
	}

	token.Created = time.Now().UTC()
	if token.UID == "" {
		token.UID = base58.NewUUID()
	}

	ds := db.Q().Insert(db.TableToken).
		Rows(token).
		Prepared(true)

	id, err := db.InsertWithID(ds, "id")
	if err != nil {
		return err
	}

	token.ID = id
	return nil
}

// Update updates some bookmark values.
func (t *Token) Update(v any) error {
	if t.ID == 0 {
		return errors.New("no ID")
	}

	_, err := db.Q().Update(db.TableToken).Prepared(true).
		Set(v).
		Where(goqu.C("id").Eq(t.ID)).
		Executor().Exec()

	return err
}

// Save updates all the token values.
func (t *Token) Save() error {
	return t.Update(t)
}

// Delete removes a token from the database.
func (t *Token) Delete() error {
	_, err := db.Q().Delete(db.TableToken).Prepared(true).
		Where(goqu.C("id").Eq(t.ID)).
		Executor().Exec()

	return err
}

// IsExpired returns true if the token has an expiration date and the
// current time is after the expiration.
func (t *Token) IsExpired() bool {
	if t.Expires == nil || t.Expires.IsZero() {
		return false
	}
	return time.Now().UTC().After(*t.Expires)
}

// ClientInfo contains a token's OAuth registered client.
type ClientInfo struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Website         string   `json:"website"`
	Logo            string   `json:"logo"`
	SoftwareID      string   `json:"software_id"`
	SoftwareVersion string   `json:"software_version"`
	GrantTypes      []string `json:"grant_types"`
}

// Scan loads a UserSettings instance from a column.
func (s *ClientInfo) Scan(value any) error {
	if value == nil {
		return nil
	}

	v, err := types.JSONBytes(value)
	if err != nil {
		return err
	}
	json.Unmarshal(v, s) //nolint:errcheck
	return nil
}

// Value encodes a UserSettings value for storage.
func (s *ClientInfo) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}

	v, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// TokenAndUser is a result of a joint query on user and token tables.
type TokenAndUser struct {
	Token *Token      `db:"t"`
	User  *users.User `db:"u"`
}
