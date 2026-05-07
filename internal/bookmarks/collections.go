// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package bookmarks

import (
	"errors"
	"hash"
	"io"
	"strconv"
	"time"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/pkg/base58"
)

var (
	// Collections is the collection query manager.
	Collections = CollectionManager{}

	// ErrCollectionNotFound is returned when a collection record was not found.
	ErrCollectionNotFound = errors.New("not found")
)

// Collection is a collection record in the database.
type Collection struct {
	ID       int       `db:"id"        goqu:"skipinsert,skipupdate"`
	UID      string    `db:"uid"`
	UserID   *int      `db:"user_id"`
	Created  time.Time `db:"created"   goqu:"skipupdate"`
	Updated  time.Time `db:"updated"`
	Name     string    `db:"name"`
	IsPinned bool      `db:"is_pinned"`
	Filters  Filters   `db:"filters"`
}

// CollectionManager is a query helper for bookmark entries.
type CollectionManager struct{}

// Query returns a prepared goqu SelectDataset that can be extended later.
func (m *CollectionManager) Query() *goqu.SelectDataset {
	return db.Q().From(goqu.T(db.TableBookmarkCollection).As("c")).Prepared(true)
}

// GetOne executes the a select query and returns the first result or an error
// when there's no result.
func (m *CollectionManager) GetOne(expressions ...goqu.Expression) (*Collection, error) {
	var c Collection
	found, err := m.Query().Where(expressions...).ScanStruct(&c)

	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, ErrBookmarkNotFound
	}

	return &c, nil
}

// Create inserts a new collection in the database.
func (m *CollectionManager) Create(collection *Collection) error {
	if collection.UserID == nil {
		return errors.New("no collection user")
	}

	collection.Created = time.Now().UTC()
	collection.Updated = collection.Created
	collection.UID = base58.NewUUID()

	ds := db.Q().Insert(db.TableBookmarkCollection).
		Rows(collection).
		Prepared(true)

	id, err := db.InsertWithID(ds, "id")
	if err != nil {
		return err
	}

	collection.ID = id

	return nil
}

// Update updates some collection values.
func (c *Collection) Update(v any) error {
	if c.ID == 0 {
		return errors.New("no ID")
	}

	switch v := v.(type) {
	case map[string]any:
		v["updated"] = time.Now().UTC()
	default:
		//
	}

	_, err := db.Q().Update(db.TableBookmarkCollection).Prepared(true).
		Set(v).
		Where(goqu.C("id").Eq(c.ID)).
		Executor().Exec()

	return err
}

// Save updates all the collection values.
func (c *Collection) Save() error {
	c.Updated = time.Now().UTC()
	return c.Update(c)
}

// Delete removes a collection from the database.
func (c *Collection) Delete() error {
	_, err := db.Q().Delete(db.TableBookmarkCollection).Prepared(true).
		Where(goqu.C("id").Eq(c.ID)).
		Executor().Exec()

	return err
}

// UpdateEtag returns the string used to generate the etag
// of the collection(s).
func (c *Collection) UpdateEtag(h hash.Hash) {
	io.WriteString(h, c.UID+strconv.FormatInt(c.Updated.UTC().UnixNano(), 10))
}
