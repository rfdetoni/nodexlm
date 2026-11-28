package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// RuntimePolicy authorizes one reviewed native runtime for one enrolled
// Guest. It does not provide executable paths, commands or download URLs.
type RuntimePolicy struct {
	ClusterID       string            `json:"cluster_id"`
	Epoch           uint64            `json:"epoch"`
	NodeID          string            `json:"node_id"`
	HostBuildCommit string            `json:"host_build_commit"`
	Engine          string            `json:"engine"`
	ExperimentalGPU bool              `json:"experimental_gpu,omitempty"`
	SourceCommit    string            `json:"source_commit"`
	Release         string            `json:"release"`
	RPCMajor        byte              `json:"rpc_major"`
	RPCMinor        byte              `json:"rpc_minor"`
	RPCPatch        byte              `json:"rpc_patch"`
	ArchiveSHA256   map[string]string `json:"archive_sha256"`
	IssuedAt        int64             `json:"issued_at"`
	ExpiresAt       int64             `json:"expires_at"`
}

type SignedRuntimePolicy struct {
	Policy    RuntimePolicy `json:"policy"`
	Payload   []byte        `json:"payload"`
	Signature string        `json:"signature"`
}

func runtimePolicyMessage(p RuntimePolicy) ([]byte, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return append([]byte("nodexlm/runtime-policy/v1\x00"), raw...), nil
}

func SignRuntimePolicy(key ed25519.PrivateKey, policy RuntimePolicy) (SignedRuntimePolicy, error) {
	if len(key) != ed25519.PrivateKeySize {
		return SignedRuntimePolicy{}, errors.New("invalid runtime policy signing key")
	}
	msg, err := runtimePolicyMessage(policy)
	if err != nil {
		return SignedRuntimePolicy{}, err
	}
	return SignedRuntimePolicy{Policy: policy, Payload: msg, Signature: hex.EncodeToString(ed25519.Sign(key, msg))}, nil
}

// VerifyRuntimePolicy binds the signature to the existing pairing, not to
// an authority key supplied by the untrusted policy response itself.
func VerifyRuntimePolicy(s SignedRuntimePolicy, credential Credential, now time.Time) error {
	key, err := hex.DecodeString(credential.AuthorityPubKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("invalid paired runtime authority")
	}
	p := s.Policy
	if credential.ClusterID == "" || credential.NodeID == "" || p.ClusterID != credential.ClusterID ||
		p.Epoch == 0 || p.Epoch != credential.Epoch || p.NodeID != credential.NodeID {
		return errors.New("runtime policy belongs to a different pairing or epoch")
	}
	if p.IssuedAt <= 0 || p.IssuedAt > now.Unix()+30 || p.ExpiresAt <= now.Unix() ||
		p.ExpiresAt <= p.IssuedAt || p.ExpiresAt-p.IssuedAt > 300 {
		return errors.New("runtime policy expired or invalid")
	}
	signature, err := hex.DecodeString(s.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("invalid runtime policy signature")
	}
	msg, err := runtimePolicyMessage(p)
	if err != nil || !bytes.Equal(s.Payload, msg) || !ed25519.Verify(ed25519.PublicKey(key), msg, signature) {
		return errors.New("runtime policy signature verification failed")
	}
	return nil
}
