package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/pairing"
)

// qrencode is an optional local executable, not a web QR service. The code
// travels via stdin rather than appearing in process arguments or a file.
func printPairingQR(ctx context.Context, host, code string) error {
	link, err := pairing.Link(host, code)
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
