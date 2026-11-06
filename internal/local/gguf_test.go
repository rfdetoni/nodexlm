// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"bytes"
	"encoding/binary"
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
	count := uint64(2) // arch and unknown tokenizer array
	if experts != 0 {
		count += 2
	}
	write(count)
	key("general.architecture", ggufString)
	write(uint64(len(arch)))
	b.WriteString(arch)
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
			if info.Architecture != test.arch || info.ExpertCount != test.experts || info.ActiveExperts != test.used || info.IsMoE != test.moe {
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
	args := m.args(8080, 2048, 4, "auto")
	if !reflect.DeepEqual(args[len(args)-3:], []string{"--cpu-moe", "--moe-cache-mib", "256"}) {
		t.Fatalf("wrong args: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "--no-webui") {
		t.Fatalf("missing default args: %v", args)
	}
}
