// SPDX-FileCopyrightText: © 2026 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package email provides functions to send email messages.
package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path"
	"slices"
	"time"

	"github.com/a-h/templ"
	"github.com/aymerick/douceur/inliner"
	"github.com/wneessen/go-mail"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/http/request"
)

const cr = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

type (
	ctxAttachmentKey struct{}
	ctxSiteURLKey    struct{}
)

// Context setters and getters.
var (
	withAttachments, getAttachments       = ctxr.WithGetter[*Attachments](ctxAttachmentKey{})
	WithSiteURL, GetSiteURL, CheckSiteURL = ctxr.WithAll[*url.URL](ctxSiteURLKey{}) //nolint:revive
)

// sender defines an email sender.
type sender interface {
	SendEmail(*Message) error
}

// Sender is the default email sender. It's made public so it can be
// overridden during tests.
var Sender sender

// InitSender initializes the default email sender base on the
// configuration.
func InitSender() {
	if Sender != nil { // Sender can be set by the test runner
		return
	}

	if configs.Config.Email.Debug {
		Sender = &StdOutSender{}
	} else if configs.Config.Email.Host != "" {
		Sender = &SMTPSender{}
	}
}

// CanSendEmail returns true when we can send emails.
func CanSendEmail() bool {
	return Sender != nil
}

// MessageOption is a function that can manipulate a message and is called
// during [NewMsg].
type MessageOption func(ctx context.Context, msg *Message) error

// Message is a wrapper around [mail.Msg].
type Message struct {
	*mail.Msg
}

// Attachment is a file attached to a message.
type Attachment struct {
	id    string
	name  string
	mtype string
	r     io.Reader
}

// URL returns the attachment URL with "cid:" scheme.
func (a *Attachment) URL() string {
	return "cid:" + a.id
}

// Attachments is a list of [Attachment].
type Attachments []Attachment

// NewAttachment creates a new attachment with a random id.
func NewAttachment(name, mtype string, r io.Reader) Attachment {
	buf := new(bytes.Buffer)
	io.Copy(buf, r) //nolint:errcheck
	id := rndID()
	return Attachment{
		id:    id,
		name:  id + path.Ext(name),
		mtype: mtype,
		r:     buf,
	}
}

func rndID() string {
	src := make([]byte, 22)
	rand.Read(src)
	for i := range src {
		src[i] = cr[src[i]%byte(len(cr))]
	}

	return string(src)
}

// NewMessage returns a new [Message] with sender, recipient and subject.
// It checks if we can send email and adds a User-Agent header.
func NewMessage(ctx context.Context, from, to, subject string, options ...MessageOption) (*Message, error) {
	if !CanSendEmail() {
		return nil, errors.New("no email sender defined")
	}

	msg := &Message{
		Msg: mail.NewMsg(
			mail.WithCharset(mail.CharsetUTF8),
			mail.WithEncoding(mail.EncodingQP),
		),
	}

	if err := msg.From(from); err != nil {
		return nil, err
	}
	if err := msg.To(to); err != nil {
		return nil, err
	}

	msg.SetUserAgent("Readeck // https://readeck.org/")
	msg.SetMessageIDWithValue(rndID())
	msg.Subject(subject)

	for _, fn := range options {
		if err := fn(ctx, msg); err != nil {
			return nil, err
		}
	}

	return msg, nil
}

// WithMDText returns a [MessageOption] that adds a text/plain message part.
// The text is converted to HTML (as Markdown) and passed to [WithComponent]
// using [Components.Base] as a template, and added as an HTML part.
// A generic footer is added to the text part.
func WithMDText(body string) MessageOption {
	return func(ctx context.Context, msg *Message) (err error) {
		// Convert to HTML first
		html := new(bytes.Buffer)
		if err = markdown.Convert([]byte(body), html); err != nil {
			return fmt.Errorf("markdown conversion: %w", err)
		}

		// Add the text/plain with signature
		body += Components{}.TextFooter(ctx)
		msg.AddAlternativeString(mail.TypeTextPlain, body)

		// Add HTML
		return WithComponent(Components{}.HTML(html))(ctx, msg)
	}
}

