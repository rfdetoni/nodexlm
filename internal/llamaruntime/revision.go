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
	Version    string            `json:"version"`
	Commit     string            `json:"commit"`
	RPCRelease string            `json:"rpc_release"`
	Published  map[string]string `json:"published"`
}

func readRevision() runtimeRevision {
	var r runtimeRevision
	if err := json.Unmarshal(revisionJSON, &r); err != nil {
		panic("invalid embedded llama.cpp revision: " + err.Error())
	}
	return r
}
