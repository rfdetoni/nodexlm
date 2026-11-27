package dashboard

import (
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// Only explicit hardware fields leave this endpoint; never device IDs, host
// names, keys, addresses, model paths or raw runtime diagnostic strings.
func publicBenchmarkDevice(d protocol.Device, role string) map[string]any {
	gpus := make([]map[string]any, 0, len(d.GPUs))
	for _, g := range d.GPUs {
		gpus = append(gpus, map[string]any{"name": g.Name, "vendor": g.Vendor, "memory_type": g.MemoryType, "vram_bytes": g.VRAMBytes, "vram_used_bytes": g.VRAMUsedBytes, "usage_percent": g.UsagePercent})
	}
	return map[string]any{"role": role, "platform": d.Platform, "architecture": d.Architecture, "cpu_count": d.CPUCount, "cpu_usage_percent": d.CPUUsagePercent, "ram_bytes": d.RAMBytes, "ram_used_bytes": d.RAMUsedBytes, "battery_percent": d.BatteryPercent, "charging": d.Charging, "thermal_status": d.ThermalStatus, "thermal_headroom": d.ThermalHeadroom, "gpus": gpus, "runtime_version": d.Runtime.Version, "rpc_devices": d.Runtime.RPCDevices, "status": d.Status}
}

func (d *Dashboard) benchmarkMetadata() map[string]any {
	s := d.Manager.Settings()
	devices := []map[string]any{publicBenchmarkDevice(d.hostSnapshot(), "Host")}
	for _, guest := range d.Cluster.DevicesSnapshot() {
		if guest.Status == "online" {
			devices = append(devices, publicBenchmarkDevice(guest, "Guest"))
		}
	}
	return map[string]any{"measured_at": time.Now().UTC(), "pinned_runtime": llamaruntime.Version, "pinned_commit": llamaruntime.SourceCommit, "devices": devices, "settings": map[string]any{"backend": s.Backend, "threads": s.Threads, "threads_batch": s.ThreadsBatch, "context_tokens": s.ContextTokens, "parallel_slots": s.ParallelSlots, "gpu_layers": s.GPULayers, "host_gpu": s.HostGPU, "batch_size": s.BatchSize, "ubatch_size": s.UBatchSize, "kv_cache_type": s.KVCacheType, "flash_attention": s.FlashAttention, "rpc_native_scheduler": s.RPCNativeScheduler, "placement_strategy": s.PlacementStrategy, "distributed": s.Distributed, "speculative_mode": s.SpeculativeMode, "cache_reuse_tokens": s.CacheReuseTokens, "moe_cpu": s.MoECPU}}
}
