// SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package utils provides simple various utilities
package utils

import (
	"fmt"
	"math"
	"net/url"
	"path"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ShortText returns a string of maxChars maximum length. It attempts to cut between words
// when possible.
func ShortText(s string, maxChars int) string {
	runes := []rune(strings.TrimSpace(strings.Join(strings.Fields(s), " ")))
	if len(runes) <= maxChars {
		return string(runes)
	}

	res := &strings.Builder{}
	j := 0
	for i, word := range strings.FieldsFunc(s, unicode.IsSpace) {
		w := []rune(word)
		j += len(w)
		if j >= maxChars {
			if len(w) > maxChars {
				res.WriteString(string(w[0:maxChars]))
			}
			break
		}
		if i > 0 {
			res.WriteString(" ")
		}
		res.WriteString(word)
	}
	res.WriteString("...")

	return res.String()
}

// ShortURL returns a string of maxChars maximum length where src is a URL. It attempts
// to nicely cut the path parts.
func ShortURL(s string, maxChars int) string {
	src, err := url.Parse(s)
	if err != nil {
		return ShortText(s, maxChars)
	}

	res := &strings.Builder{}
	maxChars = max(5, maxChars-len(src.Hostname()))
	res.WriteString(src.Hostname())
	res.WriteString("/")
	dir, file := path.Split(strings.Trim(src.Path, "/"))
	if len(dir+file) <= maxChars {
		res.WriteString(dir + file)
	} else {
		res.WriteString(".../" + ShortText(file, maxChars))
	}
	return res.String()
}

var (
	lat = []*unicode.RangeTable{unicode.Letter, unicode.Number}
	nop = []*unicode.RangeTable{unicode.Mark, unicode.Sk, unicode.Lm}
)

// Slug replaces each run of characters which are not unicode letters or
// numbers with a single hyphen, except for leading or trailing runes. Letters
// will be stripped of diacritical marks and lowercased. Letter or number
// codepoints that do not have combining marks or a lower-cased variant will
// be passed through unaltered.
func Slug(s string) string {
	buf := make([]rune, 0, len(s))
	dash := false
	for _, r := range norm.NFKD.String(s) {
		switch {
		// unicode 'letters' like mandarin characters pass through
		case unicode.IsOneOf(lat, r):
			buf = append(buf, unicode.ToLower(r))
			dash = true
		case unicode.IsOneOf(nop, r):
			// skip
		case dash:
			buf = append(buf, '-')
			dash = false
		}
	}
	if i := len(buf) - 1; i >= 0 && buf[i] == '-' {
		buf = buf[:i]
	}
	return string(buf)
}

// FormatBytes returns a human readable size in IEC format.
func FormatBytes(s uint64) string {
	if s == 0 {
		return "0 B"
	}

	sizes := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	e := math.Floor(math.Log(float64(s)) / math.Log(1024))
	suffix := sizes[int(e)]

	f := "%.2f %s"
	if e < 1 {
		f = "%.0f %s"
	}

	return fmt.Sprintf(f, math.Floor(float64(s)/math.Pow(1024, e)*10+0.5)/10, suffix)
}

// NormalizeSpaces transforms all consecutive unicode space character
// to a single space in the input string.
// It then returns a space trimed string.
func NormalizeSpaces(text string) string {
	wasSpace := false
	var b strings.Builder
	b.Grow(len(text))

	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			if !wasSpace {
				b.WriteRune(' ')
				wasSpace = true
			}
			continue
		}
		b.WriteRune(r)
		wasSpace = false
	}

	return strings.TrimSpace(b.String())
}

// NormalizeSpacesKeepNewlines collapses runs of horizontal whitespace to a single
// space, but preserves newlines (collapsing 3+ consecutive newlines down to 2 so
// markdown paragraph breaks survive without inflating). Used for fields that
// carry rich text like bookmark descriptions written in markdown.
func NormalizeSpacesKeepNewlines(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	wasHSpace := false
	newlineRun := 0
	for _, r := range text {
		if r == '\n' {
			// flush any pending horizontal space (don't emit before a newline)
			wasHSpace = false
			newlineRun++
			// cap consecutive newlines at 2 (one blank line) so we get clean
			// markdown paragraph breaks without arbitrary vertical space.
			if newlineRun <= 2 {
				b.WriteByte('\n')
			}
			continue
		}
		newlineRun = 0
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			if !wasHSpace {
				b.WriteRune(' ')
				wasHSpace = true
			}
			continue
		}
		b.WriteRune(r)
		wasHSpace = false
	}

	return strings.TrimSpace(b.String())
}

// ToLowerTextOnly returns a lowercased string with all spaces, punctuation and control
// characters removed.
func ToLowerTextOnly(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsControl(r) {
			return -1
		}
		r = unicode.To(unicode.LowerCase, r)
		return r
	}, text)
}
