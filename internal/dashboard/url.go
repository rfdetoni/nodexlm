// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"errors"
	"net/url"
	"strings"
)

func parseModelURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if err = validateURL(u); err != nil {
		return nil, err
	}
	host := strings.ToLower(u.Hostname())
	if host != "huggingface.co" && host != "hf.co" {
		return nil, errors.New("model URL must start on huggingface.co or hf.co")
	}
	if !strings.HasSuffix(strings.ToLower(u.Path), ".gguf") {
		return nil, errors.New("URL must point to a GGUF file")
	}
	return u, nil
}
func validateURL(u *url.URL) error {
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Port() != "" {
		return errors.New("only HTTPS URLs with no credentials or custom ports are permitted")
	}
	return nil
}
