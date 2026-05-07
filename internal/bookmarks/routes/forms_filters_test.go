// SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/doug-martin/goqu/v9"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

func runFiltersFromForm(tests []struct {
	body     string
	expected string
},
) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				require := require.New(t)

				form := forms.New[FilterForm](context.Background())
				buf := new(bytes.Buffer)
				buf.WriteString(test.body)
				err := json.NewDecoder(buf).Decode(form)
				require.NoError(err)
				filters := form.toFilters()
				data, err := json.Marshal(filters)
				require.NoError(err)
				require.JSONEq(test.expected, string(data))
			})
		}
	}
}

func runFiltersToForm(tests []struct {
	filters  bookmarks.Filters
	expected string
},
) func(t *testing.T) {
	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				assert := require.New(t)

				form := forms.New[FilterForm](context.Background())
				form.Title.Set("original title")

				form.fromFilters(&test.filters)
				data, err := json.Marshal(form)
				assert.NoError(err)
				assert.JSONEq(test.expected, string(data))
			})
		}
	}
}

func runFiltersToSQL(tests []struct {
	filters  bookmarks.Filters
	expected [2]string
},
) func(t *testing.T) {
	dialects := []string{"sqlite3", "postgres"}

	return func(t *testing.T) {
		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				for j, dialect := range dialects {
					db.SetDriver(dialect)
					defer db.SetDriver("")

					t.Run(dialect, func(t *testing.T) {
						assert := require.New(t)

						ds := goqu.Dialect(dialect).Select(goqu.C("*").Table("b")).From("bookmark").As("b")
						ds = test.filters.ToSelectDataSet(ds)
						sql, _, err := ds.ToSQL()
						assert.NoError(err)
						assert.Equal(test.expected[j], sql)
					})
				}
			})
		}
	}
}

