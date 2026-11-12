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
	CachePrompt           *bool          `json:"cache_prompt,omitempty"`
	StreamOptions         *StreamOptions `json:"stream_options,omitempty"`
	Model                 string         `json:"model"`
	Messages              []Message      `json:"messages"`
	MaxTokens             int            `json:"max_tokens,omitempty"`
	ReasoningEffort       string         `json:"reasoning_effort,omitempty"`
	ReasoningBudgetTokens int            `json:"reasoning_budget_tokens,omitempty"`
	Temperature           float64        `json:"temperature"`
	TopP                  *float64       `json:"top_p,omitempty"`
	Stop                  Stop           `json:"stop,omitempty"`
	Priority              int            `json:"priority,omitempty"`
	Stream                bool           `json:"stream,omitempty"`
	TargetNode            string         `json:"target_node,omitempty"`
}

type GPUDevice struct {
	Name   string `json:"name"`
	Vendor string `json:"vendor"`
	// shared means GPU heaps use system RAM; dedicated means separate VRAM.
	// Empty is legacy/unknown and must not be treated as dedicated.
	MemoryType    string `json:"memory_type,omitempty"`
	VRAMBytes     uint64 `json:"vram_bytes,omitempty"`
	FreeVRAMBytes uint64 `json:"free_vram_bytes,omitempty"`
	// Nil means the driver cannot measure this value; 0 means measured idle/empty.
	UsagePercent  *float64 `json:"usage_percent,omitempty"`
	VRAMUsedBytes *uint64  `json:"vram_used_bytes,omitempty"`
}

type RuntimeCapability struct {
	ReconnectFailures uint64 `json:"reconnect_failures,omitempty"`
	Benchmark         bool   `json:"benchmark,omitempty"`
	Engine            string `json:"engine,omitempty"`
	Version           string `json:"version,omitempty"`
	Installed         bool   `json:"installed"`
	RPCReady          bool   `json:"rpc_ready"`
	// RPCDevices lists native device kinds in ggml-rpc-server exposure order.
	// For example ["gpu","cpu"] means two distinct RPC devices on one Guest.
	// Empty is a legacy worker whose device topology is unknown.
	RPCDevices []string `json:"rpc_devices,omitempty"`
}

type Device struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Platform          string   `json:"platform"`
	Architecture      string   `json:"architecture"`
	CPUCount          int      `json:"cpu_count"`
	CPUUsagePercent   *float64 `json:"cpu_usage_percent,omitempty"`
	RAMUsedBytes      *uint64  `json:"ram_used_bytes,omitempty"`
	RAMBytes          uint64   `json:"ram_bytes,omitempty"`
	AvailableRAMBytes uint64   `json:"available_ram_bytes,omitempty"`
	// User-defined maximum weight contribution and minimum free OS reserve.
	MaxContributionBytes uint64 `json:"max_contribution_bytes,omitempty"`
	MemoryReserveBytes   uint64 `json:"memory_reserve_bytes,omitempty"`
	// Nil represents unavailable telemetry; zero is a measured value.
	BatteryPercent  *int              `json:"battery_percent,omitempty"`
	Charging        *bool             `json:"charging,omitempty"`
	ThermalStatus   *int              `json:"thermal_status,omitempty"`
	ThermalHeadroom *float64          `json:"thermal_headroom,omitempty"`
	GPUs            []GPUDevice       `json:"gpus,omitempty"`
	Runtime         RuntimeCapability `json:"runtime"`
	Models          []string          `json:"models,omitempty"`
	LastSeen        time.Time         `json:"last_seen"`
	// Authenticated Guest may withdraw from new placements (draining).
	Status            string `json:"status"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	Busy              bool   `json:"busy"`
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

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}
