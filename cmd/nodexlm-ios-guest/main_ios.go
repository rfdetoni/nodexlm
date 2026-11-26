//go:build ios && cgo

package main

/*
#include <stdlib.h>
#include <stdint.h>
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/pairing"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

type credentials struct {
	Endpoint string `json:"endpoint"`
	Node     string `json:"node"`
	Token    string `json:"token"`
	Pin      string `json:"pin"`
}

var state struct {
	sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	status  string
	profile protocol.Device
}

func response(value any) *C.char {
	data, _ := json.Marshal(value)
	return C.CString(string(data))
}

func errorResponse(err error) *C.char {
	return response(map[string]string{"error": err.Error()})
}

//export NodexIOSFree
func NodexIOSFree(value *C.char) {
	C.free(unsafe.Pointer(value))
}

//export NodexIOSPair
func NodexIOSPair(endpoint, code, name *C.char) *C.char {
	ep := strings.TrimRight(C.GoString(endpoint), "/")
	u, err := url.Parse(ep)
	if err != nil || u.Scheme != "https" || !pairing.SameLAN(net.ParseIP(u.Hostname())) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errorResponse(errors.New("enter the Host HTTPS LAN IP and port"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	offer, der, err := pairing.FetchUntrustedOffer(ctx, ep)
	if err != nil {
		return errorResponse(errors.New("cannot obtain Host pairing offer"))
	}
	if err = pairing.VerifyOffer(C.GoString(code), offer, der); err != nil {
		return errorResponse(err)
	}
	sum := sha256.Sum256(der)
	pin := hex.EncodeToString(sum[:])
	client, err := netclient.New(ep, "", "")
	if err != nil {
		return errorResponse(err)
	}
	defer client.HTTP.CloseIdleConnections()
	if err = client.UsePinnedCertificate(pin); err != nil {
		return errorResponse(err)
	}
	profile := protocol.Device{Name: C.GoString(name), Platform: "ios", Architecture: "arm64", CPUCount: 1}
	var cred protocol.Credential
	_, err = client.Request(ctx, http.MethodPost, "/v1/pairing/enroll", map[string]any{"code": pairing.Normalize(C.GoString(code)), "device": profile}, &cred)
	if err != nil {
		return errorResponse(errors.New("Host rejected enrollment"))
	}
	return response(credentials{Endpoint: ep, Node: cred.NodeID, Token: cred.Token, Pin: pin})
}

//export NodexIOSMemory
func NodexIOSMemory(total, available C.uint64_t, cores, thermal C.int) {
	state.Lock()
	defer state.Unlock()
	t := uint64(total)
	a := min(uint64(available), t)
	temp := int(thermal)
	state.profile.RAMBytes = t
	state.profile.AvailableRAMBytes = a
	state.profile.CPUCount = int(cores)
	state.profile.ThermalStatus = &temp
	state.profile.GPUs = []protocol.GPUDevice{{Name: "Metal", Vendor: "apple", MemoryType: "shared", VRAMBytes: t, FreeVRAMBytes: a}}
}

//export NodexIOSStart
func NodexIOSStart(raw, socket, name *C.char) *C.char {
	var cred credentials
	if err := json.Unmarshal([]byte(C.GoString(raw)), &cred); err != nil {
		return errorResponse(errors.New("invalid enrollment"))
	}
	client, err := netclient.New(cred.Endpoint, cred.Token, cred.Node)
	if err != nil {
		return errorResponse(err)
	}
	if err = client.UsePinnedCertificate(cred.Pin); err != nil {
		return errorResponse(err)
	}
	address := "unix:" + C.GoString(socket)
	if err = rpcmesh.ValidateRPCAddress(address); err != nil {
		return errorResponse(err)
	}
	state.Lock()
	defer state.Unlock()
	if state.cancel != nil {
		return errorResponse(errors.New("Guest already running"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	state.cancel, state.done, state.status = cancel, done, "Starting"
	state.profile.Name = C.GoString(name)
	state.profile.Platform = "ios"
	state.profile.Architecture = "arm64"
	state.profile.Runtime = protocol.RuntimeCapability{Engine: "llama.cpp", Version: llamaruntime.Version, Installed: true, Benchmark: true, RPCDevices: []string{"gpu"}}
	go run(ctx, cancel, done, client, address)
	return response(map[string]string{"status": "starting"})
}

func run(ctx context.Context, cancel context.CancelFunc, done chan struct{}, client *netclient.Client, address string) {
	defer func() {
		cancel()
		client.HTTP.CloseIdleConnections()
		state.Lock()
		state.cancel = nil
		close(done)
		state.Unlock()
	}()
	setStatus := func(value string) {
		state.Lock()
		state.status = value
		state.Unlock()
	}
	readyCtx, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	for {
		conn, err := (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext(readyCtx, "unix", strings.TrimPrefix(address, "unix:"))
		if err == nil {
			conn.Close()
			break
		}
		select {
		case <-readyCtx.Done():
			setStatus("Native Metal RPC did not become ready")
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	beat := func() error {
		state.Lock()
		profile := state.profile
		state.Unlock()
		profile.Runtime.ReconnectFailures = client.ReconnectFailures.Load()
		beatCtx, stop := context.WithTimeout(ctx, 8*time.Second)
		defer stop()
		status, err := client.Request(beatCtx, http.MethodPost, "/v1/nodes/heartbeat", profile, nil)
		if status == 401 || status == 403 {
			return rpcmesh.ErrGuestCredentialsRejected
		}
		return err
	}
	if err := beat(); err != nil {
		setStatus("Host rejected or unavailable")
		return
	}
	setStatus("Connected (foreground)")
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if errors.Is(beat(), rpcmesh.ErrGuestCredentialsRejected) {
					cancel()
					return
				}
			}
		}
	}()
	err := rpcmesh.RunAttachedGuest(ctx, client, address, 2)
	cancel()
	<-heartbeatDone
	if err != nil {
		setStatus("Guest stopped: re-pair with Host")
	} else {
		setStatus("Stopped")
	}
}

//export NodexIOSStop
func NodexIOSStop() {
	state.Lock()
	cancel, done := state.cancel, state.done
	state.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

//export NodexIOSStatus
func NodexIOSStatus() *C.char {
	state.Lock()
	defer state.Unlock()
	return C.CString(state.status)
}

func main() {}
