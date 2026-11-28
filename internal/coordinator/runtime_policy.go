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
	policy, err := protocol.SignRuntimePolicy(s.authorityPriv, protocol.RuntimePolicy{
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
	})
	if err != nil {
		return nil
	}
	return &policy
}
