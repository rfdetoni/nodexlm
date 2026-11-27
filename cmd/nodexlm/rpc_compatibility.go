package main

import (
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// The stock RPC bundle and a fork may share a major/minor protocol without
// sharing a tensor ABI. An official Host requires the full pinned identity.
func compatibleStockRPC(cap protocol.RuntimeCapability, strict bool) bool {
	major, minor := llamaruntime.RPCProtocolVersion()
	patch := int(llamaruntime.RPCProtocolPatch())
	if strict {
		return cap.RPCSourceCommit == llamaruntime.SourceCommit &&
			cap.RPCProtocolMajor == int(major) &&
			cap.RPCProtocolMinor == int(minor) &&
			cap.RPCProtocolPatch == patch
	}
	return (cap.RPCSourceCommit == "" || cap.RPCSourceCommit == llamaruntime.SourceCommit) &&
		(cap.RPCProtocolMajor == 0 || cap.RPCProtocolMajor == int(major)) &&
		(cap.RPCProtocolMinor == 0 || cap.RPCProtocolMinor == int(minor)) &&
		(cap.RPCProtocolPatch == 0 || cap.RPCProtocolPatch == patch)
}
