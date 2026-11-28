package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"
)

func TestRuntimePolicySignatureAndPairing(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10000, 0)
	credential := Credential{NodeID: "guest", ClusterID: "cluster", Epoch: 2, AuthorityPubKey: hex.EncodeToString(pub)}
	p := RuntimePolicy{NodeID: "guest", ClusterID: "cluster", Epoch: 2, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), ArchiveSHA256: map[string]string{"linux/amd64/cpu": "digest"}}
	signed, err := SignRuntimePolicy(priv, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRuntimePolicy(signed, credential, now); err != nil {
		t.Fatal(err)
	}
	wrong := credential
	wrong.NodeID = "other"
	if VerifyRuntimePolicy(signed, wrong, now) == nil {
		t.Fatal("policy accepted for another Guest")
	}
	wrong = credential
	wrong.Epoch++
	if VerifyRuntimePolicy(signed, wrong, now) == nil {
		t.Fatal("policy accepted in another epoch")
	}
	if VerifyRuntimePolicy(signed, credential, now.Add(2*time.Minute)) == nil {
		t.Fatal("expired policy accepted")
	}
	signed.Policy.ArchiveSHA256["linux/amd64/cpu"] = "tampered"
	if VerifyRuntimePolicy(signed, credential, now) == nil {
		t.Fatal("modified archive digest accepted")
	}
}
