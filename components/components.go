// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package components contains the shared templ components.
package components

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/adler32"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"codeberg.org/readeck/readeck/internal/profile/preferences"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/locales"
	"codeberg.org/readeck/readeck/pkg/glob"
	"codeberg.org/readeck/readeck/pkg/strftime"
)

// markdownRenderer renders bookmark description text. Configured for
// safe rendering: no raw HTML in source (goldmark default), strikethrough
// + GFM tables supported, hard-wrap turns single newlines into <br>.
var markdownRenderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// S is a shortcut to [fmt.Sprintf].
var S = fmt.Sprintf

// L returns the context's current [locales.Locale].
func L(ctx context.Context) *locales.Locale {
	return server.LocaleContext(ctx)
}

// URL returns a [templ.SafeURL] (path only) for a given internal URL.
func URL(ctx context.Context, args ...string) templ.SafeURL {
	return templ.URL(urls.PathOnly(urls.AbsoluteURLContext(ctx, args...)))
}

// Asset returns an asset path.
func Asset(ctx context.Context, name string) templ.SafeURL {
	return templ.URL(urls.PathOnly(urls.AssetURLContext(ctx, name)))
}

// CSPNonce returns the CSP nonce for stylesheets and scripts.
func CSPNonce(ctx context.Context) string {
	s, _ := server.GetCSPNonce(ctx)
	return s
}

// CurrentPath returns the current path without the app prefix.
func CurrentPath(ctx context.Context) string {
	return urls.CurrentPath(server.GetRequest(ctx))
}

// PathIs returns whether one of the given paths p matches the current
// request's path (query parameters excluded).
func PathIs(ctx context.Context, p ...string) bool {
	cp := "/" + strings.TrimPrefix(server.GetRequest(ctx).URL.Path, urls.Prefix())
	for _, x := range p {
		if glob.Glob(x, cp) {
			return true
		}
	}
	return false
}

// HasPermission returns whether the current user is granted permissions.
func HasPermission(ctx context.Context, obj, act string) bool {
	return server.GetUser(ctx).HasPermission(obj, act)
}

// IsAnonymous returns whether the current user is anonymous.
func IsAnonymous(ctx context.Context) bool {
	return server.GetUser(ctx).IsAnonymous()
}

// Strftime calls [strftime.Formatter.Strftime] with the user's locale.
func Strftime(ctx context.Context, t time.Time, f string) string {
	return strftime.New(L(ctx)).Strftime(f, t)
}

// Checksum returns the hexadecimal adler32 checksum of a string.
func Checksum(text string) string {
	h := adler32.New()
	h.Write([]byte(text))
	return strconv.FormatUint(uint64(h.Sum32()), 16)
}

// Preferences returns the user's preferences.
// Preferences are loaded only once, the first time they are needed.
func Preferences(ctx context.Context) *preferences.Preferences {
	p := server.GetPreferences(ctx)
	if !p.IsLoaded() {
		p2 := preferences.New(server.GetUser(ctx), server.GetSession(server.GetRequest(ctx)))
		*p = *p2
	}

	return p
}

// Tern is a poor man's ternary operator.
// Never to be used outside a component!
func Tern[T any](t func() bool, whenTrue T, whenFalse T) T {
	if t() {
		return whenTrue
	}
	return whenFalse
}

// JSON returns a string of the marshaled data into JSON.
// The result is not HTML escaped and can be indented if needed.
func JSON(data any, indent bool) string {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	enc.Encode(data) //nolint:errcheck
	return buf.String()
}

// HTML copies directly the input [io.Reader] to the response writer.
func HTML(r io.Reader) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		if r == nil {
			return nil
		}
		_, err := io.Copy(w, r)
		return err
	})
}

// Markdown renders the input string as markdown -> HTML for display in
// templates. Used by bookmark description rendering so users (or scripts
// like personal-info-system's autosave_tg.py) can write rich summaries
// that actually render in the Readeck reader UI instead of showing
// **literal** asterisks.
//
// Safety: goldmark's default config does NOT allow raw HTML in markdown
// source, so a malicious description cannot inject <script>. Strikethrough
// + GFM tables + hard-wraps enabled. Empty input renders nothing.
func Markdown(md string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		if md == "" {
			return nil
		}
		return markdownRenderer.Convert([]byte(md), w)
	})
}
