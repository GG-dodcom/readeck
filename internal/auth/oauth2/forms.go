// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/google/uuid"
	"golang.org/x/net/idna"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/tokens"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/base58"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

const (
	grantTypeAuthCode   = "authorization_code"
	grantTypeDeviceCode = "urn:ietf:params:oauth:grant-type:device_code"
)

// baseForm is the form that every other form extends so they
// can use its custom tags.
type baseForm struct {
	forms.Form
}

func (*baseForm) GetTaggedValidator(name, args string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "is_valid_client_uri":
		return isValidClientURI, true
	case "is_valid_redirect_uri":
		return isValidRedirectURI, true
	case "is_valid_logo_uri":
		return isValidLogoURI, true
	// This can be required_with:authcode or required_with:device
	// The form must contain a "grant_types" or "grant_type" field
	// and the resulting validator will only apply when one of these
	// fields contain the requested grant type.
	case "required_with":
		return forms.FieldValidatorFunc(func(field forms.Binder) error {
			var grantType string
			switch args {
			case "authcode":
				grantType = grantTypeAuthCode
			case "device":
				grantType = grantTypeDeviceCode
			}

			typeField := tc.Form.Fields()["grant_types"]
			if typeField == nil {
				typeField = tc.Form.Fields()["grant_type"]
			}
			if typeField == nil {
				panic("no grant_types or grant_type field in the form")
			}

			switch f := typeField.(type) {
			case *forms.TextField:
				if f.Value() == grantType {
					return forms.Required(field)
				}
				return nil
			case *forms.TextListField:
				if slices.Contains(f.Value(), grantType) {
					return forms.Required(field)
				}
				return nil
			}
			panic("required_with cannot not apply")
		}), true
	case "clean_user_code":
		return forms.CleanerFunc[string](func(v string) string {
			return strings.ToUpper(strings.ReplaceAll(v, "-", ""))
		}), true

	case "grant_types":
		forms.Choices(tc.Field,
			forms.Choice(grantTypeAuthCode, grantTypeAuthCode),
			forms.Choice(grantTypeDeviceCode, grantTypeDeviceCode),
		)

		if f, ok := tc.Field.(*forms.TextListField); ok {
			f.Set([]string{grantTypeAuthCode, grantTypeDeviceCode})
		}
		return nil, true
	case "auth_methods":
		forms.Choices(tc.Field,
			forms.Choice("none", "none"),
		)
		tc.Field.(*forms.TextField).Set("none")
		return nil, true
	case "response_types":
		forms.Choices(tc.Field,
			forms.Choice("code", "code"),
		)
		tc.Field.(*forms.TextListField).Set([]string{"code"})
		return nil, true
	case "challenge_methods":
		forms.Choices(tc.Field,
			forms.Choice("S256", "S256"),
		)
		return nil, true
	case "scope_choices":
		var user *users.User
		if info, _ := auth.CheckAuthInfo(tc.Context); !info.User.IsAnonymous() {
			user = info.User
		}

		g := users.GroupList(server.LocaleContext(tc.Context), "@oauth_scope", user)
		choices := make([]forms.ValueChoice[string], 0, len(g))
		for _, x := range g {
			choices = append(choices, forms.Choice(x[1], x[0]))
		}
		forms.Choices(tc.Field, choices...)

		return nil, true
	}
	return nil, false
}

type clientForm struct {
	baseForm
	ClientName              forms.TextField     `json:"client_name"                validate:"trim required max_len:128"`
	ClientURI               forms.TextField     `json:"client_uri"                 validate:"trim required max_len:256 is_valid_client_uri"`
	LogoURI                 forms.TextField     `json:"logo_uri"                   validate:"trim max_len:8192 is_valid_logo_uri"`
	SoftwareID              forms.TextField     `json:"software_id"                validate:"trim required max_len:128"`
	SoftwareVersion         forms.TextField     `json:"software_version"           validate:"trim required max_len:64"`
	RedirectURIs            forms.TextListField `json:"redirect_uris"              validate:"trim required_with:authcode max_len:256 is_valid_redirect_uri"`
	GrantTypes              forms.TextListField `json:"grant_types"                validate:"grant_types"`
	TokenEndpointAuthMethod forms.TextField     `json:"token_endpoint_auth_method" validate:"auth_methods"`
	ResponseTypes           forms.TextListField `json:"response_types"             validate:"response_types"`
}

