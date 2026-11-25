// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package catalog

import (
    "context"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"
)

// ImportDirectory imports the top-level complete GGUF models, never traversing
// symlinks or copying weights in link mode. Link mode is explicit because
// the source and destination share an inode: use only immutable trusted files.
func ImportDirectory(ctx context.Context, source, destination string, link bool) ([]string, error) {
    entries, err := os.ReadDir(source)
    if err != nil { return nil, err }
    if err := os.MkdirAll(destination, 0700); err != nil { return nil, err }
    srcInfo, err := os.Stat(source)
    if err != nil { return nil, err }
    dstInfo, err := os.Stat(destination)
    if err != nil { return nil, err }
    if os.SameFile(srcInfo, dstInfo) { return nil, errors.New("source and destination directories must differ") }
    var imported []string
    for _, entry := range entries {
        if err := ctx.Err(); err != nil { return imported, err }
        name := entry.Name()
        if !strings.EqualFold(filepath.Ext(name), ".gguf") || entry.IsDir() { continue }
        // Only first shards are model entry points.
        if match := shardPattern.FindStringSubmatch(name); match != nil && match[2] != "00001" { continue }
        if !safeName(name) { return imported, fmt.Errorf("invalid model filename: %q", name) }
        parts, err := ShardNames(name)
        if err != nil { return imported, err }
        // Validate the entire set before creating any destination file.
        for _, part := range parts {
            path := filepath.Join(source, part)
            info, err := os.Lstat(path)
            if err != nil { return imported, fmt.Errorf("model %s: %w", part, err) }
            if !info.Mode().IsRegular() { return imported, fmt.Errorf("model %s: not a regular file", part) }
            f, err := os.Open(path)
            if err != nil { return imported, err }
            opened, statErr := f.Stat()
            validErr := verifyGGUF(f)
            f.Close()
            if statErr != nil || !os.SameFile(info, opened) { return imported, fmt.Errorf("model %s changed during inspection", part) }
            if validErr != nil { return imported, fmt.Errorf("model %s: %w", part, validErr) }
            if _, err := os.Lstat(filepath.Join(destination, part)); err == nil {
                return imported, fmt.Errorf("model %s already exists", part)
            } else if !errors.Is(err, os.ErrNotExist) { return imported, err }
        }
        if !link {
            result, err := ImportFile(ctx, filepath.Join(source, name), destination, "")
            if err != nil { return imported, err }
            imported = append(imported, result)
            continue
        }
        // Publish the first shard last so the model is discoverable only once
        // the rest of its shards exist. Never silently fall back to copying.
        var created []string
        for i := len(parts)-1; i >= 0; i-- {
            target := filepath.Join(destination, parts[i])
            err := ctx.Err()
            if err == nil { err = os.Link(filepath.Join(source, parts[i]), target) }
            if err == nil {
                created = append(created, target)
                info, statErr := os.Lstat(target)
                if statErr != nil { err = statErr } else if !info.Mode().IsRegular() { err = errors.New("linked model is not a regular file") }
            }
            if err != nil {
                for _, file := range created { _ = os.Remove(file) }
                return imported, fmt.Errorf("link %s (same filesystem required): %w", parts[i], err)
            }
        }
        imported = append(imported, filepath.Join(destination, name))
    }
    return imported, nil
}