// WithHTML adds an HTML part to the message.
// When the message doesn't contain a text/plain part already,
// the HTML is converted to text and added as a text/plain part.
func WithHTML(html io.Reader) MessageOption {
	return func(_ context.Context, msg *Message) error {
		buf := new(bytes.Buffer)

		// First, add the plain text version, if we don't have one already
		if slices.IndexFunc(msg.GetParts(), func(p *mail.Part) bool {
			return p.GetContentType() == mail.TypeTextPlain
		}) == -1 {
			txt, err := html2md4email.ConvertReader(io.TeeReader(html, buf))
			if err != nil {
				return fmt.Errorf("html conversion: %w", err)
			}
			msg.AddAlternativeString(mail.TypeTextPlain, string(txt))
		} else {
			if _, err := io.Copy(buf, html); err != nil {
				return err
			}
		}

		// Then, add the inlined HTML
		inlined, err := inliner.Inline(buf.String())
		if err != nil {
			return fmt.Errorf("style inliner: %w", err)
		}
		msg.AddAlternativeString(mail.TypeTextHTML, inlined)
		return nil
	}
}

// WithComponent adds a [templ.Component] as a text/html part.
// It uses [WithHTML].
func WithComponent(component templ.Component) MessageOption {
	return func(ctx context.Context, msg *Message) (err error) {
		buf := new(bytes.Buffer)

		// Add attachments to the context so we can later
		// add them to the message.
		files := Attachments{}
		ctx = withAttachments(ctx, &files)

		// Render component.
		if err = component.Render(ctx, buf); err != nil {
			return err
		}

		if err = WithHTML(buf)(ctx, msg); err != nil {
			return err
		}

		for _, file := range files {
			if err = msg.EmbedReader(
				file.name,
				file.r,
				mail.WithFileContentType(mail.ContentType(file.mtype)),
				mail.WithFileContentID(file.id),
			); err != nil {
				return err
			}
		}
		return nil
	}
}

// SendMessage sends a message with [MessageOption]s.
// The provided context is derived with [context.WithoutCancel],
// making this function safe to be called as a goroutine.
func SendMessage(ctx context.Context, from, to, subject string, options ...MessageOption) error {
	ctx = context.WithoutCancel(ctx)
	log := slog.Default()

	if reqID, ok := request.CheckReqID(ctx); ok {
		log = log.With(slog.String("@id", reqID))
	}

	log = log.With(
		slog.String("from", from),
		slog.String("to", to),
		slog.String("subject", subject),
	)

	msg, err := NewMessage(ctx, from, to, subject, options...)
	if err != nil {
		log.Error("making email", slog.Any("err", err))
		return err
	}

	if err = Send(msg); err != nil {
		log.Error("sending email", slog.Any("err", err))
		return err
	}

	log.Debug("email sent")
	return nil
}

// Send sends a [mail.Msg] using the default sender.
func Send(msg *Message) error {
	return Sender.SendEmail(msg)
}

// StdOutSender implements EmailSender for stdout.
type StdOutSender struct{}

// SendEmail "sends" an email to stdout.
func (s *StdOutSender) SendEmail(msg *Message) error {
	fmt.Fprintln(os.Stdout, "=== Outbound email ===================================================")
	msg.WriteTo(os.Stdout) // nolint:errcheck
	fmt.Fprintln(os.Stdout, "\n======================================================================")
	return nil
}

// SMTPSender implements EmailSender for SMTP.
type SMTPSender struct{}

// SendEmail sends an email using SMTP.
func (s *SMTPSender) SendEmail(msg *Message) error {
	client, err := mail.NewClient(
		configs.Config.Email.Host,
		mail.WithPort(configs.Config.Email.Port),
		mail.WithTimeout(time.Second*10),
		mail.WithTLSConfig(&tls.Config{
			ServerName:         configs.Config.Email.Host,
			MinVersion:         mail.DefaultTLSMinVersion,
			InsecureSkipVerify: configs.Config.Email.Insecure, //nolint:gosec
		}),
	)
	if err != nil {
		return err
	}

	if configs.Config.Email.Username != "" {
		client.SetSMTPAuth(mail.SMTPAuthPlain)
		client.SetUsername(configs.Config.Email.Username)
		client.SetPassword(configs.Config.Email.Password)
	}

	switch configs.Config.Email.Encryption {
	case "starttls":
		client.SetTLSPolicy(mail.TLSMandatory)
	case "ssltls":
		client.SetSSL(true)
	case "none":
		client.SetTLSPolicy(mail.NoTLS)
	default:
		client.SetTLSPolicy(mail.TLSOpportunistic)
	}

	return client.DialAndSend(msg.Msg)
}
