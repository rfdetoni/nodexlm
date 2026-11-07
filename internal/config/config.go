// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

type Agent struct {
	Coordinator            string              `json:"coordinator"`
	Node                   protocol.Credential `json:"node"`
	CertificateFingerprint string              `json:"certificate_fingerprint,omitempty"`
}

func DefaultDir() string {
	if v := os.Getenv("NODEXLM_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".nodexlm"
	}
	return filepath.Join(home, ".nodexlm")
}
func ReadAgent(dir string) (Agent, error) {
	var a Agent
	b, err := os.ReadFile(filepath.Join(dir, "agent.json"))
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(b, &a)
	return a, err
}
func SaveAgent(dir string, a Agent) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "agent.json"), body)
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-nodexlm-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func CreateCertificate(certPath, keyPath string, hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("provide certificate DNS names or IP addresses")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "NodexLM Coordinator"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if _, err := os.Stat(certPath); err == nil {
		return errors.New("certificate already exists; refusing overwrite")
	}
	if _, err := os.Stat(keyPath); err == nil {
		return errors.New("key already exists; refusing overwrite")
	}
	if err := atomicWrite(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})); err != nil {
		return err
	}
	if err := atomicWrite(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		// A partial key+cert pair would make the next Host startup fail.
		_ = os.Remove(keyPath)
		return err
	}
	return nil
}