func (f *clientForm) getError() oauthError {
	switch {
	case len(f.RedirectURIs.Errors()) > 0:
		return errInvalidRedirectURI.withDescription(f.RedirectURIs.Errors().Error())
	default:
		return errInvalidClientMetadata.withDescription(newFormError(f).description)
	}
}

func (f *clientForm) createClient() (*oauthClient, error) {
	client := &oauthClient{
		ID:                      uuid.New().URN(),
		Name:                    f.ClientName.Value(),
		URI:                     f.ClientURI.Value(),
		Logo:                    f.LogoURI.Value(),
		RedirectURIs:            f.RedirectURIs.Value(),
		GrantTypes:              f.GrantTypes.Value(),
		TokenEndpointAuthMethod: f.TokenEndpointAuthMethod.Value(),
		ResponseTypes:           f.ResponseTypes.Value(),
		SoftwareID:              f.SoftwareID.Value(),
		SoftwareVersion:         f.SoftwareVersion.Value(),
	}

	if err := client.store(); err != nil {
		return nil, err
	}

	return client, nil
}

type authorizationForm struct {
	baseForm
	ClientID            forms.TextField    `json:"client_id"             validate:"trim required max_len:52"`
	RedirectURI         forms.TextField    `json:"redirect_uri"          validate:"trim required max_len:256 is_valid_redirect_uri"`
	Scope               scopeField         `json:"scope"                 validate:"trim required trim required scope_choices"`
	State               forms.TextField    `json:"state"                 validate:"trim max_len:64"`
	CodeChallenge       forms.TextField    `json:"code_challenge"        validate:"trim required max_len:256"`
	CodeChallengeMethod forms.TextField    `json:"code_challenge_method" validate:"trim required challenge_methods"`
	Granted             forms.BooleanField `json:"granted"`
}

func (f *authorizationForm) getCode(user *users.User) (string, error) {
	req := authCodeRequest{
		ClientID:  f.ClientID.Value(),
		TokenID:   base58.NewUUID(),
		Scopes:    f.Scope.Value(),
		Challenge: f.CodeChallenge.Value(),
		UserID:    user.ID,
		Expires:   time.Now().UTC().Add(time.Minute * 10),
	}

	slices.Sort(req.Scopes)
	req.Scopes = slices.Compact(req.Scopes)

	// Encode the request
	code, err := configs.Keys.OauthRequestKey().EncodeJSON(req)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(code), nil
}

func (f *authorizationForm) getError() oauthError {
	switch {
	case len(f.Scope.Errors()) > 0:
		return errInvalidScope.withDescription(f.Scope.Errors().Error())
	default:
		return newFormError(f)
	}
}

type deviceForm struct {
	baseForm
	ClientID forms.TextField `json:"client_id" validate:"trim required max_len:52"`
	Scope    scopeField      `json:"scope"     validate:"trim required scope_choices"`
}

func (f *deviceForm) getError() oauthError {
	switch {
	case len(f.Scope.Errors()) > 0:
		return errInvalidScope.withDescription(f.Scope.Errors().Error())
	default:
		return newFormError(f)
	}
}

type deviceAuthorizationForm struct {
	baseForm
	UserCode forms.TextField    `json:"user_code" validate:"max_len:16 clean_user_code"`
	Granted  forms.BooleanField `json:"granted"`
}

type tokenForm struct {
	baseForm
	GrantType    forms.TextField `json:"grant_type"    validate:"trim required grant_types"`
	Code         forms.TextField `json:"code"          validate:"trim required_with:authcode max_len:2048"`
	CodeVerifier forms.TextField `json:"code_verifier" validate:"trim required_with:authcode max_len:256"`
	DeviceCode   forms.TextField `json:"device_code"   validate:"trim required_with:device max_len:64"`
	ClientID     forms.TextField `json:"client_id"     validate:"trim required_with:device max_len:52"`
}

