// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

const (
	SupportedRPCMajor = 10
	SupportedRPCMinor = 0
)

var (
	// ErrRPCWireIncompatible indicates a major wire mismatch or unimplemented required extension.
	ErrRPCWireIncompatible = errors.New("RPC wire protocol incompatible")
	// ErrDirectTensorCopyUnsupported reports that pinned ggml-rpc 10.0 routes cross-node copies via Host.
	ErrDirectTensorCopyUnsupported = errors.New("direct Guest-to-Guest tensor copy unavailable in ggml-rpc 10.0")
)

type PeerRPCCapabilities struct {
	ProtocolMajor    int  `json:"rpc_protocol_major"`
	ProtocolMinor    int  `json:"rpc_protocol_minor"`
	DirectTensorCopy bool `json:"rpc_direct_tensor_copy"`
}

func LocalPeerRPCCapabilities() PeerRPCCapabilities {
	return PeerRPCCapabilities{
		ProtocolMajor:    SupportedRPCMajor,
		ProtocolMinor:    SupportedRPCMinor,
		DirectTensorCopy: false,
	}
}

// CheckWireCompatibility rejects mismatched major versions and unsupported required capabilities.
func CheckWireCompatibility(major, minor int, requiredCaps []string) error {
	if major != SupportedRPCMajor || minor < 0 {
		return fmt.Errorf("%w: got %d.%d, require %d.x", ErrRPCWireIncompatible, major, minor, SupportedRPCMajor)
	}
	for _, cap := range requiredCaps {
		switch cap {
		case "directional-v1", "deflate-v1":
		case "direct-tensor-copy-v1":
			return fmt.Errorf("%w: %s", ErrDirectTensorCopyUnsupported, cap)
		default:
			return fmt.Errorf("%w: unknown required capability %q", ErrRPCWireIncompatible, cap)
		}
	}
	return nil
}

// ValidatePeerRPCSession validates a Coordinator-signed "rpc"-scoped grant before
// allowing any direct peer RPC tunnel operation, and fails closed on 10.0 when
// direct tensor copy is not supported by the native runtime.
func ValidatePeerRPCSession(
	grant protocol.PeerGrant,
	authorityPub ed25519.PublicKey,
	clusterID string,
	minEpoch uint64,
	selfNodeID string,
	caps PeerRPCCapabilities,
	now time.Time,
) error {
	if err := protocol.VerifyPeerGrant(grant, authorityPub, clusterID, minEpoch, selfNodeID, "rpc", now); err != nil {
		return err
	}
	if !caps.DirectTensorCopy || caps.ProtocolMajor != SupportedRPCMajor || caps.ProtocolMinor < 1 {
		return ErrDirectTensorCopyUnsupported
	}
	return nil
}
