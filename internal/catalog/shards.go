// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package catalog

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
)

var shardPattern = regexp.MustCompile(`^(.*)-([0-9]{5})-of-([0-9]{5})\.gguf$`)

// ShardNames expands a first GGUF shard into the complete ordered set. Files
// not matching the llama.cpp shard convention remain single-file models.
// The bound avoids untrusted filenames asking the downloader for thousands of files.
func ShardNames(filename string) ([]string, error) {
	matches := shardPattern.FindStringSubmatch(filename)
	if matches == nil {
		if path.Ext(filename) != ".gguf" {
			return nil, errors.New("invalid GGUF filename")
		}
		return []string{filename}, nil
	}
	total, _ := strconv.Atoi(matches[3])
	first, _ := strconv.Atoi(matches[2])
	if first != 1 || total < 2 || total > 128 {
		return nil, errors.New("sharded GGUF requires first shard and 2-128 parts")
	}
	result := make([]string, 0, total)
	for i := 1; i <= total; i++ {
		result = append(result, fmt.Sprintf("%s-%05d-of-%05d.gguf", matches[1], i, total))
	}
	return result, nil
}
