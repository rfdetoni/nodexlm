// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package protocol

import "time"

const APIVersion = "v1"

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	TargetNode  string    `json:"target_node,omitempty"`
}

type Device struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Platform     string    `json:"platform"`
	Architecture string    `json:"architecture"`
	CPUCount     int       `json:"cpu_count"`
	RAMBytes     uint64    `json:"ram_bytes,omitempty"`
	Models       []string  `json:"models,omitempty"`
	LastSeen     time.Time `json:"last_seen"`
	Status       string    `json:"status"`
	Busy         bool      `json:"busy"`
}

type Enrollment struct {
	Device Device `json:"device"`
}

type Credential struct {
	NodeID string `json:"node_id"`
	Token  string `json:"token"`
}

type Job struct {
	ID        string      `json:"id"`
	NodeID    string      `json:"node_id,omitempty"`
	Request   ChatRequest `json:"request"`
	Status    string      `json:"status"`
	Created   time.Time   `json:"created_at"`
	Started   *time.Time  `json:"started_at,omitempty"`
	Completed *time.Time  `json:"completed_at,omitempty"`
	Result    string      `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type JobResult struct {
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type ModelRecord struct {
	Name          string `json:"name"`
	Size          int64  `json:"size_bytes"`
	Architecture  string `json:"architecture,omitempty"`
	MoE           bool   `json:"moe"`
	ExpertCount   uint32 `json:"expert_count,omitempty"`
	ActiveExperts uint32 `json:"active_experts,omitempty"`
}
