package rpcmesh

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func startProbeServer(t *testing.T, behavior string) (string, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil { done <- err; return }
		defer conn.Close()
		var hello [33]byte
		if _, err = io.ReadFull(conn, hello[:]); err != nil { done <- err; return }
		if hello[0] != 14 || binary.LittleEndian.Uint64(hello[1:9]) != 24 {
			done <- io.ErrUnexpectedEOF; return
		}
		if behavior == "timeout" {
			<-t.Context().Done()
			done <- nil
			return
		}
		var response [36]byte
		binary.LittleEndian.PutUint64(response[:8], 28)
		response[8] = 9
		if behavior == "version" { response[8] = 8 }
		if err = writeRPCProbe(conn, response[:]); err != nil { done <- err; return }
		if behavior == "version" { done <- nil; return }
		var count [9]byte
		if _, err = io.ReadFull(conn, count[:]); err != nil { done <- err; return }
		if count[0] != 15 || binary.LittleEndian.Uint64(count[1:]) != 0 {
			done <- io.ErrUnexpectedEOF; return
		}
		var answer [12]byte
		binary.LittleEndian.PutUint64(answer[:8], 4)
		binary.LittleEndian.PutUint32(answer[8:], 2)
		done <- writeRPCProbe(conn, answer[:])
	}()
	return ln.Addr().String(), done
}

func TestProbeHelloAndDeviceCount(t *testing.T) {
	endpoint, done := startProbeServer(t,"ok")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	count, err := ProbeHello(ctx,endpoint)
	if err != nil || count != 2 { t.Fatalf("count=%d err=%v",count,err) }
	if err:= <-done; err != nil {t.Fatal(err)}
}
func TestProbeHelloRejectsIncompatibleVersion(t *testing.T) {
	endpoint, done := startProbeServer(t,"version")
	_, err := ProbeHello(context.Background(),endpoint)
	if err == nil || !strings.Contains(err.Error(),"incompatible") { t.Fatalf("expected version failure: %v",err) }
	<-done
}
func TestProbeHelloDoesNotWaitIndefinitely(t *testing.T) {
	endpoint, _ := startProbeServer(t,"timeout")
	ctx,cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	started := time.Now()
	_,err:=ProbeHello(ctx,endpoint)
	if err==nil || time.Since(started)>time.Second {t.Fatalf("must fail fast, got %v after %s",err,time.Since(started))}
}
func TestProbeHelloNeverDialsExternalIP(t *testing.T) {
	if _,err:=ProbeHello(context.Background(),"192.168.1.10:50052");err==nil {
		t.Fatal("probe must require loopback relay")
	}
}