func TestFilters(t *testing.T) {
	t.Run("escapes", func(t *testing.T) {
		text := `test" import]\`
		form := forms.New[FilterForm](context.Background())
		// forms.UnmarshalValues([]string{text}, &form.Labels)
		form.Labels.Set(strconv.Quote(text))
		filters := form.toFilters()

		require.Exactly(t, strconv.Quote(text), filters.Labels)
	})

	t.Run("from form", runFiltersFromForm([]struct {
		body     string
		expected string
	}{
		{
			`{}`,
			`{
				"search": "",
				"title": "",
				"author": "",
				"site": "",
				"type": null,
				"labels": "",
				"note": "",
				"read_status": null,
				"is_marked": null,
				"is_archived": null,
				"is_loaded": null,
				"has_errors": null,
				"has_labels": null,
				"range_start": "",
				"range_end": ""
			}`,
		},
		{
			`{
				"title": "--title--",
				"labels": "ABC"
			}`,
			`{
				"search": "",
				"title": "--title--",
				"author": "",
				"site": "",
				"type": null,
				"labels": "ABC",
				"note": "",
				"read_status": null,
				"is_marked": null,
				"is_archived": null,
				"is_loaded": null,
				"has_errors": null,
				"has_labels": null,
				"range_start": "",
				"range_end": ""
			}`,
		},
		{
			`{
				"title": "--title--",
				"type": ["article", "video"],
				"read_status": []
			}`,
			`{
				"search": "",
				"title": "--title--",
				"author": "",
				"site": "",
				"type": ["article", "video"],
				"labels": "",
				"note": "",
				"read_status": [],
				"is_marked": null,
				"is_archived": null,
				"is_loaded": null,
				"has_errors": null,
				"has_labels": null,
				"range_start": "",
				"range_end": ""
			}`,
		},
		{
			`{
				"title": "--title--",
				"is_marked": false,
				"is_loaded": true
			}`,
			`{
				"search": "",
				"title": "--title--",
				"author": "",
				"site": "",
				"type": null,
				"labels": "",
				"note": "",
				"read_status": null,
				"is_marked": false,
				"is_archived": null,
				"is_loaded": true,
				"has_errors": null,
				"has_labels": null,
				"range_start": "",
				"range_end": ""
			}`,
		},
		{
			`{
				"search": "test title:--title-- label:XYZ",
				"labels": "ABC",
				"is_marked": false,
				"is_loaded": true
			}`,
			`{
				"search": "test",
				"title": "--title--",
				"author": "",
				"site": "",
				"type": null,
				"labels": "XYZ ABC",
				"note": "",
				"read_status": null,
				"is_marked": false,
				"is_archived": null,
				"is_loaded": true,
				"has_errors": null,
				"has_labels": null,
				"range_start": "",
				"range_end": ""
			}`,
		},
	}))

	t.Run("to form", runFiltersToForm([]struct {
		filters  bookmarks.Filters
		expected string
	}{
		{
			bookmarks.Filters{},
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"bf": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"author": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"has_errors": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"has_labels": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"id": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"is_archived": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"is_loaded": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"is_marked": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"labels": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"note": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"range_end": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"range_start": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"read_status": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"search": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"site": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"title": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"type": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					}
				}
			}`,
		},
		{
			bookmarks.Filters{
				Title:      "--title--",
				Type:       types.Strings{"article", "video"},
				IsMarked:   new(true),
				IsArchived: new(false),
			},
			`{
				"is_valid": true,
				"errors": null,
				"fields": {
					"bf": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"author": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"has_errors": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"has_labels": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"id": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"is_archived": {
						"is_null": false,
						"is_bound": true,
						"value": false,
						"errors": null
					},
					"is_loaded": {
						"is_null": false,
						"is_bound": false,
						"value": false,
						"errors": null
					},
					"is_marked": {
						"is_null": false,
						"is_bound": true,
						"value": true,
						"errors": null
					},
					"labels": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"note": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"range_end": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"range_start": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"read_status": {
						"is_null": false,
						"is_bound": false,
						"value": [],
						"errors": null
					},
					"search": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"site": {
						"is_null": false,
						"is_bound": false,
						"value": "",
						"errors": null
					},
					"title": {
						"is_null": false,
						"is_bound": false,
						"value": "--title--",
						"errors": null
					},
					"type": {
						"is_null": false,
						"is_bound": false,
						"value": ["article","video"],
						"errors": null
					}
				}
			}`,
		},
	}))

	t.Run("to SQL", runFiltersToSQL([]struct {
		filters  bookmarks.Filters
		expected [2]string
	}{
		{
			bookmarks.Filters{
				Title: "title--",
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` INNER JOIN `bookmark_idx` ON (`bookmark_idx`.`rowid` = `b`.`id`) WHERE `bookmark_idx` match 'catchall:oooooo AND title:\"title\"' ORDER BY rank ASC",
				`SELECT "b".* FROM "bookmark" INNER JOIN "bookmark_search" ON ("bookmark_search"."bookmark_id" = "b"."id") WHERE bookmark_search.title @@ to_tsquery('ts', '(title)') ORDER BY ts_rank_cd(bookmark_search.title, to_tsquery('ts', '(title)')) DESC`,
			},
		},
		{
			bookmarks.Filters{
				Search: "test title:-title-",
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` INNER JOIN `bookmark_idx` ON (`bookmark_idx`.`rowid` = `b`.`id`) WHERE `bookmark_idx` match 'catchall:oooooo AND -catchall:\"test\" AND title:\"title\"' ORDER BY rank ASC",
				`SELECT "b".* FROM "bookmark" INNER JOIN "bookmark_search" ON ("bookmark_search"."bookmark_id" = "b"."id") WHERE (bookmark_search.title || bookmark_search.description || bookmark_search."text" || bookmark_search.site || bookmark_search."label" || bookmark_search.note @@ to_tsquery('ts', '(test)') AND bookmark_search.title @@ to_tsquery('ts', '(title)')) ORDER BY ts_rank_cd(bookmark_search.title || bookmark_search.description || bookmark_search."text" || bookmark_search.site || bookmark_search."label" || bookmark_search.note, to_tsquery('ts', '(test)')) DESC, ts_rank_cd(bookmark_search.title, to_tsquery('ts', '(title)')) DESC`,
			},
		},
		{
			bookmarks.Filters{
				Search: "test -title:-title-",
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` INNER JOIN `bookmark_idx` ON (`bookmark_idx`.`rowid` = `b`.`id`) WHERE `bookmark_idx` match 'catchall:oooooo AND -catchall:\"test\" NOT title:\"title\"' ORDER BY rank ASC",
				`SELECT "b".* FROM "bookmark" INNER JOIN "bookmark_search" ON ("bookmark_search"."bookmark_id" = "b"."id") WHERE (bookmark_search.title || bookmark_search.description || bookmark_search."text" || bookmark_search.site || bookmark_search."label" || bookmark_search.note @@ to_tsquery('ts', '(test)') AND bookmark_search.title @@ to_tsquery('ts', '!(title)')) ORDER BY ts_rank_cd(bookmark_search.title || bookmark_search.description || bookmark_search."text" || bookmark_search.site || bookmark_search."label" || bookmark_search.note, to_tsquery('ts', '(test)')) DESC, ts_rank_cd(bookmark_search.title, to_tsquery('ts', '!(title)')) DESC`,
			},
		},
		{
			bookmarks.Filters{
				Search: "test title:-title-",
				Title:  "x-",
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` INNER JOIN `bookmark_idx` ON (`bookmark_idx`.`rowid` = `b`.`id`) WHERE `bookmark_idx` match 'catchall:oooooo AND -catchall:\"test\" AND title:\"title\" AND title:\"x\"' ORDER BY rank ASC",
				`SELECT "b".* FROM "bookmark" INNER JOIN "bookmark_search" ON ("bookmark_search"."bookmark_id" = "b"."id") WHERE (bookmark_search.title || bookmark_search.description || bookmark_search."text" || bookmark_search.site || bookmark_search."label" || bookmark_search.note @@ to_tsquery('ts', '(test)') AND bookmark_search.title @@ to_tsquery('ts', '(title) & (x)')) ORDER BY ts_rank_cd(bookmark_search.title || bookmark_search.description || bookmark_search."text" || bookmark_search.site || bookmark_search."label" || bookmark_search.note, to_tsquery('ts', '(test)')) DESC, ts_rank_cd(bookmark_search.title, to_tsquery('ts', '(title) & (x)')) DESC`,
			},
		},
		{
			bookmarks.Filters{
				Labels: `abc "ab" xx* -zz* -test`,
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE (EXISTS (SELECT * FROM json_each(CASE json_type(CASE json_valid(`b`.`labels`) WHEN true THEN `b`.`labels` ELSE '[]' END) WHEN 'array' THEN `b`.`labels` ELSE '[]' END) WHERE (`json_each`.`value` = 'abc')) AND EXISTS (SELECT * FROM json_each(CASE json_type(CASE json_valid(`b`.`labels`) WHEN true THEN `b`.`labels` ELSE '[]' END) WHEN 'array' THEN `b`.`labels` ELSE '[]' END) WHERE (`json_each`.`value` = 'ab')) AND EXISTS (SELECT * FROM json_each(CASE json_type(CASE json_valid(`b`.`labels`) WHEN true THEN `b`.`labels` ELSE '[]' END) WHEN 'array' THEN `b`.`labels` ELSE '[]' END) WHERE (`json_each`.`value` LIKE 'xx%')) AND NOT EXISTS (SELECT * FROM json_each(CASE json_type(CASE json_valid(`b`.`labels`) WHEN true THEN `b`.`labels` ELSE '[]' END) WHEN 'array' THEN `b`.`labels` ELSE '[]' END) WHERE (`json_each`.`value` LIKE 'zz%')) AND NOT EXISTS (SELECT * FROM json_each(CASE json_type(CASE json_valid(`b`.`labels`) WHEN true THEN `b`.`labels` ELSE '[]' END) WHEN 'array' THEN `b`.`labels` ELSE '[]' END) WHERE (`json_each`.`value` = 'test')))",
				`SELECT "b".* FROM "bookmark" WHERE (EXISTS (SELECT "value" FROM jsonb_array_elements_text(CASE jsonb_typeof("b"."labels") WHEN 'array' THEN "b"."labels" ELSE '[]' END) WHERE ("value" = 'abc')) AND EXISTS (SELECT "value" FROM jsonb_array_elements_text(CASE jsonb_typeof("b"."labels") WHEN 'array' THEN "b"."labels" ELSE '[]' END) WHERE ("value" = 'ab')) AND EXISTS (SELECT "value" FROM jsonb_array_elements_text(CASE jsonb_typeof("b"."labels") WHEN 'array' THEN "b"."labels" ELSE '[]' END) WHERE ("value" ILIKE 'xx%')) AND NOT EXISTS (SELECT "value" FROM jsonb_array_elements_text(CASE jsonb_typeof("b"."labels") WHEN 'array' THEN "b"."labels" ELSE '[]' END) WHERE ("value" ILIKE 'zz%')) AND NOT EXISTS (SELECT "value" FROM jsonb_array_elements_text(CASE jsonb_typeof("b"."labels") WHEN 'array' THEN "b"."labels" ELSE '[]' END) WHERE ("value" = 'test')))`,
			},
		},
		{
			bookmarks.Filters{
				RangeEnd: "2025-01-23",
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE (datetime(`created`) BETWEEN datetime('0001-01-01T00:00:00Z') AND datetime('2025-01-23T00:00:00Z'))",
				`SELECT "b".* FROM "bookmark" WHERE ("created" BETWEEN '0001-01-01T00:00:00Z' AND '2025-01-23T00:00:00Z')`,
			},
		},
		{
			bookmarks.Filters{
				RangeStart: "2025-01-23",
				RangeEnd:   "2025-01-24",
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE (datetime(`created`) BETWEEN datetime('2025-01-23T00:00:00Z') AND datetime('2025-01-24T00:00:00Z'))",
				`SELECT "b".* FROM "bookmark" WHERE ("created" BETWEEN '2025-01-23T00:00:00Z' AND '2025-01-24T00:00:00Z')`,
			},
		},
		{
			bookmarks.Filters{
				Type: []string{"article"},
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE (`b`.`type` = 'article')",
				`SELECT "b".* FROM "bookmark" WHERE ("b"."type" = 'article')`,
			},
		},
		{
			bookmarks.Filters{
				Type: []string{"article", "video"},
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE ((`b`.`type` = 'article') OR (`b`.`type` = 'video'))",
				`SELECT "b".* FROM "bookmark" WHERE (("b"."type" = 'article') OR ("b"."type" = 'video'))`,
			},
		},
		{
			bookmarks.Filters{
				ReadStatus: []string{"reading"},
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE (`b`.`read_progress` BETWEEN 1 AND 99)",
				`SELECT "b".* FROM "bookmark" WHERE ("b"."read_progress" BETWEEN 1 AND 99)`,
			},
		},
		{
			bookmarks.Filters{
				ReadStatus: []string{"reading", "unread"},
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE ((`b`.`read_progress` BETWEEN 1 AND 99) OR (`b`.`read_progress` = 0))",
				`SELECT "b".* FROM "bookmark" WHERE (("b"."read_progress" BETWEEN 1 AND 99) OR ("b"."read_progress" = 0))`,
			},
		},
		{
			bookmarks.Filters{
				IsMarked:   new(false),
				IsArchived: new(true),
				IsLoaded:   new(true),
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE ((`b`.`is_marked` IS 0) AND (`b`.`is_archived` IS 1) AND (`b`.`state` != 2))",
				`SELECT "b".* FROM "bookmark" WHERE (("b"."is_marked" IS FALSE) AND ("b"."is_archived" IS TRUE) AND ("b"."state" != 2))`,
			},
		},
		{
			bookmarks.Filters{
				IsMarked:   new(false),
				IsArchived: new(true),
				IsLoaded:   new(false),
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE ((`b`.`is_marked` IS 0) AND (`b`.`is_archived` IS 1) AND NOT((`b`.`state` != 2)))",
				`SELECT "b".* FROM "bookmark" WHERE (("b"."is_marked" IS FALSE) AND ("b"."is_archived" IS TRUE) AND NOT(("b"."state" != 2)))`,
			},
		},
		{
			bookmarks.Filters{
				HasLabels: new(true),
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE (json_array_length(CASE  WHEN json_valid(`b`.`labels`) THEN `b`.`labels` ELSE '[]' END) > 0)",
				`SELECT "b".* FROM "bookmark" WHERE (jsonb_array_length(CASE  WHEN (jsonb_typeof("b"."labels") = 'array') THEN "b"."labels" ELSE '[]' END) > 0)`,
			},
		},
		{
			bookmarks.Filters{
				HasErrors: new(true),
			},
			[2]string{
				"SELECT `b`.* FROM `bookmark` WHERE ((`b`.`state` = 1) OR (json_array_length(CASE  WHEN json_valid(`b`.`errors`) THEN `b`.`errors` ELSE '[]' END) > 0))",
				`SELECT "b".* FROM "bookmark" WHERE (("b"."state" = 1) OR (jsonb_array_length(CASE  WHEN (jsonb_typeof("b"."errors") = 'array') THEN "b"."errors" ELSE '[]' END) > 0))`,
			},
		},
	}))
}
