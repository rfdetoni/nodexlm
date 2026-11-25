package clusterplan

import (
	"testing"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestMultiGPUUsesMinimumIndependentBudget(t *testing.T) {
	d := protocol.Device{Platform: "linux", RAMBytes: 16 * GiB, AvailableRAMBytes: 12 * GiB, Runtime: protocol.RuntimeCapability{RPCDevices: []string{"gpu", "gpu", "cpu"}}, GPUs: []protocol.GPUDevice{
		{Vendor: "nvidia", MemoryType: "dedicated", VRAMBytes: 6 * GiB, FreeVRAMBytes: 5 * GiB},
		{Vendor: "nvidia", MemoryType: "dedicated", VRAMBytes: 12 * GiB, FreeVRAMBytes: 9 * GiB},
	}}
	want := 2 * ((5 * GiB - 256 * MiB) * 90 / 100)
	if got := RPCMemory(d); got.GPU != want || got.RAM == 0 {
		t.Fatalf("unsafe device budgets: %+v", got)
	}
	d.GPUs[1].MemoryType = "shared"
	if Budget(d) != 0 {
		t.Fatal("mixed/shared telemetry must fail closed")
	}
}
