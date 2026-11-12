package hardware

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestDRMGPU(t *testing.T) {
	root := t.TempDir()
	create := func(card, vendor, total, used string) {
		t.Helper()
		dir := filepath.Join(root, card, "device")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{"vendor": vendor, "mem_info_vram_total": total, "mem_info_vram_used": used, "gpu_busy_percent": "42"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	create("card0", "0x1002", "8589934592", "2147483648")
	create("card1", "0x8086", "0", "0")
	create("renderD128", "0x1002", "100", "0")
	got := drmDevices(root)
	if len(got) != 2 {
		t.Fatalf("expected two GPUs, got %+v", got)
	}
	if got[0].Vendor != "amd" || got[0].VRAMBytes != 8<<30 || got[0].FreeVRAMBytes != 6<<30 {
		t.Fatalf("bad AMD VRAM %+v", got[0])
	}
	if got[1].Vendor != "intel" || got[1].FreeVRAMBytes != 0 {
		t.Fatalf("bad Intel GPU %+v", got[1])
	}
}
func TestDRMUnknownUsedDoesNotInventFreeVRAM(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "card2", "device")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor"), []byte("0x1002"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mem_info_vram_total"), []byte("1024"), 0600); err != nil {
		t.Fatal(err)
	}
	got := drmDevices(root)
	if len(got) != 1 || got[0].VRAMBytes != 1024 || got[0].FreeVRAMBytes != 0 {
		t.Fatalf("unexpected GPU %+v", got)
	}
}

func TestMeasuredGPUUsage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "card0", "device")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"vendor": "0x1002", "mem_info_vram_total": "1000",
		"mem_info_vram_used": "250", "gpu_busy_percent": "42",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	found := drmDevices(root)
	if len(found) != 1 || found[0].UsagePercent == nil || *found[0].UsagePercent != 42 ||
		found[0].VRAMUsedBytes == nil || *found[0].VRAMUsedBytes != 250 {
		t.Fatalf("GPU metrics: %+v", found)
	}
	if err := os.Remove(filepath.Join(dir, "gpu_busy_percent")); err != nil {
		t.Fatal(err)
	}
	found = drmDevices(root)
	if len(found) != 1 || found[0].UsagePercent != nil {
		t.Fatal("Unavailable GPU usage must be null")
	}
}

func TestNVIDIADedicatedVRAMBudgetAndMalformedCounters(t *testing.T) {
	metrics := "NVIDIA RTX 4070, 12288, 6144, 27\nNVIDIA bad, 4096, 5000, N/A\n"
	devices := parseNVIDIASMI(metrics)
	if len(devices) != 1 {
		t.Fatalf("invalid NVIDIA metrics must not reach the Host: %+v", devices)
	}
	gpu := devices[0]
	if gpu.MemoryType != "dedicated" || gpu.VRAMBytes != 12288*clusterplan.MiB ||
		gpu.FreeVRAMBytes != 6144*clusterplan.MiB || gpu.VRAMUsedBytes == nil ||
		*gpu.VRAMUsedBytes != 6144*clusterplan.MiB ||
		gpu.UsagePercent == nil || *gpu.UsagePercent != 27 {
		t.Fatalf("NVIDIA dedicated VRAM was reported incorrectly: %+v", gpu)
	}
	host := protocol.Device{RAMBytes: 8 * clusterplan.GiB, AvailableRAMBytes: clusterplan.GiB + 512*clusterplan.MiB}
	withoutGPU := clusterplan.Budget(host)
	host.GPUs = devices
	if withGPU := clusterplan.Budget(host); withGPU <= withoutGPU {
		t.Fatalf("known dedicated VRAM was treated as shared: with=%d without=%d", withGPU, withoutGPU)
	}
}
