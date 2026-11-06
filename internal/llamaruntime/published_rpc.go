// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package llamaruntime

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "runtime"
    "strings"
    "time"
)

// Each official NodexLM RPC bundle uses exactly the same pinned llama.cpp
// commit as the source-build fallback. No locally installed compiler is needed.
// This is a dedicated release, separate from the frequently updated CLI edge.
const publishedRPCBaseURL = "https://github.com/rfdetoni/nodexlm/releases/download/nodexlm-rpc-b12143"
const maxPublishedArchive = int64(400 << 20)

// pinnedRPCHash is set to the reviewed published artifact digest, independent
// of the downloaded SHA256SUMS file. This avoids trusting a mutable manifest.
// The initial release is built before this checksum can be filled.
const pinnedRPCHash = ""

func publishedRPCSupported(backend string) bool {
    return runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && backend == "cpu"
}

func publishedChecksum(body []byte, name string) (string, error) {
    result := ""
    for _, line := range strings.Split(string(body), "\n") {
        fields := strings.Fields(line)
        if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
            continue
        }
        hash := strings.ToLower(fields[0])
        if len(hash) != 64 {
            return "", errors.New("invalid published RPC checksum length")
        }
        if _, err := hex.DecodeString(hash); err != nil {
            return "", errors.New("invalid published RPC checksum characters")
        }
        if result != "" {
            return "", errors.New("duplicate published RPC checksum entry")
        }
        result = hash
    }
    if result == "" {
        return "", fmt.Errorf("published RPC checksum missing for %s", name)
    }
    return result, nil
}

func fetchPublishedRPC(ctx context.Context, home, backend, baseURL string) (string, string, error) {
    name := fmt.Sprintf("nodexlm-rpc_%s_%s_%s_%s.tar.gz", Version, runtime.GOOS, runtime.GOARCH, backend)
    dest := installDir(home, backend) + "-rpc"
    if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
        return "", "", err
    }
    stage, err := os.MkdirTemp(filepath.Dir(dest), ".rpc-download-")
    if err != nil { return "", "", err }
    defer os.RemoveAll(stage)

    client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
        if req.URL.Scheme != "https" || len(via) > 8 { return errors.New("unsafe RPC runtime redirect") }
        return nil
    }}
    get := func(filename string, max int64) ([]byte, error) {
        req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/"+filename, nil)
        if err != nil { return nil, err }
        response, err := client.Do(req)
        if err != nil { return nil, err }
        defer response.Body.Close()
        if response.StatusCode != http.StatusOK {
            return nil, fmt.Errorf("RPC runtime %s HTTP %d", filename, response.StatusCode)
        }
        if response.ContentLength > max { return nil, errors.New("RPC runtime response exceeds limit") }
        b, err := io.ReadAll(io.LimitReader(response.Body, max+1))
        if err != nil { return nil, err }
        if int64(len(b)) > max { return nil, errors.New("RPC runtime response exceeds limit") }
        return b, nil
    }

    manifest, err := get("SHA256SUMS.txt", 128<<10)
    if err != nil { return "", "", err }
    expected, err := publishedChecksum(manifest, name)
    if err != nil { return "", "", err }
    if pinnedRPCHash != "" && expected != pinnedRPCHash {
        return "", "", errors.New("RPC manifest disagrees with pinned artifact hash")
    }

    // Stream the large archive to disk instead of buffering it in Go's heap.
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/"+name, nil)
    if err != nil { return "", "", err }
    response, err := client.Do(req)
    if err != nil { return "", "", err }
    defer response.Body.Close()
    if response.StatusCode != http.StatusOK {
        return "", "", fmt.Errorf("RPC runtime archive HTTP %d", response.StatusCode)
    }
    if response.ContentLength > maxPublishedArchive {
        return "", "", errors.New("RPC runtime archive exceeds size limit")
    }
    file, err := os.CreateTemp(stage, "runtime-")
    if err != nil { return "", "", err }
    defer file.Close()
    hash := sha256.New()
    n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxPublishedArchive+1))
    if err != nil { return "", "", err }
    if n > maxPublishedArchive { return "", "", errors.New("RPC runtime archive exceeds size limit") }
    actual := hex.EncodeToString(hash.Sum(nil))
    if actual != expected { return "", "", errors.New("RPC runtime archive SHA-256 mismatch") }
    if pinnedRPCHash != "" && actual != pinnedRPCHash {
        return "", "", errors.New("RPC runtime archive disagrees with pinned SHA-256")
    }
    if _, err := file.Seek(0, 0); err != nil { return "", "", err }
    payload := filepath.Join(stage, "payload")
    if err := os.Mkdir(payload, 0700); err != nil { return "", "", err }
    if err := extractTar(file, payload); err != nil { return "", "", err }
    if executable(payload, "llama-server") == "" ||
       (executable(payload, "ggml-rpc-server") == "" && executable(payload, "rpc-server") == "") {
        return "", "", errors.New("verified RPC runtime archive has no matching server pair")
    }
    if err := os.Rename(payload, dest); err != nil {
        if server, rpc, existingErr := installedDistributed(home, backend); existingErr == nil {
            return server, rpc, nil
        }
        return "", "", err
    }
    return installedDistributed(home, backend)
}
