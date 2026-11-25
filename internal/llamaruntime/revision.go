package llamaruntime

import (
	_ "embed"
	"encoding/json"
)

//go:embed revision.json
var revisionJSON []byte
var revision = readRevision()
var Version = revision.Version
var SourceCommit = revision.Commit

type runtimeRevision struct {
	UnixRPC            bool              `json:"unix_rpc"`
	ParallelRPCUploads bool              `json:"parallel_rpc_uploads"`
	Version            string            `json:"version"`
	Commit             string            `json:"commit"`
	RPCRelease         string            `json:"rpc_release"`
	Published          map[string]string `json:"published"`
	RPCProtocolMajor   byte              `json:"rpc_protocol_major"`
	RPCProtocolMinor   byte              `json:"rpc_protocol_minor"`
}

// RPCProtocolVersion is the exact native wire version of the reviewed source.
func RPCProtocolVersion() (byte, byte) {
	return revision.RPCProtocolMajor, revision.RPCProtocolMinor
}

func BundledParallelRPCUploadsSupported() bool {
	return revision.ParallelRPCUploads
}

func BundledRPCRelease() string {
	return revision.RPCRelease
}

func readRevision() runtimeRevision {
	var r runtimeRevision
	if err := json.Unmarshal(revisionJSON, &r); err != nil {
		panic("invalid embedded llama.cpp revision: " + err.Error())
	}
	if r.RPCProtocolMajor == 0 {
		panic("missing native RPC protocol version in embedded revision")
	}
	return r
}