func (f *tokenForm) loadRequest() (*authCodeRequest, error) {
	data, err := base64.RawURLEncoding.DecodeString(f.Code.Value())
	if err != nil {
		return nil, err
	}

	// Decode encrypted request
	req := new(authCodeRequest)
	if err = configs.Keys.OauthRequestKey().DecodeJSON(data, req); err != nil {
		return nil, err
	}

	if !f.verifyChallenge(req.Challenge) {
		return nil, errInvalidChallenge
	}

	if time.Now().UTC().After(req.Expires) {
		return nil, errRequestExpired
	}

	return req, nil
}

func (f *tokenForm) verifyChallenge(challenge string) bool {
	c, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil {
		return false
	}

	h := sha256.New()
	h.Write([]byte(f.CodeVerifier.Value()))

	return subtle.ConstantTimeCompare(c, h.Sum(nil)) == 1
}

type revokeTokenForm struct {
	baseForm
	Token forms.TextField `json:"token" validate:"trim required max_len:64"`
}

func (f *revokeTokenForm) revoke(r *http.Request) error {
	tokenID, err := configs.Keys.TokenKey().Decode(f.Token.Value())
	if err != nil {
		return err
	}

	// must be authenticated with the same token
	if tokenID != auth.GetRequestAuthInfo(r).Provider.ID {
		return errAccessDenied
	}

	token, err := tokens.Tokens.GetOne(goqu.C("uid").Eq(tokenID))
	if err != nil {
		if errors.Is(err, tokens.ErrNotFound) {
			return nil
		}
		return err
	}

	return token.Delete()
}

// isValidClientURI checks the given client URL.
// It must be https only and resolve to an ip that is not
// private or a loopback address.
var isValidClientURI = forms.TypedValidator(func(v string) bool {
	u, err := url.Parse(v)
	if err != nil {
		return false
	}

	if u.Scheme != "https" {
		return false
	}
	if u.Hostname() == "" {
		return false
	}
	host, err := idna.ToASCII(u.Hostname())
	if err != nil {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}

	// Private and loopback is not allowed
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() {
			return false
		}
	}

	return true
}, errors.New("invalid client URI"))

var isValidLogoURI = forms.TypedValidator(func(v string) bool {
	if v == "" {
		return true
	}
	u, err := url.Parse(v)
	if err != nil {
		return false
	}

	if u.Scheme != "data" || !strings.HasPrefix(u.Opaque, "image/png;base64,") {
		return false
	}

	text, _ := strings.CutPrefix(u.Opaque, "image/png;base64,")
	_, err = png.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(text)))
	return err == nil
}, errors.New("invalid logo URI"))

var isValidRedirectURI = forms.TypedValidator(func(v string) bool {
	u, err := url.Parse(v)
	if err != nil {
		return false
	}

	switch u.Scheme {
	case "":
		return false
	case "https":
		// https needs a hostname
		return u.Hostname() != ""
	case "http":
		// only allow http with a loopback IP address
		host := u.Hostname()
		if ip, err := netip.ParseAddr(host); err == nil {
			if ip.IsLoopback() {
				return true
			}
		}
		return false
	default:
		// Allow URIs like net.myapp:auth-callback
		return true
	}
}, forms.ErrInvalidURL)

type scopeField struct {
	forms.ListField[string, scopeValue]
	forms.ChoicesField[string]
}

// nolint:unused
type scopeValue struct {
	forms.ListValue[string, forms.StringValue]
}

// nolint:unused
func (v *scopeValue) UnmarshalValues(data []string) error {
	values := []string{}
	for _, v := range data {
		values = append(values, strings.Fields(v)...)
	}

	return v.ListValue.UnmarshalValues(values)
}

// nolint:unused
func (v *scopeValue) UnmarshalJSON(data []byte) error {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	s, ok := decoded.(string)
	if !ok {
		return forms.ErrInvalidValue
	}

	return v.UnmarshalValues([]string{s})
}
