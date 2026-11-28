package main

import (
	"errors"
	"maps"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func verifyHostRuntimePolicy(credential protocol.Credential, signed *protocol.SignedRuntimePolicy) error {
	if signed == nil {
		// Local development Hosts and older manually enrolled credentials
		// do not advertise a release policy. Released paired Guests require it.
		if protocol.ValidBuildCommit(buildCommit) && credential.AuthorityPubKey != "" {
			return errors.New("Host did not supply a signed runtime policy")
		}
		return nil
	}
	if err := protocol.VerifyRuntimePolicy(*signed, credential, time.Now()); err != nil {
		return err
	}
	p := signed.Policy
	major, minor := llamaruntime.RPCProtocolVersion()
	if p.HostBuildCommit != buildCommit || p.Engine != "llama.cpp" ||
		p.SourceCommit != llamaruntime.SourceCommit || p.Release != llamaruntime.BundledRPCRelease() ||
		p.RPCMajor != major || p.RPCMinor != minor || p.RPCPatch != llamaruntime.RPCProtocolPatch() ||
		!maps.Equal(p.ArchiveSHA256, llamaruntime.PublishedRPCDigests()) {
		return errors.New("Host runtime policy differs from the reviewed local release catalog")
	}
	return nil
}
