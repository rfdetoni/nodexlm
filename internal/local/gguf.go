// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/catalog"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
)

// GGUFInfo describes model architecture without mapping or reading its weights.
// Metadata is read sequentially, with bounded allocations even for tokenizer arrays.
type GGUFInfo struct {
	PoolingType     uint32   `json:"pooling_type,omitempty"`
	Architecture    string   `json:"architecture,omitempty"`
	EmbeddingLength uint32   `json:"embedding_length,omitempty"`
	HeadCount       uint32   `json:"head_count,omitempty"`
	KVHeadCount     uint32   `json:"kv_head_count,omitempty"`
	BlockCount      uint32   `json:"block_count,omitempty"`
	ExpertCount     uint32   `json:"expert_count,omitempty"`
	ActiveExperts   uint32   `json:"active_experts,omitempty"`
	IsMoE           bool     `json:"moe"`
	BlockBytes      []uint64 `json:"-"`
	NonBlockBytes   uint64   `json:"-"`

	Trellis bool `json:"-"`
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
	return inspectGGUF(path, nil)
}

// InspectBundledGGUF validates every shard's tensor descriptors against the
// pinned runtime before any native startup, RPC handshake or weight transfer.
// Generic metadata inspection remains available for explicitly custom engines.
func InspectBundledGGUF(path string) (GGUFInfo, error) {
	return inspectRuntimeGGUF(path, false)
}

// InspectTrellisGGUF uses the separately pinned Strix enum, never stock RPC.
func InspectTrellisGGUF(path string) (GGUFInfo, error) {
	return inspectRuntimeGGUF(path, true)
}

func inspectRuntimeGGUF(path string, trellis bool) (GGUFInfo, error) {
	names, err := catalog.ShardNames(filepath.Base(path))
	if err != nil {
		return GGUFInfo{}, err
	}
	validate := llamaruntime.ValidateTensorType
	if trellis {
		validate = llamaruntime.ValidateTrellisTensorType
	}
	var info GGUFInfo
	for i, name := range names {
		part, err := inspectGGUF(filepath.Join(filepath.Dir(path), name), validate)
		if err != nil {
			return GGUFInfo{}, fmt.Errorf("GGUF %s: %w", name, err)
		}
		if i == 0 {
			info = part
		}
	}
	if !trellis {
		if err := llamaruntime.ValidateArchitecture(info.Architecture); err != nil {
			return GGUFInfo{}, err
		}
	}
	info.Trellis = trellis
	return info, nil
}

