package main

import (
	"strings"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestCompatibleStockRPC(t *testing.T) {
	major, minor := llamaruntime.RPCProtocolVersion()
	pinned := protocol.RuntimeCapability{
		RPCSourceCommit:  llamaruntime.SourceCommit,
		RPCProtocolMajor: int(major),
		RPCProtocolMinor: int(minor),
		RPCProtocolPatch: int(llamaruntime.RPCProtocolPatch()),
	}
	if !compatibleStockRPC(pinned, true) || !compatibleStockRPC(pinned, false) {
		t.Fatal("rejected pinned native ABI")
	}
	if compatibleStockRPC(protocol.RuntimeCapability{}, true) || !compatibleStockRPC(protocol.RuntimeCapability{}, false) {
		t.Fatal("missing native identity must be rejected on release Hosts only")
	}
	for _, change := range []func(*protocol.RuntimeCapability){
		func(c *protocol.RuntimeCapability) { c.RPCSourceCommit = strings.Repeat("0", 40) },
		func(c *protocol.RuntimeCapability) { c.RPCProtocolMajor++ },
		func(c *protocol.RuntimeCapability) { c.RPCProtocolMinor++ },
		func(c *protocol.RuntimeCapability) { c.RPCProtocolPatch++ },
	} {
		mismatch := pinned
		change(&mismatch)
		if compatibleStockRPC(mismatch, true) || compatibleStockRPC(mismatch, false) {
			t.Fatalf("accepted incompatible ABI: %+v", mismatch)
		}
	}
}
