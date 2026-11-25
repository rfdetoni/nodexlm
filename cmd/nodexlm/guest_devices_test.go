package main

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestParseNativeRPCDevicesFromPinnedWorker(t *testing.T) {
	log := `ggml_vulkan: Found 1 Vulkan devices:
error: unknown device: __nodexlm_probe_devices__
available devices:
  Vulkan0: NVIDIA GeForce RTX 2060 (6144 MiB, 5628 MiB free)
  CPU: Intel(R) Core(TM) (28672 MiB, 20480 MiB free)
No devices found
`
	want := []string{"Vulkan0", "CPU"}
	if got := parseRPCDeviceNames(log); !reflect.DeepEqual(got, want) {
		t.Fatalf("native discovery: %q, expected %q", got, want)
	}
	if got := parseRPCDeviceNames("no devices found"); len(got) != 0 {
		t.Fatalf("invalid probe result was treated as a GPU: %q", got)
	}
}

func TestAutoGuestPrefersVulkanWithDedicatedGPUAndExposesRAM(t *testing.T) {
	profile := protocol.Device{Platform: "linux", GPUs: []protocol.GPUDevice{{
		Name: "NVIDIA GeForce RTX 2060", MemoryType: "dedicated",
		VRAMBytes: 6 << 30, FreeVRAMBytes: 5 << 30,
	}}}
	if (runtime.GOOS == "linux" || runtime.GOOS == "windows") && runtime.GOARCH == "amd64" &&
		autoGuestBackend(profile) != "vulkan" {
		t.Fatalf("dedicated GPU was not selected: %q", autoGuestBackend(profile))
	}
	if got := autoGuestBackendFor("windows", "amd64", profile); got != "vulkan" {
		t.Fatalf("dedicated Windows AMD64 GPU did not select vulkan: %q", got)
	}
	if got := autoGuestBackendFor("linux", "amd64", profile); got != "vulkan" {
		t.Fatalf("dedicated Linux AMD64 GPU did not select vulkan: %q", got)
	}
	if got := autoGuestBackendFor("darwin", "arm64", profile); got != "metal" {
		t.Fatalf("macOS ARM64 did not select metal: %q", got)
	}
	devices := []string{"Vulkan0", "CPU"}
	if got := autoRPCDeviceChoice(devices, profile); got != "Vulkan0,CPU" {
		t.Fatalf("dedicated GPU and RAM must both be available: %q", got)
	}
	if got := nativeDeviceKinds("Vulkan0,CPU", profile); !reflect.DeepEqual(got, []string{"gpu", "cpu"}) {
		t.Fatalf("reported incorrect RPC device ordering: %+v", got)
	}
	if got := nativeDeviceKinds("CPU", profile); !reflect.DeepEqual(got, []string{"cpu"}) {
		t.Fatalf("CPU-only was not honored: %+v", got)
	}
}

func TestAutoGuestNeverCountsSharedVRAMAsExtraRAM(t *testing.T) {
	shared := protocol.Device{Platform: "android", GPUs: []protocol.GPUDevice{{
		MemoryType: "shared", VRAMBytes: 6 << 30, FreeVRAMBytes: 5 << 30,
	}}}
	if guestDedicatedGPU(shared) {
		t.Fatal("shared memory was treated as dedicated VRAM")
	}
	if got := autoRPCDeviceChoice([]string{"Vulkan0", "CPU"}, shared); got != "CPU" {
		t.Fatalf("shared GPU must not force hybrid auto mode: %q", got)
	}
	if got := nativeDeviceKinds("Vulkan0,CPU", shared); len(got) != 0 {
		t.Fatalf("shared GPU must not advertise additive VRAM: %q", got)
	}
	if got := nativeDeviceKinds("CUDA0,CPU", shared); len(got) != 0 {
		t.Fatalf("CUDA shared VRAM incorrectly treated as discrete: %q", got)
	}
}

func TestExplicitCUDADeviceChoice(t *testing.T) {
	profile := protocol.Device{GPUs: []protocol.GPUDevice{{MemoryType: "dedicated", FreeVRAMBytes: 6 << 30}}}
	if got, err := explicitCUDADeviceChoice([]string{"CUDA0", "CPU"}, profile); err != nil || got != "CUDA0,CPU" {
		t.Fatalf("CUDA+CPU: %q %v", got, err)
	}
	for _, names := range [][]string{{"CPU"}, {"CUDA0", "CUDA1", "CPU"}} {
		if _, err := explicitCUDADeviceChoice(names, profile); err == nil {
			t.Fatalf("accepted unavailable or ambiguous CUDA: %v", names)
		}
	}
}