func inspectGGUF(path string, validate func(uint32, string) error) (GGUFInfo, error) {
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
	tensorCount, err := g.uint64()
	if err != nil {
		return GGUFInfo{}, err
	}
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
	values := map[string]uint32{}
	for i := uint64(0); i < count; i++ {
		key, err := g.str(4096)
		if err != nil {
			return GGUFInfo{}, fmt.Errorf("GGUF metadata key: %w", err)
		}
		kind, err := g.uint32()
		if err != nil {
			return GGUFInfo{}, err
		}
		capture := strings.HasSuffix(key, ".pooling_type") || key == "general.architecture" || strings.HasSuffix(key, ".expert_count") || strings.HasSuffix(key, ".expert_used_count") || strings.HasSuffix(key, ".block_count") || strings.HasSuffix(key, ".embedding_length") || strings.HasSuffix(key, ".attention.head_count") || strings.HasSuffix(key, ".attention.head_count_kv")
		value, text, err := g.value(kind, capture)
		if err != nil {
			return GGUFInfo{}, fmt.Errorf("GGUF metadata %s: %w", key, err)
		}
		if capture {
			values[key] = value
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
	result.PoolingType = values[result.Architecture+".pooling_type"]
	result.EmbeddingLength = values[result.Architecture+".embedding_length"]
	result.HeadCount = values[result.Architecture+".attention.head_count"]
	result.KVHeadCount = values[result.Architecture+".attention.head_count_kv"]
	if validate != nil {
		pos, err := g.r.Seek(0, io.SeekCurrent)
		if err != nil {
			return GGUFInfo{}, err
		}
		if err := g.validateTensorTypes(tensorCount, validate); err != nil {
			return GGUFInfo{}, err
		}
		if _, err := g.r.Seek(pos, io.SeekStart); err != nil {
			return GGUFInfo{}, err
		}
	}
	// Sharded/unknown tensor layouts retain the uniform estimate rather
	// than treating a partial tensor table as the entire model.
	if result.BlockCount > 0 && result.BlockCount <= 4096 && tensorCount > 0 && tensorCount <= 1_000_000 && !strings.Contains(path, "-of-") {
		result.BlockBytes, result.NonBlockBytes = g.tensorBytes(tensorCount, result.BlockCount)
	}
	return result, nil
}

// The enum is taken from ggml/include/ggml.h in the pinned source revision.
// The supported ID range is not the same as the weight-estimation table.
func (g *ggufReader) validateTensorTypes(count uint64, validate func(uint32, string) error) error {
	if count > 1_000_000 {
		return errors.New("too many GGUF tensors")
	}
	for i := uint64(0); i < count; i++ {
		name, err := g.str(4096)
		if err != nil {
			return fmt.Errorf("GGUF tensor descriptor: %w", err)
		}
		dims, err := g.uint32()
		if err != nil || dims < 1 || dims > 4 {
			return fmt.Errorf("invalid GGUF tensor dimensions for %q", name)
		}
		if err := g.skip(uint64(dims) * 8); err != nil {
			return err
		}
		kind, err := g.uint32()
		if err != nil {
			return err
		}
		if err := validate(kind, name); err != nil {
			return err
		}
		if err := g.skip(8); err != nil {
			return err
		}
	}
	return nil
}

// Known GGML types from the pinned runtime. Unknown types deliberately
// disable the detailed estimate; never infer a quantization block size.
func tensorTypeSize(kind uint32) (uint64, uint64) {
	switch kind {
	case 0:
		return 1, 4 // F32
	case 1, 30:
		return 1, 2 // F16, BF16
	case 2:
		return 32, 18 // Q4_0
	case 3:
		return 32, 20 // Q4_1
	case 6:
		return 32, 22 // Q5_0
	case 7:
		return 32, 24 // Q5_1
	case 8:
		return 32, 34 // Q8_0
	case 10:
		return 256, 84 // Q2_K
	case 11:
		return 256, 110 // Q3_K
	case 12:
		return 256, 144 // Q4_K
	case 13:
		return 256, 176 // Q5_K
	case 14:
		return 256, 210 // Q6_K
	case 15:
		return 256, 292 // Q8_K
	case 16:
		return 256, 66 // IQ2_XXS
	case 17:
		return 256, 74 // IQ2_XS
	case 18:
		return 256, 98 // IQ3_XXS
	case 19:
		return 256, 50 // IQ1_S
	case 20:
		return 32, 18 // IQ4_NL
	case 21:
		return 256, 110 // IQ3_S
	case 22:
		return 256, 82 // IQ2_S
	case 23:
		return 256, 136 // IQ4_XS
	case 24:
		return 1, 1 // I8
	case 25:
		return 1, 2 // I16
	case 26:
		return 1, 4 // I32
	case 27, 28:
		return 1, 8 // I64, F64
	case 29:
		return 256, 56 // IQ1_M
	case 144:
		return 128, 34 // TQ2_T
	case 145:
		return 128, 26 // TQK6
	case 146:
		return 128, 30 // TQK7
	case 147:
		return 128, 42 // TQP5
	case 148:
		return 128, 50 // TQP6
	case 149:
		return 128, 58 // TQP7
	default:
		return 0, 0
	}
}

func (g *ggufReader) tensorBytes(count uint64, blocks uint32) ([]uint64, uint64) {
	layers := make([]uint64, blocks)
	var other, total, maxEnd uint64
	for i := uint64(0); i < count; i++ {
		name, err := g.str(4096)
		if err != nil {
			return nil, 0
		}
		dims, err := g.uint32()
		if err != nil || dims < 1 || dims > 4 {
			return nil, 0
		}
		var first uint64
		elements := uint64(1)
		for d := uint32(0); d < dims; d++ {
			n, err := g.uint64()
			if err != nil || n == 0 || elements > ^uint64(0)/n {
				return nil, 0
			}
			if d == 0 {
				first = n
			}
			elements *= n
		}
		kind, err := g.uint32()
		if err != nil {
			return nil, 0
		}
		offset, err := g.uint64()
		block, size := tensorTypeSize(kind)
		if err != nil || block == 0 || first%block != 0 || elements/block > ^uint64(0)/size {
			return nil, 0
		}
		weight := elements / block * size
		if weight > uint64(g.size) || offset > uint64(g.size)-weight || total > uint64(g.size)-weight {
			return nil, 0
		}
		total += weight
		maxEnd = max(maxEnd, offset+weight)
		if rest, ok := strings.CutPrefix(name, "blk."); ok {
			index, _, found := strings.Cut(rest, ".")
			n, err := strconv.ParseUint(index, 10, 32)
			if !found || err != nil || n >= uint64(blocks) {
				return nil, 0
			}
			layers[n] += weight
		} else {
			other += weight
		}
	}
	pos, err := g.r.Seek(0, io.SeekCurrent)
	if err != nil || maxEnd > uint64(g.size-pos) || total > uint64(g.size-pos) {
		return nil, 0
	}
	for _, size := range layers {
		if size == 0 {
			return nil, 0
		}
	}
	return layers, other
}
