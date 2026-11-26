package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/pairing"
)

func pairingLink(host, code string) (string, error) {
	u, err := url.Parse(host)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("QR host must be the HTTPS LAN endpoint, without credentials or paths")
	}
	if !pairing.ValidCode(code) {
		return "", errors.New("invalid pairing code")
	}
	params := url.Values{"host": {strings.TrimRight(u.String(), "/")}, "code": {code}}
	return "nodexlm://pair?"+params.Encode(), nil
}

// qrencode is an optional local executable, not a web QR service. The code
// travels via stdin rather than appearing in process arguments or a file.
func printPairingQR(ctx context.Context, host, code string) error {
	link, err := pairingLink(host, code)
	if err != nil {
		return err
	}
	bin, err := exec.LookPath("qrencode")
	if err != nil {
		return errors.New("install qrencode to display a pairing QR; the text code remains valid")
	}
	cmd := exec.CommandContext(ctx, bin, "-t", "ANSIUTF8", "-m", "4")
	cmd.Stdin = strings.NewReader(link)
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
