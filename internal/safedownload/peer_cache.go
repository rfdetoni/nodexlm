// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package safedownload

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

const (
	peerCacheMaxSlots    = 4
	peerCacheChunkBytes  = 4 << 20
	peerCacheMaxNonces   = 4096
	peerCacheMaxGrantLen = 4096
)

var (
	// ErrPeerCorrupt indicates the remote peer served bytes failing ETag,
	// Content-Range, GGUF magic, or SHA-256 verification and should be quarantined.
	ErrPeerCorrupt = errors.New("peer served corrupt or mismatched model data")
)

type PeerCacheServer struct {
	nodeID       string
	directory    string
	authorityPub ed25519.PublicKey
	clusterID    string

	mu           sync.Mutex
	minEpoch     uint64
	revoked      map[string]struct{}
	nonces       map[string]time.Time
	slots        chan struct{}
	certSHA256   string
	listener     net.Listener
	server       *http.Server
	bytesSent    atomic.Uint64
	requestsOK   atomic.Uint64
	requestsDeny atomic.Uint64
}

func NewPeerCacheServer(nodeID, directory, clusterID string, authorityPub ed25519.PublicKey, minEpoch uint64, listenAddr string) (*PeerCacheServer, error) {
	if nodeID == "" || clusterID == "" || len(authorityPub) != ed25519.PublicKeySize {
		return nil, errors.New("invalid peer cache server identity")
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	cert, certSHA, err := selfSignedPeerTLS()
	if err != nil {
		return nil, err
	}
	p := &PeerCacheServer{
		nodeID:       nodeID,
		directory:    root,
		authorityPub: append(ed25519.PublicKey(nil), authorityPub...),
		clusterID:    clusterID,
		minEpoch:     max(1, minEpoch),
		revoked:      make(map[string]struct{}),
		nonces:       make(map[string]time.Time),
		slots:        make(chan struct{}, peerCacheMaxSlots),
		certSHA256:   certSHA,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /v1/peer/cache/{sha256}", p.serveModel)
	mux.HandleFunc("GET /v1/peer/cache/{sha256}", p.serveModel)
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
	}
	ln, err := tls.Listen("tcp", listenAddr, tlsCfg)
	if err != nil {
		return nil, err
	}
	p.listener = ln
	p.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() { _ = p.server.Serve(ln) }()
	return p, nil
}

func selfSignedPeerTLS() (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "nodexlm-peer-cache"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}, hex.EncodeToString(sum[:]), nil
}

func (p *PeerCacheServer) Address() string    { return p.listener.Addr().String() }
func (p *PeerCacheServer) CertSHA256() string { return p.certSHA256 }
func (p *PeerCacheServer) BytesSent() uint64  { return p.bytesSent.Load() }

func (p *PeerCacheServer) Close() error {
	if p.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return p.server.Shutdown(ctx)
}

func (p *PeerCacheServer) UpdateAuthority(epoch uint64, revoked []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if epoch > p.minEpoch {
		p.minEpoch = epoch
	}
	clear(p.revoked)
	for _, id := range revoked {
		if id != "" {
			p.revoked[id] = struct{}{}
		}
	}
}

func (p *PeerCacheServer) acceptNonce(nonce string, exp time.Time) bool {
	now := time.Now().UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.nonces {
		if now.After(v) {
			delete(p.nonces, k)
		}
	}
	if _, exists := p.nonces[nonce]; exists {
		return false
	}
	if len(p.nonces) >= peerCacheMaxNonces {
		return false
	}
	p.nonces[nonce] = exp
	return true
}

