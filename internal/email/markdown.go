// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package email

import (
	"regexp"

	"golang.org/x/net/html"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var urlRegexp = regexp.MustCompile(`^(?:https?)://[-a-zA-Z0-9@:%._\+~#=]{1,256}(?::\d+)?(?:[/#?][-a-zA-Z0-9@:%_+.~#$!?&/=;,'">\^{}\[\]` + "`" + `]*)?`)

// markdown is the main markdown parser for email text to html.
var markdown = goldmark.New(
	goldmark.WithExtensions(
		extension.NewLinkify(
			extension.WithLinkifyAllowedProtocols([]string{
				"http:",
				"https:",
			}),
			extension.WithLinkifyURLRegexp(
				urlRegexp,
			),
		),
	),
)

var html2md4email = converter.NewConverter(
	converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(
			commonmark.WithHeadingStyle(commonmark.HeadingStyleATX),
			commonmark.WithEmDelimiter("_"),
			commonmark.WithStrongDelimiter("**"),
			commonmark.WithBulletListMarker("-"),
		),
		table.NewTablePlugin(),
		&html2mdMailPlugin{},
	),
)

type html2mdMailPlugin struct{}

func (s *html2mdMailPlugin) Name() string {
	return "email-render"
}

func (s *html2mdMailPlugin) Init(conv *converter.Converter) error {
	conv.Register.RendererFor("a", converter.TagTypeInline, s.handleLink, converter.PriorityEarly)
	conv.Register.RendererFor("b", converter.TagTypeInline, s.passthrough, converter.PriorityEarly)
	conv.Register.RendererFor("strong", converter.TagTypeInline, s.passthrough, converter.PriorityEarly)
	conv.Register.RendererFor("i", converter.TagTypeInline, s.passthrough, converter.PriorityEarly)
	conv.Register.RendererFor("em", converter.TagTypeInline, s.passthrough, converter.PriorityEarly)
	conv.Register.RendererFor("img", converter.TagTypeInline, s.ignoreImg, converter.PriorityEarly)
	conv.Register.RendererFor("div", converter.TagTypeBlock, s.handleDiv, converter.PriorityEarly)
	return nil
}

func (s *html2mdMailPlugin) ignoreImg(_ converter.Context, _ converter.Writer, _ *html.Node) converter.RenderStatus {
	return converter.RenderSuccess
}

func (s *html2mdMailPlugin) passthrough(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
	ctx.RenderChildNodes(ctx, w, node)
	return converter.RenderSuccess
}

// handleLink returns the URL only if the link's text is the same as its href value.
func (s *html2mdMailPlugin) handleLink(_ converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
	text := ""
	href := ""
	for _, attr := range node.Attr {
		if attr.Key == "href" {
			href = attr.Val
		}
	}

	for n := range node.ChildNodes() {
		if n.Type == html.TextNode {
			text = n.Data
		}
	}

	if text == href {
		w.WriteString(href) // nolint:errcheck
		return converter.RenderSuccess
	}

	return converter.RenderTryNext
}

func (s *html2mdMailPlugin) handleDiv(_ converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
	// Add "-- " when we find a role=footer div.
	for _, attr := range node.Attr {
		if attr.Key == "role" && attr.Val == "footer" {
			w.WriteString("-- ") //nolint:errcheck
		}
	}

	return converter.RenderTryNext
}
