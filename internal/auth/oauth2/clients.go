// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2

import (
	"net/http"
	"slices"
	"time"

	"codeberg.org/readeck/readeck/internal/auth/tokens"
	"codeberg.org/readeck/readeck/internal/bus"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/forms"
)

const (
	clientTTL = time.Minute * 10
)

type oauthClient struct {
	ID                      string   `json:"client_id"`
	Name                    string   `json:"client_name"`
	URI                     string   `json:"client_uri"`
	Logo                    string   `json:"logo_uri"`
	RedirectURIs            []string `json:"redirect_uris"`
	SoftwareID              string   `json:"software_id"`
	SoftwareVersion         string   `json:"software_version"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

func loadClient(id string, grantType string) (*oauthClient, error) {
	c := &oauthClient{}
	if err := bus.Get("oauth:client:"+id, c); err != nil {
		return nil, errServerError.withError(err)
	}
	if c.ID == "" {
		return nil, errInvalidClient
	}

	if !slices.Contains(c.GrantTypes, grantType) {
		return nil, errUnauthorizedClient
	}

	return c, nil
}

func (c *oauthClient) store() error {
	return bus.Set("oauth:client:"+c.ID, c, clientTTL)
}

func (c *oauthClient) remove() error {
	if c.ID == "" {
		return nil
	}
	return bus.Store().Del("oauth:client:" + c.ID)
}

func (c *oauthClient) toClientInfo() *tokens.ClientInfo {
	return &tokens.ClientInfo{
		ID:              c.ID,
		Name:            c.Name,
		Website:         c.URI,
		Logo:            c.Logo,
		GrantTypes:      c.GrantTypes,
		SoftwareID:      c.SoftwareID,
		SoftwareVersion: c.SoftwareVersion,
	}
}

// clientCreate creates a new client that is stored in the K/V store
// for [clientTTL] duration.
func (api *oauthAPI) clientCreate(w http.ResponseWriter, r *http.Request) {
	f := forms.BindAs[clientForm](r)

	if !f.IsValid() {
		server.Err(w, r, f.getError())
		return
	}

	client, err := f.createClient()
	if err != nil {
		server.Err(w, r, errServerError.withError(err))
		return
	}

	server.Render(w, r, http.StatusCreated, client)
}
