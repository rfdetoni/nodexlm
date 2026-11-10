// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/rfdetoni/nodexlm/internal/catalog"
)

// ModelFolder returns the Host filesystem path, not a browser-accessible URL.
func (m *Manager) ModelFolder() string {
	dir, err := filepath.Abs(m.modelDir)
	if err != nil {
		return m.modelDir
	}
	return dir
}

// OpenModelFolder runs the native file manager on the Host, never on a Guest.
// The directory is configured by the Host, not supplied by the HTTP client.
func (m *Manager) OpenModelFolder() error {
	dir := m.ModelFolder()
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("model destination is not a directory")
	}
	program := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		program = "open"
	case "windows":
		program = "explorer.exe"
	}
	cmd := exec.Command(program, dir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open model folder on Host: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// DeleteModel only removes regular GGUF files from the managed directory.
// Active loads, native processes and incomplete downloads cannot be deleted.
func (m *Manager) DeleteModel(name string) error {
	if _, err := modelPath(m.modelDir, name); err != nil {
		return err
	}
	names, err := catalog.ShardNames(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.loading || m.unloading {
		m.mu.Unlock()
		return errors.New("finish or cancel model loading before deleting files")
	}
	m.mu.Unlock()

	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loading || m.unloading || m.current == name && m.engine != nil || m.residents[name] != nil {
		return errors.New("cannot delete a loaded or loading model; unload it first")
	}
	if m.download != nil && m.download.Status == "downloading" {
		for _, part := range names {
			if m.download.Name == part {
				return errors.New("cannot delete a model while it is downloading")
			}
		}
	}
	for _, part := range names {
		path, err := modelPath(m.modelDir, part)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("only regular GGUF files can be deleted")
		}
	}
	if m.settings.Model == name {
		updated := m.settings
		updated.Model = ""
		body, err := json.MarshalIndent(updated, "", "  ")
		if err != nil {
			return err
		}
		if err := writePrivate(filepath.Join(m.dir, "runtime.json"), body); err != nil {
			return err
		}
		m.settings = updated
	}
	for _, part := range names {
		if err := os.Remove(filepath.Join(m.modelDir, part)); err != nil {
			return fmt.Errorf("delete GGUF shard %s: %w", part, err)
		}
	}
	return nil
}
