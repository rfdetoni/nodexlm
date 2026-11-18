// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// ErrNoDecisionWorkers means the caller may try the original local runtime.
// A worker failure is a different error; the coordinator only retries requests
// that contain data and questions, never side-effecting tool calls.
var ErrNoDecisionWorkers = errors.New("no matching decision worker online")

const decisionLeaseTimeout = 35 * time.Second

// DecisionWorker describes a *whole-model* System One executor. It is never a
// GGML-RPC tensor server. A worker advertises only models it can actually run.
type DecisionWorker struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	Model        string `json:"model"`
	Busy         bool   `json:"busy"`
	P50MS        int64  `json:"p50_ms,omitempty"`
	P95MS        int64  `json:"p95_ms,omitempty"`
	Score        int    `json:"score"`
}

type decisionChoice struct {
	id    string
	name  string
	score int
}

// DecisionWorkers is a localhost-only snapshot for the dashboard.
func (s *Server) DecisionWorkers() []DecisionWorker {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	workers := make([]DecisionWorker, 0)
	for id, n := range s.nodes {
		if n.device.Status != "online" || now.Sub(n.device.LastSeen) >= heartbeatGrace ||
			n.decisionSeen.IsZero() || now.Sub(n.decisionSeen) >= heartbeatGrace {
			continue
		}
		p50, p95 := decisionPercentiles(s.decisionTimings[id])
		for _, model := range n.device.DecisionModels {
			workers = append(workers, DecisionWorker{
				ID: id, Name: n.device.Name, Platform: n.device.Platform,
				Model: model, Busy: n.active != "", P50MS: p50, P95MS: p95,
				Score: decisionScore(n.device, n.active != "") - min(int(p50/100), 30),
			})
		}
	}
	sort.Slice(workers, func(i, j int) bool {
		if workers[i].Score == workers[j].Score {
			return workers[i].ID < workers[j].ID
		}
		return workers[i].Score > workers[j].Score
	})
	return workers
}

func decisionPercentiles(samples []time.Duration) (int64, int64) {
	if len(samples) == 0 {
		return 0, 0
	}
	order := append([]time.Duration(nil), samples...)
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	p50 := order[(len(order)-1)/2]
	p95 := order[(len(order)*95+99)/100-1]
	return p50.Milliseconds(), p95.Milliseconds()
}

func decisionScore(device protocol.Device, busy bool) int {
	score := 100
	if busy {
		score -= 50
	}
	if device.CPUUsagePercent != nil {
		score -= int(*device.CPUUsagePercent / 4)
	}
	if device.ThermalStatus != nil {
		score -= *device.ThermalStatus * 10
	}
	if device.BatteryPercent != nil && *device.BatteryPercent < 20 &&
		(device.Charging == nil || !*device.Charging) {
		score -= 35
	}
	if device.AvailableRAMBytes >= 2<<30 {
		score += 10
	}
	if len(device.GPUs) > 0 {
		score += 5
	}
	return score
}

func (s *Server) decisionCandidates(model string) []decisionChoice {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var result []decisionChoice
	for id, n := range s.nodes {
		if n.device.Status != "online" || now.Sub(n.device.LastSeen) >= heartbeatGrace ||
			n.decisionSeen.IsZero() || now.Sub(n.decisionSeen) >= heartbeatGrace ||
			(n.device.ThermalStatus != nil && *n.device.ThermalStatus >= 5) {
			continue
		}
		if !modelAvailable(n.device.DecisionModels, model) {
			continue
		}
		p50, _ := decisionPercentiles(s.decisionTimings[id])
		result = append(result, decisionChoice{id: id, name: n.device.Name,
			score: decisionScore(n.device, n.active != "") - min(int(p50/100), 30)})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].score == result[j].score {
			return result[i].id < result[j].id
		}
		return result[i].score > result[j].score
	})
	return result
}

// Decide routes typed System One inference to the best compatible worker and
// retries another node after a failed result, disconnect, or bounded timeout.
// The original model name/typed JSON is not interpreted or recalibrated.
func (s *Server) Decide(ctx context.Context, model string, body []byte) ([]byte, error) {
	if len(model) == 0 || len(model) > 256 || len(body) == 0 || len(body) > maxBody/2 || !json.Valid(body) {
		return nil, errors.New("invalid decision request")
	}
	candidates := s.decisionCandidates(model)
	if len(candidates) == 0 {
		return nil, ErrNoDecisionWorkers
	}
	var lastErr error
	for _, n := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		raw, err := s.attemptDecision(ctx, n.id, model, body)
		if err == nil {
			return raw, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("decision workers failed: %w", lastErr)
}

func (s *Server) attemptDecision(ctx context.Context, nodeID, model string, body []byte) ([]byte, error) {
	id, err := token()
	if err != nil {
		return nil, err
	}
	job := &protocol.Job{
		ID: id, Kind: "decision", Request: protocol.ChatRequest{Model: model, TargetNode: nodeID},
		Decision: append(json.RawMessage(nil), body...), Created: time.Now(), Status: "queued",
	}
	s.mu.Lock()
	if len(s.queue) >= 128 || len(s.jobs) >= 4096 {
		s.mu.Unlock()
		return nil, errors.New("decision job queue is full")
	}
	n := s.nodes[nodeID]
	if n == nil || !modelAvailable(n.device.DecisionModels, model) {
		s.mu.Unlock()
		return nil, ErrNoDecisionWorkers
	}
	s.jobs[id] = job
	e := &jobEvents{changed: make(chan struct{}, 1)}
	s.events[id] = e
	s.queue = append(s.queue, id)
	select {
	case n.signal <- struct{}{}:
	default:
	}
	s.mu.Unlock()

	// A disconnected client must not leave a queued request or a node lease.
	defer func() {
		s.mu.Lock()
		s.cancelLocked(id, "decision attempt ended")
		s.mu.Unlock()
	}()
	timer, cancel := context.WithTimeout(ctx, decisionLeaseTimeout)
	defer cancel()
	heartbeat := time.NewTicker(2 * time.Second)
	defer heartbeat.Stop()
	for {
		s.mu.Lock()
		current := s.jobs[id]
		var status, result, detail string
		if current != nil {
			status, result, detail = current.Status, current.Result, current.Error
		}
		owner := s.nodes[nodeID]
		lost := owner == nil || owner.device.Status != "online" || time.Since(owner.device.LastSeen) > heartbeatGrace
		s.mu.Unlock()
		switch status {
		case "completed":
			if len(result) == 0 || len(result) > maxBody/2 || !json.Valid([]byte(result)) {
				return nil, errors.New("worker response is not a bounded JSON decision")
			}
			return []byte(result), nil
		case "failed", "cancelled":
			return nil, fmt.Errorf("worker reported %s: %s", status, detail)
		case "":
			return nil, errors.New("worker job disappeared")
		}
		if lost {
			return nil, errors.New("decision worker disconnected")
		}
		select {
		case <-e.changed:
		case <-heartbeat.C:
		case <-timer.Done():
			return nil, timer.Err()
		}
	}
}

// Normalize a supported decision model alias without permitting arbitrary URL
// selection. A node decides whether to advertise the exact user-visible name.
func ValidDecisionModel(name string) bool {
	return name != "" && len(name) <= 256 && strings.TrimSpace(name) == name &&
		!strings.ContainsAny(name, "\r\n\x00")
}
