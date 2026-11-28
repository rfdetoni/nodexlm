package coordinator

import (
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// Caller holds s.mu. Development builds do not issue release policies.
func (s *Server) runtimePolicyLocked(id string) *protocol.SignedRuntimePolicy {
	if s.buildCommit == "" || s.fenced {
		return nil
	}
	now := time.Now()
	major, minor := llamaruntime.RPCProtocolVersion()
	p := protocol.RuntimePolicy{
		ClusterID:       s.clusterID,
		Epoch:           s.epoch,
		NodeID:          id,
		HostBuildCommit: s.buildCommit,
		Engine:          "llama.cpp",
		SourceCommit:    llamaruntime.SourceCommit,
		Release:         llamaruntime.BundledRPCRelease(),
		RPCMajor:        major,
		RPCMinor:        minor,
		RPCPatch:        llamaruntime.RPCProtocolPatch(),
		ArchiveSHA256:   llamaruntime.PublishedRPCDigests(),
		IssuedAt:        now.Unix(),
		ExpiresAt:       now.Add(2 * time.Minute).Unix(),
	}
	if n := s.nodes[id]; s.trellisRPC && n != nil && trellisDesktop(n.device) {
		p.Engine = "trellis"
		p.SourceCommit = llamaruntime.TrellisSourceCommit
		p.Release = llamaruntime.TrellisRPCRelease
		p.RPCMajor, p.RPCMinor, p.RPCPatch = 10, 0, 3
		p.ArchiveSHA256 = nil // pinned source build, not an unverified archive
	}
	policy, err := protocol.SignRuntimePolicy(s.authorityPriv, p)
	if err != nil {
		return nil
	}
	return &policy
}

func trellisDesktop(d protocol.Device) bool {
	return d.Platform == "linux" || d.Platform == "windows" || d.Platform == "darwin"
}

func trellisRuntimeMatches(cap protocol.RuntimeCapability) bool {
	return cap.RPCSourceCommit == llamaruntime.TrellisSourceCommit &&
		cap.RPCProtocolMajor == 10 && cap.RPCProtocolMinor == 0 && cap.RPCProtocolPatch == 3
}

// SetTrellisRPC is called only at a primary model transition, after the
// previous process has stopped. Android/iOS keep their stock native runtime.
func (s *Server) SetTrellisRPC(enabled bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	enabled = enabled && s.buildCommit != "" && !s.fenced
	changed := s.trellisRPC != enabled
	s.trellisRPC = enabled
	count := 0
	for id, n := range s.nodes {
		if !trellisDesktop(n.device) {
			continue
		}
		cap := n.device.Runtime
		if cap.RPCSourceCommit != llamaruntime.SourceCommit && cap.RPCSourceCommit != llamaruntime.TrellisSourceCommit {
			continue
		}
		if changed {
			s.rpcHub.Remove(id)
		}
		if (enabled || cap.RPCSourceCommit == llamaruntime.TrellisSourceCommit) && n.device.BuildCommit == s.buildCommit && n.device.Status == "online" &&
			time.Since(n.device.LastSeen) < 65*time.Second && protocol.ComputeAdmissionReason(n.device) == "" {
			count++
		}
	}
	return count
}
