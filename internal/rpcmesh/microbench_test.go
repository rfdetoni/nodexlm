// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestMicrobenchNativeDevices(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			conn, err := ln.Accept()
			if err == nil {
				err = serveSizedProbeRPC(conn, corrupt, true)
			}
			done <- err
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		got, err := Microbench(ctx, ln.Addr().String())
		cancel()
		_ = ln.Close()
		<-done
		if corrupt {
			if err == nil {
				t.Fatal("corrupt data accepted")
			}
		} else if err != nil || len(got.Devices) != 4 || got.Source != "auto" {
			t.Fatalf("invalid measurement: %+v %v", got, err)
		}
	}
}
