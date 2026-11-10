// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only

// Package debuglog enables explicitly requested local diagnostics without
// changing network endpoints or writing HTTP authorization headers/bodies.
package debuglog

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

const maxLogBytes int64 = 8 << 20

type outputState struct{ writer io.Writer }

var nativeOutput atomic.Pointer[outputState]

// NativeOutput includes stderr from GGML subprocesses in the debug log.
// Without --debug it remains normal stderr; prompts and keys are never
// deliberately recorded by the NodexLM HTTP request tracing layer.
func NativeOutput() io.Writer {
	if state := nativeOutput.Load(); state != nil {
		return state.writer
	}
	return os.Stderr
}

type rotatingWriter struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	if err := os.Remove(w.path + ".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return err
	}
	next, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	w.file, w.size = next, 0
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, errors.New("debug log closed")
	}
	if w.size > 0 && w.size+int64(len(p)) > maxLogBytes {
		if err := w.rotate(); err != nil {
			return 0, fmt.Errorf("rotate debug log: %w", err)
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("debug log directory must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("debug log directory %s must have permissions 0700", path)
	}
	return nil
}

// Start attaches bounded, private Host/Guest log files, preserving stderr.
// One previous 8 MiB segment is kept; file access is restricted to the user.
func Start(dir, role string) (func(), error) {
	if role != "host" && role != "guest" {
		return nil, errors.New("invalid debug log role")
	}
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	logs := filepath.Join(dir, "logs")
	if err := privateDirectory(logs); err != nil {
		return nil, err
	}
	filename := filepath.Join(logs, role+".log")
	if info, err := os.Lstat(filename); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("debug log file must be a regular file")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("debug log file must have permissions 0600")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	rotator := &rotatingWriter{path: filename, file: file, size: info.Size()}
	out := io.MultiWriter(os.Stderr, rotator)
	previous := slog.Default()
	options := &slog.HandlerOptions{Level: slog.LevelDebug}
	if strings.EqualFold(os.Getenv("NODEXLM_LOG_FORMAT"), "json") {
		slog.SetDefault(slog.New(slog.NewJSONHandler(out, options)))
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(out, options)))
	}
	nativeOutput.Store(&outputState{writer: out})
	slog.Info("Diagnostic logging enabled", "component", role, "file", filename, "segment_limit_mib", 8, "caution", "Logs may contain model paths or runtime diagnostics; share only after review")
	return func() {
		slog.SetDefault(previous)
		nativeOutput.Store(nil)
		_ = rotator.Close()
	}, nil
}
