package pairing

import (
	"errors"
	"net/url"
	"strings"
)

// Link encodes a short-lived pairing code and explicit HTTPS endpoint.
func Link(host, code string) (string, error) {
	u, err := url.Parse(host)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("QR host must be the HTTPS LAN endpoint, without credentials or paths")
	}
	if !ValidCode(code) {
		return "", errors.New("invalid pairing code")
	}
	params := url.Values{"host": {strings.TrimRight(u.String(), "/")}, "code": {code}}
	return "nodexlm://pair?" + params.Encode(), nil
}
