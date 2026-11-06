// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// GGUFInfo describes model architecture without mapping or reading its weights.
// Metadata is read sequentially, with bounded allocations even for tokenizer arrays.
type GGUFInfo struct {
	Architecture  string `json:"architecture,omitempty"`
	BlockCount    uint32 `json:"block_count,omitempty"`
	ExpertCount   uint32 `json:"expert_count,omitempty"`
	ActiveExperts uint32 `json:"active_experts,omitempty"`
	IsMoE         bool   `json:"moe"`
}

const (
	ggufUint8 = iota
	ggufInt8
	ggufUint16
	ggufInt16
	ggufUint32
	ggufInt32
	ggufFloat32
	ggufBool
	ggufString
	ggufArray
	ggufUint64
	ggufInt64
	ggufFloat64
)

// ggufReader bounds seeks and input lengths to the actual file size.
type ggufReader struct {
	r    *io.SectionReader
	size int64
}

func (g *ggufReader) uint32() (uint32, error) {
	var b [4]byte
	_, err := io.ReadFull(g.r, b[:])
	return binary.LittleEndian.Uint32(b[:]), err
}
func (g *ggufReader) uint64() (uint64, error) {
	var b [8]byte
	_, err := io.ReadFull(g.r, b[:])
	return binary.LittleEndian.Uint64(b[:]), err
}
func (g *ggufReader) skip(n uint64) error {
	pos, err := g.r.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if n > uint64(g.size-pos) {
		return io.ErrUnexpectedEOF
	}
	_, err = g.r.Seek(int64(n), io.SeekCurrent)
	return err
}
func (g *ggufReader) str(limit uint64) (string, error) {
	n, err := g.uint64()
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("GGUF metadata string exceeds %d bytes", limit)
	}
	b := make([]byte, int(n))
	if _, err := io.ReadFull(g.r, b); err != nil {
		return "", err
	}
	return string(b), nil
}
func typeSize(kind uint32) uint64 {
	switch kind {
	case ggufUint8, ggufInt8, ggufBool:
		return 1
	case ggufUint16, ggufInt16:
		return 2
	case ggufUint32, ggufInt32, ggufFloat32:
		return 4
	case ggufUint64, ggufInt64, ggufFloat64:
		return 8
	}
	return 0
}
func (g *ggufReader) value(kind uint32, capture bool) (uint32, string, error) {
	if kind == ggufArray {
		elemKind, err := g.uint32()
		if err != nil {
			return 0, "", err
		}
		n, err := g.uint64()
		if err != nil {
			return 0, "", err
		}
		if elemKind == ggufArray || elemKind > ggufFloat64 {
			return 0, "", errors.New("unsupported GGUF array element type")
		}
		if elemKind == ggufString {
			if n > 4_000_000 {
				return 0, "", errors.New("GGUF string array too large")
			}
			for i := uint64(0); i < n; i++ {
				length, err := g.uint64()
				if err != nil {
					return 0, "", err
				}
				if err := g.skip(length); err != nil {
					return 0, "", err
				}
			}
			return 0, "", nil
		}
		elemSize := typeSize(elemKind)
		if elemSize == 0 || n > uint64(g.size)/elemSize {
			return 0, "", errors.New("GGUF array size invalid")
		}
		return 0, "", g.skip(n * elemSize)
	}
	if kind == ggufString {
		if capture {
			s, err := g.str(256)
			return 0, s, err
		}
		length, err := g.uint64()
		if err != nil {
			return 0, "", err
		}
		return 0, "", g.skip(length)
	}
	size := typeSize(kind)
	if size == 0 {
		return 0, "", errors.New("unknown GGUF metadata type")
	}
	if capture && kind == ggufUint32 {
		value, err := g.uint32()
		return value, "", err
	}
	if capture && kind == ggufUint64 {
		value, err := g.uint64()
		if err != nil {
			return 0, "", err
		}
		if value > 1<<32-1 {
			return 0, "", errors.New("GGUF expert count overflow")
		}
		return uint32(value), "", nil
	}
	return 0, "", g.skip(size)
}

func InspectGGUF(path string) (GGUFInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return GGUFInfo{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return GGUFInfo{}, err
	}
	g := ggufReader{r: io.NewSectionReader(f, 0, stat.Size()), size: stat.Size()}
	var magic [4]byte
	if _, err = io.ReadFull(g.r, magic[:]); err != nil {
		return GGUFInfo{}, err
	}
	if string(magic[:]) != "GGUF" {
		return GGUFInfo{}, errors.New("not a GGUF model")
	}
	version, err := g.uint32()
	if err != nil {
		return GGUFInfo{}, err
	}
	if version != 2 && version != 3 {
		return GGUFInfo{}, fmt.Errorf("GGUF version %d unsupported", version)
	}
	if _, err = g.uint64(); err != nil {
		return GGUFInfo{}, err
	} // tensor count
	count, err := g.uint64() // metadata count
	if err != nil {
		return GGUFInfo{}, err
	}
	if count > 1_000_000 {
		return GGUFInfo{}, errors.New("too many GGUF metadata entries")
	}
	var result GGUFInfo
	var experts, active, blocks uint32
	var expertKey, activeKey, blockKey string
	for i := uint64(0); i < count; i++ {
		key, err := g.str(4096)
		if err != nil {
			return GGUFInfo{}, fmt.Errorf("GGUF metadata key: %w", err)
		}
		kind, err := g.uint32()
		if err != nil {
			return GGUFInfo{}, err
		}
		capture := key == "general.architecture" || strings.HasSuffix(key, ".expert_count") || strings.HasSuffix(key, ".expert_used_count") || strings.HasSuffix(key, ".block_count")
		value, text, err := g.value(kind, capture)
		if err != nil {
			return GGUFInfo{}, fmt.Errorf("GGUF metadata %s: %w", key, err)
		}
		switch {
		case key == "general.architecture":
			result.Architecture = text
		case strings.HasSuffix(key, ".block_count"):
			blocks, blockKey = value, key
		case strings.HasSuffix(key, ".expert_count"):
			experts, expertKey = value, key
		case strings.HasSuffix(key, ".expert_used_count"):
			active, activeKey = value, key
		}
	}
	if result.Architecture != "" && blockKey == result.Architecture+".block_count" {
		result.BlockCount = blocks
	}
	if result.Architecture != "" && expertKey == result.Architecture+".expert_count" {
		result.ExpertCount = experts
		if activeKey == result.Architecture+".expert_used_count" {
			result.ActiveExperts = active
		}
		result.IsMoE = result.ExpertCount > 1
	}
	return result, nil
}
