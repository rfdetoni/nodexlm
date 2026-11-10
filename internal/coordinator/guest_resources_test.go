package coordinator

import (
	"encoding/json"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"net/http"
	"testing"
)

func TestLinuxGuestLeaseReportsRAMAndVRAM(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.CloseRPC)
	h := s.Handler()
	initial := protocol.Device{Name: "linux-guest", Platform: "linux", CPUCount: 8, Models: []string{"example.gguf"}}
	joined := call(t, h, http.MethodPost, "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{Device: initial})
	if joined.Code != 201 {
		t.Fatal(joined.Body.String())
	}
	var cred protocol.Credential
	if err := json.Unmarshal(joined.Body.Bytes(), &cred); err != nil {
		t.Fatal(err)
	}
	profile := protocol.Device{Models: []string{"example.gguf"}, RAMBytes: 16 << 30, AvailableRAMBytes: 12 << 30,
		GPUs:    []protocol.GPUDevice{{Name: "AMD card0", Vendor: "amd", VRAMBytes: 8 << 30, FreeVRAMBytes: 6 << 30}},
		Runtime: protocol.RuntimeCapability{Engine: "llama.cpp", Installed: true}}
	lease := call(t, h, http.MethodPost, "/v1/nodes/lease", cred.Token, cred.NodeID, profile)
	if lease.Code != 204 {
		t.Fatalf("lease status %d: %s", lease.Code, lease.Body.String())
	}
	list := call(t, h, http.MethodGet, "/v1/nodes", adminSecret, "", nil)
	var devices []protocol.Device
	if err := json.Unmarshal(list.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].RAMBytes != 16<<30 || devices[0].AvailableRAMBytes != 12<<30 ||
		len(devices[0].GPUs) != 1 || devices[0].GPUs[0].FreeVRAMBytes != 6<<30 || !devices[0].Runtime.Installed {
		t.Fatalf("Linux Guest resources not persisted: %+v", devices)
	}
}

func TestHeartbeatRejectsInvalidResourceUtilization(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil { t.Fatal(err) }
	h := s.Handler()
	newNode := protocol.Device{Name:"guest", Platform:"linux", CPUCount:4, RAMBytes: 1024}
	resp := call(t,h,"POST","/v1/nodes/enroll",joinSecret,"",protocol.Enrollment{Device:newNode})
	if resp.Code != 201 { t.Fatalf("enroll %d: %s",resp.Code,resp.Body.String()) }
	var cred protocol.Credential
	if err:=json.Unmarshal(resp.Body.Bytes(), &cred);err!=nil { t.Fatal(err) }
	cpu := 101.0
	newNode.CPUUsagePercent = &cpu
	bad := call(t,h,"POST","/v1/nodes/heartbeat",cred.Token,cred.NodeID,newNode)
	if bad.Code != 400 { t.Fatalf("invalid CPU accepted %d",bad.Code) }
	cpu = 0
	newNode.RAMUsedBytes = new(uint64)
	*newNode.RAMUsedBytes = 2000
	bad = call(t,h,"POST","/v1/nodes/heartbeat",cred.Token,cred.NodeID,newNode)
	if bad.Code != 400 { t.Fatalf("invalid RAM accepted %d",bad.Code) }
	*newNode.RAMUsedBytes = 512
	gpuValue := -1.0
	newNode.GPUs = []protocol.GPUDevice{{Name:"gpu", Vendor:"amd", VRAMBytes:1024, UsagePercent:&gpuValue}}
	bad = call(t,h,"POST","/v1/nodes/heartbeat",cred.Token,cred.NodeID,newNode)
	if bad.Code != 400 { t.Fatalf("invalid GPU accepted %d",bad.Code) }
	gpuValue = 75
	vram := uint64(256)
	newNode.GPUs[0].VRAMUsedBytes = &vram
	ok := call(t,h,"POST","/v1/nodes/heartbeat",cred.Token,cred.NodeID,newNode)
	if ok.Code != 200 { t.Fatalf("valid utilization rejected %d: %s",ok.Code,ok.Body.String()) }
	devices:=s.DevicesSnapshot()
	if len(devices)!=1 || devices[0].CPUUsagePercent==nil || *devices[0].CPUUsagePercent!=0 ||
		devices[0].RAMUsedBytes==nil || *devices[0].RAMUsedBytes!=512 ||
		len(devices[0].GPUs)!=1 || devices[0].GPUs[0].UsagePercent==nil || *devices[0].GPUs[0].UsagePercent!=75 {
		t.Fatalf("measured telemetry lost in snapshot: %+v",devices)
	}
}
