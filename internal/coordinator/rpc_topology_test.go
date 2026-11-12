package coordinator

import (
	"testing"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestRPCDeviceTopologyAcceptsOnlySupportedNativeLayouts(t *testing.T) {
	for _, kinds := range [][]string{nil, {}, {"cpu"}, {"gpu"}, {"gpu", "cpu"}} {
		if !validRPCDeviceTopology(kinds) {
			t.Fatalf("valid native RPC topology rejected: %+v", kinds)
		}
	}
	for _, kinds := range [][]string{
		{"cpu", "gpu"}, {"gpu", "gpu"}, {"cpu", "cpu"},
		{"gpu", "cpu", "gpu"}, {"GPU"}, {"garbage"},
	} {
		if validRPCDeviceTopology(kinds) {
			t.Fatalf("untrusted topology accepted: %+v", kinds)
		}
	}
}

func TestGuestHeartbeatRejectsInvalidGPUCPUClaims(t *testing.T) {
	d := protocol.Device{
		Name: "test-guest", Platform: "linux", Architecture: "amd64",
		CPUCount: 8, RAMBytes: 16 << 30, AvailableRAMBytes: 12 << 30,
		Runtime: protocol.RuntimeCapability{
			Engine: "llama.cpp", Version: "b12255",
			RPCDevices: []string{"gpu", "cpu"},
		},
	}
	if !validDevice(d) {
		t.Fatal("valid GPU+CPU heartbeat rejected")
	}
	d.Runtime.RPCDevices = []string{"gpu", "gpu", "cpu"}
	if validDevice(d) {
		t.Fatal("unsupported multi-GPU heartbeat was admitted without a safe device mapping")
	}
	d.Runtime.RPCDevices = []string{"cpu", "gpu"}
	if validDevice(d) {
		t.Fatal("incorrect RPC device order was admitted")
	}
}
