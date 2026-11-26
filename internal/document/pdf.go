package document

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

const maxPDFBytes = 16 << 20

// Use a locally installed Poppler executable; no network or OCR. Read the
// file from stdin so the parser never interprets a filename as a switch.
func readPDF(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxPDFBytes {
		return "", errors.New("PDF must be a regular file of at most 16 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() > maxPDFBytes {
		return "", errors.New("PDF changed during opening")
	}
	var magic [5]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != "%PDF-" {
		return "", errors.New("invalid PDF header")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", errors.New("install Poppler pdftotext to extract PDF text")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-enc", "UTF-8", "-nopgbrk", "-", "-")
	cmd.Stdin = io.LimitReader(f, maxPDFBytes)
	cmd.Stderr = io.Discard
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(out, MaxText+1))
	if readErr != nil || len(data) > MaxText {
		cancel()
		_ = cmd.Wait()
		return "", errors.New("PDF extracted text exceeds limit or could not be read")
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("PDF text extraction failed: %w", err)
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 || strings.TrimSpace(string(data)) == "" {
		return "", errors.New("PDF has no valid extractable text; scanned documents require OCR")
	}
	return string(data), nil
}
