package worker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/coordinator"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestWorkerStreamsAndCancelsThroughCoordinator(t *testing.T) {
	const admin = "admin-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const join = "join-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	s, err := coordinator.New(admin, join)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseRPC()
	host := httptest.NewTLSServer(s.Handler())
	defer host.Close()
	pin := sha256.Sum256(host.Certificate().Raw)
	client, _ := netclient.New(host.URL, join, "")
	_ = client.UsePinnedCertificate(hex.EncodeToString(pin[:]))
	var cred protocol.Credential
	_, err = client.Request(context.Background(), http.MethodPost, "/v1/nodes/enroll", protocol.Enrollment{Device: protocol.Device{Name: "worker", Platform: "linux", CPUCount: 2, Models: []string{"model"}}}, &cred)
	if err != nil {
		t.Fatal(err)
	}
	client.Token = cred.Token
	client.NodeID = cred.NodeID
	cancelled := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"token\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer backend.Close()
	engine, _ := local.New(backend.URL)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	exited := make(chan error, 1)
	go func() { exited <- (&Runner{Client: client, Engine: engine, Model: "model"}).Run(ctx) }()
	chatCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(chatCtx, http.MethodPost, host.URL+"/v1/chat/completions", strings.NewReader(`{"model":"model","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+admin)
	response, err := host.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "token") {
		t.Fatal(line, err)
	}
	cancel()
	_ = response.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(4 * time.Second):
		t.Fatal("disconnected caller did not cancel native worker request")
	}
	// Runner acknowledges the cancelled job, releasing the lease for future jobs.
	adminClient, _ := netclient.New(host.URL, admin, "")
	_ = adminClient.UsePinnedCertificate(hex.EncodeToString(pin[:]))
	deadline := time.Now().Add(3 * time.Second)
	for {
		var devices []protocol.Device
		_, err = adminClient.Request(context.Background(), http.MethodGet, "/v1/nodes", nil, &devices)
		if err != nil {
			t.Fatal(err)
		}
		if len(devices) == 1 && !devices[0].Busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled worker retained busy lease")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	select {
	case err := <-exited:
		if err != nil && err != context.Canceled && err != io.EOF {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestBoundedResultRespectsCoordinatorJSONBodyLimit(t *testing.T) {
	// JSON escapes control bytes as \u0000. The historical 512 KiB text
	// threshold would expand to more than the 1 MiB request limit.
	result := boundedResult(strings.Repeat("\x00", 128<<10), nil)
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || len(body) > 1<<20 || len(result.Result) > (1<<20)/2 {
		t.Fatalf("valid result cannot be submitted safely: error=%q serialized=%d", result.Error, len(body))
	}
	ascii := boundedResult(strings.Repeat("x", 256<<10), nil)
	if ascii.Error != "" || len(ascii.Result) != 256<<10 {
		t.Fatal("valid 256 KiB ASCII result rejected unnecessarily")
	}
	tooLarge := boundedResult(strings.Repeat("x", (1<<19)+1), nil)
	if tooLarge.Error == "" || tooLarge.Result != "" {
		t.Fatal("oversized result must be reported as a bounded job failure")
	}
	escaped := boundedResult(strings.Repeat("\x00", (1<<20)/6+1), nil)
	if escaped.Error == "" || escaped.Result != "" {
		t.Fatal("JSON-escaped result over 1 MiB must be rejected before submission")
	}
	generationFailure := boundedResult(strings.Repeat("ignored", 40000), fmt.Errorf("%s", strings.Repeat("e", 2000)))
	if generationFailure.Result != "" || len(generationFailure.Error) != 1000 {
		t.Fatalf("generator error was not preserved and bounded: %+v", generationFailure)
	}
}
