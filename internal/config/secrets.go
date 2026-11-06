// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Secrets struct {
	Admin string `json:"admin"`
	Join  string `json:"join"`
}

func RandomSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func ReadSecrets(dir string) (Secrets, error) {
	var secrets Secrets
	path := filepath.Join(dir, "secrets.json")
	info, err := os.Stat(path)
	if err != nil {
		return secrets, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return secrets, errors.New("secrets.json must not be readable by other users")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return secrets, err
	}
	err = json.Unmarshal(body, &secrets)
	if err != nil {
		return secrets, err
	}
	if len(secrets.Admin) < 32 || len(secrets.Join) < 32 || secrets.Admin == secrets.Join {
		return secrets, errors.New("invalid coordinator secrets")
	}
	return secrets, nil
}

func EnsureSecrets(dir string) (Secrets, error) {
	s, err := ReadSecrets(dir)
	if err == nil {
		return s, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Secrets{}, err
	}
	s.Admin, err = RandomSecret()
	if err != nil {
		return Secrets{}, err
	}
	s.Join, err = RandomSecret()
	if err != nil {
		return Secrets{}, err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return Secrets{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Secrets{}, err
	}
	// O_EXCL prevents overwriting a concurrently created credential store.
	f, err := os.OpenFile(filepath.Join(dir, "secrets.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return ReadSecrets(dir)
	}
	if err != nil {
		return Secrets{}, err
	}
	_, writeErr := f.Write(b)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return Secrets{}, writeErr
	}
	if syncErr != nil {
		return Secrets{}, syncErr
	}
	if closeErr != nil {
		return Secrets{}, closeErr
	}
	return s, nil
}

func AdminSecret(dir string) string {
	if v := strings.TrimSpace(os.Getenv("NODEXLM_ADMIN_TOKEN")); v != "" {
		return v
	}
	s, err := ReadSecrets(dir)
	if err != nil {
		return ""
	}
	return s.Admin
}