func (p *PeerCacheServer) serveModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	digest := r.PathValue("sha256")
	if !protocol.IsSHA256Hex(digest) {
		p.requestsDeny.Add(1)
		http.Error(w, "invalid digest", http.StatusBadRequest)
		return
	}
	grantRaw := r.Header.Get("X-Nodex-Peer-Grant")
	proof := r.Header.Get("X-Nodex-Peer-Proof")
	peerNode := r.Header.Get("X-Nodex-Node")
	if len(grantRaw) == 0 || len(grantRaw) > peerCacheMaxGrantLen || proof == "" || peerNode == "" {
		p.requestsDeny.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var grant protocol.PeerGrant
	if err := json.Unmarshal([]byte(grantRaw), &grant); err != nil {
		p.requestsDeny.Add(1)
		http.Error(w, "invalid grant", http.StatusUnauthorized)
		return
	}
	p.mu.Lock()
	minEpoch := p.minEpoch
	_, selfRevoked := p.revoked[p.nodeID]
	_, targetRevoked := p.revoked[grant.TargetPeerID]
	p.mu.Unlock()
	if selfRevoked || targetRevoked || grant.TargetPeerID != peerNode || grant.SHA256 != digest {
		p.requestsDeny.Add(1)
		http.Error(w, "revoked or mismatched peer grant", http.StatusForbidden)
		return
	}
	now := time.Now().UTC()
	if err := protocol.VerifyPeerGrant(grant, p.authorityPub, p.clusterID, minEpoch, p.nodeID, "cache", now); err != nil {
		p.requestsDeny.Add(1)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if err := protocol.VerifyPeerRequest(grant.TargetPubKey, proof, r.Method, r.URL.Path, grant.Nonce, now, p.acceptNonce); err != nil {
		p.requestsDeny.Add(1)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "peer cache busy", http.StatusTooManyRequests)
		return
	}
	// OpenVerifiedContent requires the content cache env opt-in; pass explicit P2P env check via helper context.
	if os.Getenv("NODEXLM_MODEL_P2P") != "1" {
		http.Error(w, "peer cache disabled", http.StatusNotFound)
		return
	}
	file, err := OpenVerifiedContent(r.Context(), p.directory, digest)
	if err != nil {
		http.Error(w, "verified model unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.Size() != grant.Size {
		http.Error(w, "verified model mismatch", http.StatusNotFound)
		return
	}
	w.Header().Set("ETag", `"`+digest+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	cw := &countingResponseWriter{ResponseWriter: w, counter: &p.bytesSent}
	p.requestsOK.Add(1)
	http.ServeContent(cw, r, stat.Name(), time.Time{}, file)
}

type countingResponseWriter struct {
	http.ResponseWriter
	counter *atomic.Uint64
}

func (c *countingResponseWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	if n > 0 {
		c.counter.Add(uint64(n))
	}
	return n, err
}

type PeerFetchSource struct {
	NodeID     string             `json:"node_id"`
	Address    string             `json:"address"`
	CertSHA256 string             `json:"cert_sha256"`
	Size       int64              `json:"size"`
	Grant      protocol.PeerGrant `json:"grant"`
}

// FetchFromPeer downloads and verifies a complete GGUF directly from a peer,
// pinning the peer's TLS leaf certificate and signing per-request PoP headers.
func FetchFromPeer(
	ctx context.Context,
	src PeerFetchSource,
	selfNodeID string,
	selfPriv *ecdsa.PrivateKey,
	destination string,
	allowLoopback bool,
	progress func(int64, int64),
) (int64, error) {
	if err := protocol.ValidatePrivatePeerAddress(src.Address, allowLoopback); err != nil {
		return 0, err
	}
	if !protocol.IsSHA256Hex(src.CertSHA256) || !protocol.IsSHA256Hex(src.Grant.SHA256) ||
		src.Size < 8 || src.Size > 150<<30 || src.Grant.Size != src.Size || selfNodeID == "" || selfPriv == nil {
		return 0, errors.New("invalid peer transfer parameters")
	}
	expectedCert, err := hex.DecodeString(src.CertSHA256)
	if err != nil {
		return 0, err
	}
	grantJSON, err := json.Marshal(src.Grant)
	if err != nil {
		return 0, err
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          peerCacheMaxSlots,
		MaxIdleConnsPerHost:   peerCacheMaxSlots,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true, // Verified unconditionally by pinned leaf cert SHA-256 below.
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("peer presented no TLS certificate")
				}
				actual := sha256.Sum256(cs.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(actual[:], expectedCert) != 1 {
					return errors.New("peer TLS certificate pin mismatch")
				}
				return nil
			},
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("peer cache redirects forbidden")
		},
	}

	path := "/v1/peer/cache/" + src.Grant.SHA256
	targetURL := "https://" + src.Address + path
	doReq := func(method, rangeHeader, ifRange string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
		if err != nil {
			return nil, err
		}
		proof, err := protocol.SignPeerRequest(selfPriv, method, path, src.Grant.Nonce, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Nodex-Node", selfNodeID)
		req.Header.Set("X-Nodex-Peer-Grant", string(grantJSON))
		req.Header.Set("X-Nodex-Peer-Proof", proof)
		req.Header.Set("Accept-Encoding", "identity")
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
			req.Header.Set("If-Range", ifRange)
		}
		return client.Do(req)
	}

	headResp, err := doReq(http.MethodHead, "", "")
	if err != nil {
		return 0, err
	}
	_ = headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("peer cache HEAD HTTP %d", headResp.StatusCode)
	}
	expectedETag := `"` + src.Grant.SHA256 + `"`
	if headResp.Header.Get("ETag") != expectedETag || headResp.ContentLength != src.Size {
		return 0, fmt.Errorf("%w: HEAD metadata mismatch", ErrPeerCorrupt)
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return 0, err
	}
	stage, err := os.CreateTemp(filepath.Dir(destination), ".peer-stage-*")
	if err != nil {
		return 0, err
	}
	stagePath := stage.Name()
	cleanup := true
	defer func() {
		_ = stage.Close()
		if cleanup {
			_ = os.Remove(stagePath)
		}
	}()
	if err := stage.Chmod(0600); err != nil {
		return 0, err
	}
	if err := stage.Truncate(src.Size); err != nil {
		return 0, err
	}

	// Stream disjoint byte ranges concurrently (capped at 4 workers), then check
	// complete SHA-256 and GGUF magic before publishing atomically.
	type chunkRange struct{ start, end int64 }
	var ranges []chunkRange
	for offset := int64(0); offset < src.Size; offset += peerCacheChunkBytes {
		end := min(offset+peerCacheChunkBytes-1, src.Size-1)
		ranges = append(ranges, chunkRange{start: offset, end: end})
	}
	jobs := make(chan chunkRange, len(ranges))
	for _, r := range ranges {
		jobs <- r
	}
	close(jobs)

	var transferred atomic.Int64
	var firstErr error
	var errMu sync.Mutex
	setErr := func(e error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		errMu.Unlock()
	}

	workers := min(peerCacheMaxSlots, len(ranges))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 128<<10)
			for r := range jobs {
				if ctx.Err() != nil {
					setErr(ctx.Err())
					return
				}
				errMu.Lock()
				failed := firstErr != nil
				errMu.Unlock()
				if failed {
					return
				}
				rangeHdr := "bytes=" + strconv.FormatInt(r.start, 10) + "-" + strconv.FormatInt(r.end, 10)
				resp, err := doReq(http.MethodGet, rangeHdr, expectedETag)
				if err != nil {
					setErr(err)
					return
				}
				expectedCount := r.end - r.start + 1
				expectedCR := "bytes " + strconv.FormatInt(r.start, 10) + "-" + strconv.FormatInt(r.end, 10) + "/" + strconv.FormatInt(src.Size, 10)
				if resp.StatusCode != http.StatusPartialContent ||
					resp.Header.Get("ETag") != expectedETag ||
					resp.Header.Get("Content-Range") != expectedCR ||
					resp.ContentLength != expectedCount {
					_ = resp.Body.Close()
					setErr(fmt.Errorf("%w: invalid range response %d", ErrPeerCorrupt, resp.StatusCode))
					return
				}
				pos := r.start
				var copied int64
				for {
					if ctx.Err() != nil {
						_ = resp.Body.Close()
						setErr(ctx.Err())
						return
					}
					n, readErr := resp.Body.Read(buf)
					if n > 0 {
						if copied+int64(n) > expectedCount {
							_ = resp.Body.Close()
							setErr(fmt.Errorf("%w: peer exceeded range length", ErrPeerCorrupt))
							return
						}
						if _, writeErr := stage.WriteAt(buf[:n], pos); writeErr != nil {
							_ = resp.Body.Close()
							setErr(writeErr)
							return
						}
						pos += int64(n)
						copied += int64(n)
						total := transferred.Add(int64(n))
						if progress != nil {
							progress(total, src.Size)
						}
					}
					if readErr == io.EOF {
						break
					}
					if readErr != nil {
						_ = resp.Body.Close()
						setErr(readErr)
						return
					}
				}
				_ = resp.Body.Close()
				if copied != expectedCount {
					setErr(fmt.Errorf("%w: truncated peer chunk", ErrPeerCorrupt))
					return
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return 0, firstErr
	}
	if _, err := stage.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: stage}); err != nil {
		return 0, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != src.Grant.SHA256 {
		return 0, fmt.Errorf("%w: SHA-256 mismatch", ErrPeerCorrupt)
	}
	var magic [4]byte
	if _, err := stage.ReadAt(magic[:], 0); err != nil || string(magic[:]) != "GGUF" {
		return 0, fmt.Errorf("%w: missing GGUF header", ErrPeerCorrupt)
	}
	if err := stage.Sync(); err != nil {
		return 0, err
	}
	if err := stage.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(stagePath, destination); err != nil {
		return 0, err
	}
	cleanup = false
	return src.Size, nil
}
