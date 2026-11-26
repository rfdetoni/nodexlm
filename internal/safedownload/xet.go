package safedownload

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
    "time"
)

type xetLocation struct {
    Repository string
    Filename string
    Revision string
}

// parseXetLocation forbids mutable revisions, arbitrary URLs, encoded path
// separators and special path segments before invoking an external client.
func parseXetLocation(source string) (xetLocation, error) {
    u, err := url.Parse(source)
    if err != nil || ValidateURL(u) != nil || u.Host != "huggingface.co" ||
        u.RawQuery != "" || u.ForceQuery {
        return xetLocation{}, errors.New("Xet requires canonical Hugging Face HTTPS")
    }
    raw := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
    if len(raw) < 5 || len(raw) > 23 || raw[2] != "resolve" {
        return xetLocation{}, errors.New("invalid immutable Xet path")
    }
    rev := raw[3]
    if len(rev) != 40 {
        return xetLocation{}, errors.New("Xet requires a pinned commit")
    }
    if _, err = hex.DecodeString(rev); err != nil {
        return xetLocation{}, errors.New("Xet revision is not a SHA-1 commit ID")
    }
    parts := make([]string, 0, len(raw))
    for _, part := range append([]string{raw[0], raw[1]}, raw[4:]...) {
        value, err := url.PathUnescape(part)
        if err != nil || value == "" || value == "." || value == ".." || len(value) > 180 ||
            strings.ContainsAny(value, "/\\:%?#") {
            return xetLocation{}, errors.New("unsafe Xet path segment")
        }
        parts = append(parts, value)
    }
    filename := strings.Join(parts[2:], "/")
    if !strings.EqualFold(filepath.Ext(filename), ".gguf") {
        return xetLocation{}, errors.New("Xet content must be GGUF")
    }
    return xetLocation{Repository: parts[0] + "/" + parts[1], Filename: filename, Revision: rev}, nil
}

// TryXet uses the official installed Hugging Face CLI (hf-xet/xet-core), never
// reimplements the CAS reconstruction or trusts a CLI success code as proof
// of model integrity. The normal bounded HTTP downloader remains the default.
// It is deliberately disabled on Android (which has a separate APK transport).
func TryXet(ctx context.Context, client *http.Client, source, target string, limit int64, progress func(int64, int64)) (int64, bool, error) {
    if os.Getenv("NODEXLM_DOWNLOAD_XET") != "1" || runtime.GOOS == "android" {
        return 0, false, nil
    }
    parsed, err := parseXetLocation(source)
    if err != nil {
        return 0, false, nil
    }
    binary, err := exec.LookPath("hf")
    if err != nil {
        return 0, false, nil
    }
    if _, err := os.Lstat(target); err == nil {
        // Existing resumable stages belong to the Go implementation.
        return 0, false, nil
    } else if !errors.Is(err, os.ErrNotExist) {
        return 0, true, err
    }
    if client == nil {
        client = Client()
    }
    probe, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()
    req, err := http.NewRequestWithContext(probe, http.MethodHead, source, nil)
    if err != nil {
        return 0, true, err
    }
    req.Header.Set("Accept-Encoding", "identity")
    response, err := client.Do(req)
    if err != nil {
        return 0, false, nil // Proxy/CDN does not support Xet metadata; use HTTPS.
    }
    _ = response.Body.Close()
    if response.StatusCode != http.StatusOK {
        return 0, false, nil
    }
    var digest []byte
    for current := response; current != nil; {
        if result, valid := ExpectedSHA256(current.Header); valid {
            digest = result
        }
        if current.Request == nil {
            break
        }
        current = current.Request.Response
    }
    if len(digest) != sha256.Size || response.ContentLength < 8 || response.ContentLength > limit {
        return 0, false, nil // Never run a CLI without strong identity and bounds.
    }
    expected := hex.EncodeToString(digest)
    expectedSize := response.ContentLength
    tmp, err := os.MkdirTemp(filepath.Dir(target), ".nodexlm-xet-*")
    if err != nil {
        return 0, true, err
    }
    defer os.RemoveAll(tmp)
    command := exec.CommandContext(ctx, binary, "download", parsed.Repository, parsed.Filename,
        "--revision", parsed.Revision, "--local-dir", tmp)
    // The CLI is an optional installed dependency. Do not capture unbounded
    // output, inject a shell, or pass Hub/admin/Guest secrets in arguments.
    command.Stdout, command.Stderr = io.Discard, io.Discard
    if err := command.Run(); err != nil {
        if ctx.Err() != nil {
            return 0, true, ctx.Err()
        }
        return 0, false, nil // Official CLI unavailable/unsupported; HTTPS fallback.
    }
    output := filepath.Join(tmp, filepath.FromSlash(parsed.Filename))
    info, err := os.Lstat(output)
    if err != nil || !info.Mode().IsRegular() || info.Size() != expectedSize {
        return 0, true, errors.New("Xet file missing, symbolic or wrong size")
    }
    input, err := os.Open(output)
    if err != nil {
        return 0, true, err
    }
    defer input.Close()
    hash := sha256.New()
    if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: input}); err != nil {
        return 0, true, err
    }
    if hex.EncodeToString(hash.Sum(nil)) != expected {
        return 0, true, errors.New("Xet model checksum mismatch")
    }
    var header [4]byte
    if _, err := input.ReadAt(header[:], 0); err != nil || string(header[:]) != "GGUF" {
        return 0, true, errors.New("Xet output is not GGUF")
    }
    // An interrupted request can never publish an unverified partial model.
    if err := ctx.Err(); err != nil {
        return 0, true, err
    }
    meta := partial{Source: source, Validator: fmt.Sprintf("%q", expected), SHA256: expected, Total: expectedSize}
    raw, err := json.Marshal(meta)
    if err != nil {
        return 0, true, err
    }
    if err := os.Rename(output, target); err != nil {
        return 0, true, err
    }
    if err := os.WriteFile(target+".json", raw, 0600); err != nil {
        _ = os.Remove(target)
        return 0, true, err
    }
    if progress != nil {
        progress(expectedSize, expectedSize)
    }
    return expectedSize, true, nil
}
