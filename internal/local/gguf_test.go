// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ggufTestFile(arch string, experts, used uint32) []byte {
	var b bytes.Buffer
	write := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	key := func(k string, kind uint32) {
		write(uint64(len(k)))
		b.WriteString(k)
		write(kind)
	}
	b.WriteString("GGUF")
	write(uint32(3))
	write(uint64(0))
	count := uint64(3) // arch, transformer blocks, and unknown tokenizer array
	if experts != 0 {
		count += 2
	}
	write(count)
	key("general.architecture", ggufString)
	write(uint64(len(arch)))
	b.WriteString(arch)
	key(arch+".block_count", ggufUint32)
	write(uint32(32))
	key("tokenizer.ggml.tokens", ggufArray)
	write(uint32(ggufString))
	write(uint64(2))
	for _, token := range []string{"hello", "world"} {
		write(uint64(len(token)))
		b.WriteString(token)
	}
	if experts != 0 {
		key(arch+".expert_count", ggufUint32)
		write(experts)
		key(arch+".expert_used_count", ggufUint32)
		write(used)
	}
	return b.Bytes()
}

func TestInspectMoEAndDenseGGUF(t *testing.T) {
	for _, test := range []struct {
		name, arch    string
		experts, used uint32
		moe           bool
	}{
		{"moe", "qwen3moe", 128, 8, true},
		{"dense", "llama", 0, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "model.gguf")
			if err := os.WriteFile(path, ggufTestFile(test.arch, test.experts, test.used), 0600); err != nil {
				t.Fatal(err)
			}
			info, err := InspectGGUF(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Architecture != test.arch || info.BlockCount != 32 || info.ExpertCount != test.experts || info.ActiveExperts != test.used || info.IsMoE != test.moe {
				t.Fatalf("info %+v", info)
			}
			models, err := Models(filepath.Dir(path))
			if err != nil || len(models) != 1 || models[0].MoE != test.moe {
				t.Fatalf("models %+v err %v", models, err)
			}
		})
	}
}
func TestRejectMalformedGGUF(t *testing.T) {
	for _, payload := range [][]byte{[]byte("GGUF"), []byte("GGUFnotheader"), []byte("OTHER"), append(ggufTestFile("llama", 0, 0)[:25:25], 0xFF)} {
		path := filepath.Join(t.TempDir(), "bad.gguf")
		if err := os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := InspectGGUF(path); err == nil {
			t.Fatal("malformed GGUF passed")
		}
	}
}
func TestMoEOptions(t *testing.T) {
	moe := GGUFInfo{IsMoE: true, ExpertCount: 64}
	for _, m := range []*Managed{
		{MoECPU: true}, {MoECPULayers: 18}, {MoECPU: true, MoECPULayers: 2}, {MoECacheMiB: 256}, {MoECPULayers: -1},
	} {
		err := m.validateMoE(moe)
		if (m.MoECPU && m.MoECPULayers == 0 || m.MoECPULayers > 0 && !m.MoECPU) && m.MoECacheMiB == 0 && err != nil {
			t.Fatalf("valid opts rejected: %v", err)
		}
		if (m.MoECPU && m.MoECPULayers > 0 || m.MoECacheMiB > 0 && !m.MoECPU && m.MoECPULayers == 0 || m.MoECPULayers < 0) && err == nil {
			t.Fatalf("invalid opts accepted: cpu=%v layers=%d cache=%d", m.MoECPU, m.MoECPULayers, m.MoECacheMiB)
		}
	}
	if err := (&Managed{MoECPU: true}).validateMoE(GGUFInfo{}); err == nil {
		t.Fatal("dense accepted MoE-only flags")
	}
	m := &Managed{MoECPU: true, MoECacheMiB: 256, ModelPath: "model.gguf", Alias: "model"}
	args := m.args(8080, 2048, 4, "auto", 0)
	if !reflect.DeepEqual(args[len(args)-3:], []string{"--cpu-moe", "--moe-cache-mib", "256"}) {
		t.Fatalf("wrong args: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "--no-webui") {
		t.Fatalf("missing default args: %v", args)
	}
}

func TestInspectGGUFTensorBytes(t *testing.T) {
	raw := ggufTestFile("llama", 0, 0)
	binary.LittleEndian.PutUint64(raw[8:16], 33)
	var b bytes.Buffer
	b.Write(raw)
	write := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	var offset uint64
	for i := 0; i < 33; i++ {
		name := fmt.Sprintf("blk.%d.weight", i)
		n := uint64(32)
		if i == 0 {
			n = 64
		}
		if i == 32 {
			name = "token_embd.weight"
		}
		write(uint64(len(name)))
		b.WriteString(name)
		write(uint32(1))
		write(n)
		write(uint32(2))
		write(offset)
		offset += n / 32 * 18
	}
	b.Write(make([]byte, offset+32))
	path := filepath.Join(t.TempDir(), "layers.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	meta, err := InspectGGUF(path)
	if err != nil || len(meta.BlockBytes) != 32 || meta.BlockBytes[0] != 36 || meta.BlockBytes[1] != 18 || meta.NonBlockBytes != 18 {
		t.Fatalf("unexpected tensor sizes: %+v %v", meta, err)
	}
}

func TestBundledGGUFRejectsCustomTensorTypeAfterStandardType(t *testing.T) {
	raw := ggufTestFile("llama", 0, 0)
	binary.LittleEndian.PutUint64(raw[8:16], 2)
	var b bytes.Buffer
	b.Write(raw)
	write := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	for i, kind := range []uint32{35, 144} {
		name := fmt.Sprintf("blk.%d.weight", i)
		write(uint64(len(name)))
		b.WriteString(name)
		write(uint32(1))
		write(uint64(256))
		write(kind)
		write(uint64(0))
	}
	path := filepath.Join(t.TempDir(), "gyro.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectGGUF(path); err != nil {
		t.Fatalf("generic metadata inspection must remain available: %v", err)
	}
	if _, err := InspectBundledGGUF(path); err == nil || !strings.Contains(err.Error(), "TQ2_T") {
		t.Fatalf("expected clear custom-format error, got %v", err)
	}
	if meta, err := InspectTrellisGGUF(path); err != nil || !meta.Trellis {
		t.Fatalf("expected separate trellis format admission: %+v %v", meta, err)
	}
}

func TestTrellisTypeSizes(t *testing.T) {
	for i, size := range []uint64{34, 26, 30, 42, 50, 58} {
		if block, got := tensorTypeSize(uint32(144 + i)); block != 128 || got != size {
			t.Fatalf("unexpected trellis block layout for type %d: %d/%d", 144+i, block, got)
		}
	}
}

func TestTrellisPLERequiresDocumentedSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Qwen3.8-Flash-Next-Gyro-M-TQ2_0-noPLE.gguf")
	meta := GGUFInfo{Architecture: "qwen4exp"}
	if _, err := TrellisPLE(path, meta); err == nil {
		t.Fatal("accepted missing PLE")
	}
	sidecar := filepath.Join(dir, flashNextPLE)
	if err := os.WriteFile(sidecar, ggufTestFile("qwen4exp", 0, 0), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := TrellisPLE(path, meta); err != nil || got != sidecar {
		t.Fatalf("PLE path %q: %v", got, err)
	}
}
