package dashboard

import (
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// effectivePlacementStrategy uses low-latency execution unless the user
// explicitly opts into capacity mode. Empty legacy settings are speed-first.
func effectivePlacementStrategy(value string) string {
	if value == "" {
		return "speed"
	}
	return value
}

// placementBenchmarks accepts only fresh, comparable controlled measurements.
func placementBenchmarks(samples []InferenceSample, fingerprint string, now time.Time) map[string]InferenceSample {
	valid := make(map[string]InferenceSample)
	for _, sample := range samples {
		if fingerprint == "" || sample.Fingerprint != fingerprint || sample.Kind != "benchmark" || sample.Suspect ||
			(sample.RateBasis != "native_decode" && sample.RateBasis != "end_to_end") ||
			sample.TokensPerSecond <= 0 || math.IsNaN(sample.TokensPerSecond) || math.IsInf(sample.TokensPerSecond, 0) ||
			sample.MeasuredAt.After(now) || now.Sub(sample.MeasuredAt) >= 24*time.Hour {
			continue
		}
		if previous, ok := valid[sample.Model]; !ok || sample.MeasuredAt.After(previous.MeasuredAt) {
			valid[sample.Model] = sample
		}
	}
	return valid
}

type placementMeasurements struct {
	samples     []InferenceSample
	fingerprint string
	now         time.Time
}

func (m *Manager) placementMeasurements(s Settings) placementMeasurements {
	return placementMeasurements{samples: m.InferenceSamples(), fingerprint: m.benchmarkSignature(s, s.Model), now: time.Now()}
}

// chooseRPC is used both for preview and actual native model loading.
func (m *Manager) chooseRPC(s Settings, meta local.GGUFInfo, size uint64, hostBudget uint64, guests []RPCGuest) (clusterplan.Selection, error) {
	return m.chooseRPCWithMeasurements(s, meta, size, hostBudget, guests, m.placementMeasurements(s))
}

func (m *Manager) chooseRPCWithMeasurements(s Settings, meta local.GGUFInfo, size uint64, hostBudget uint64, guests []RPCGuest, measurements placementMeasurements) (result clusterplan.Selection, resultErr error) {
	defer func() {
		if resultErr == nil && selectedMultiGPU(result, guests) && len(planRPCDevices(result, guests, meta, s, size).Devices) == 0 {
			result, resultErr = clusterplan.Selection{}, errors.New("multi-GPU split exceeds per-device budget; reduce context or add capacity")
		}
	}()
	if explicitPrimary(s.PrimaryNode) {
		return choosePrimary(s, meta, size, hostBudget, guests)
	}
	allSamples, fingerprint := measurements.samples, measurements.fingerprint
	samples := placementBenchmarks(allSamples, fingerprint, measurements.now)
	// A suspect aggregate cluster rate is not a per-node placement rate.
	for _, sample := range allSamples {
		if !sample.Suspect || sample.Fingerprint != fingerprint || sample.Kind != "benchmark" {
			continue
		}
		relevant := sample.Model == s.Model
		for _, guest := range guests {
			relevant = relevant || sample.Model == s.Model+"@"+guest.ID
		}
		if relevant {
			clear(samples)
			defer func() {
				if resultErr == nil {
					result.Reason += "; medição suspeita; re-medir benchmark controlado (sem ponderação por tokens/s)"
				}
			}()
			break
		}
	}
	hostSample := samples[s.Model]
	hostSpeed := hostSample.TokensPerSecond
	basis := hostSample.RateBasis
	if hostSpeed == 0 {
		// Without a Host reference, never compare native decode with end-to-end rates.
		for _, guest := range guests {
			if guest.CapacityBytes == 0 {
				continue
			}
			sample, ok := samples[s.Model+"@"+guest.ID]
			if !ok {
				continue
			}
			if basis != "" && basis != sample.RateBasis {
				basis = ""
				break
			}
			basis = sample.RateBasis
		}
	}
	strategy := effectivePlacementStrategy(s.PlacementStrategy)
	// A connected Guest is optional for local inference, even when an older
	// runtime.json explicitly saved "capacity" with distributed=true.
	// Prefer the Host if there is no usable RPC provider, but never silently
	// overcommit its RAM. Plan() and load() both call this same decision.
	if len(guests) == 0 {
		if meta.BlockCount == 0 {
			return clusterplan.Selection{}, errors.New("GGUF block_count is required for distributed placement")
		}
		if size == 0 || size > ^uint64(0)/115 || size*115/100 > hostBudget {
			return clusterplan.Selection{}, fmt.Errorf(
				"nenhum Guest RPC conectado e memória local insuficiente: modelo de %d MiB requer margem para pesos e contexto, Host dispõe de %d MiB; conecte um Guest, reduza o modelo ou libere memória no Host",
				size/clusterplan.MiB, hostBudget/clusterplan.MiB)
		}
		if err := validatePlacementMemory(meta, s, size, hostBudget, nil); err != nil {
			return clusterplan.Selection{}, fmt.Errorf("nenhum Guest RPC conectado e o modelo não cabe integralmente no Host: %w", err)
		}
		return clusterplan.Selection{
			LocalLayers: int(meta.BlockCount),
			Reason:      "Nenhum Guest RPC conectado: executando o modelo integralmente no Host, sem exigir pareamento; a distribuição poderá ser utilizada quando houver Guests",
		}, nil
	}
	// When the complete GGUF and context fit on the Host, split execution
	// incurs extra RPC synchronization on EVERY decoded token. A remote
	// benchmark may only beat Host-only when both were measured with the same
	// benchmark and the chosen remote can fit the whole model independently.
	// Never compare an unmeasured Host with a measured Guest and assume RPC
	// is faster; and never infer split-cluster speed from Guest-only metrics.
	if strategy == "speed" && size > 0 && size <= ^uint64(0)/115 &&
		size*115/100 <= hostBudget &&
		validatePlacementMemory(meta, s, size, hostBudget, nil) == nil &&
		meta.BlockCount > 0 {
		var best *RPCGuest
		var bestSpeed float64
		// Require 15% headroom before claiming an RPC advantage. Small
		// network fluctuations must not trigger topology thrashing.
		if hostSpeed > 0 && size <= ^uint64(0)/115 {
			required := size * 115 / 100
			for i := range guests {
				guest := &guests[i]
				sample := samples[s.Model+"@"+guest.ID]
				measured := sample.TokensPerSecond
				if measured <= hostSpeed*1.15 || measured <= bestSpeed ||
					sample.RateBasis != hostSample.RateBasis || guest.CapacityBytes < required {
					continue
				}
				candidate := clusterplan.Candidate{ID: guest.ID, CapacityBytes: guest.CapacityBytes}
				if validatePlacementMemory(meta, s, size, 0, []clusterplan.Candidate{candidate}) != nil {
					continue
				}
				best, bestSpeed = guest, measured
			}
		}
		if best != nil {
			excluded := make([]string, 0, len(guests)-1)
			for _, guest := range guests {
				if guest.ID != best.ID {
					excluded = append(excluded, guest.ID)
				}
			}
			return clusterplan.Selection{
				LocalLayers: 0,
				Guests:      []clusterplan.Candidate{{ID: best.ID, CapacityBytes: best.CapacityBytes}},
				Excluded:    excluded,
				Reason:      "Single remote Guest benchmark exceeded Host-only decode by at least 15%; no layer splitting between Host and Guest",
			}, nil
		}
		excluded := make([]string, 0, len(guests))
		for _, guest := range guests {
			excluded = append(excluded, guest.ID)
		}
		return clusterplan.Selection{
			LocalLayers: int(meta.BlockCount), Excluded: excluded,
			Reason: "Host-only memory admission passed; keep sequential decode local unless the same benchmark proves a whole-model Guest at least 15% faster. Capacity mode remains available explicitly",
		}, nil
	}
	candidates := make([]clusterplan.Candidate, 0, len(guests))
	unavailable := make([]string, 0)
	seen := make(map[string]bool, len(guests))
	for _, guest := range guests {
		if guest.ID == "" || seen[guest.ID] {
			return clusterplan.Selection{}, errors.New("duplicate or missing RPC Guest ID")
		}
		seen[guest.ID] = true
		if guest.CapacityBytes == 0 {
			// A connected Android may advertise 0 allocatable RAM during
			// memory pressure. It cannot be selected but should not prevent
			// other healthy Guests from contributing their free capacity.
			unavailable = append(unavailable, guest.ID)
			continue
		}
		candidate := clusterplan.Candidate{ID: guest.ID, CapacityBytes: guest.CapacityBytes}
		if network := m.networkSample(guest.ID); network != nil {
			candidate.Performance.NetworkMeasured = true
			candidate.Performance.RoundTripMS = network.RoundTripMS
			candidate.Performance.MiBPerSecond = network.MiBPerSecond
		}
		if sample, ok := samples[s.Model+"@"+guest.ID]; ok &&
			basis != "" && sample.RateBasis == basis {
			candidate.Performance.TokensPerSecond = sample.TokensPerSecond
		}
		if micro := m.microbenchSample(guest.ID); micro != nil {
			for _, sample := range micro.Devices {
				if sample.Bytes == 32<<20 {
					rate := min(sample.UploadMiBPerSecond, sample.ReadbackMiBPerSecond)
					if rate > 0 && (candidate.Performance.MicrobenchMiBPerSecond == 0 || rate < candidate.Performance.MicrobenchMiBPerSecond) {
						candidate.Performance.MicrobenchMiBPerSecond = rate
					}
				}
			}
		}
		candidates = append(candidates, candidate)
	}
	selectPlacement := clusterplan.SelectWithHostSpeed
	if strategy == "speed" && os.Getenv("NODEXLM_RPC_COST_PLACEMENT") == "1" {
		selectPlacement = clusterplan.SelectWithHostSpeedAndRTT
	}
	selection, err := selectPlacement(meta.BlockCount, size, hostBudget, hostSpeed, candidates, strategy)
	if err != nil {
		return selection, err
	}
	if err = validatePlacementMemory(meta, s, size, hostBudget, selection.Guests); err == nil {
		err = applyLayerBytes(meta, s, size, hostBudget, guests, &selection)
	}
	if err == nil {
		selection.Excluded = append(selection.Excluded, unavailable...)
		if len(unavailable) > 0 {
			selection.Reason += "; excluded Guests with no allocatable RAM"
		}
		return selection, nil
	}
	if strategy != "speed" {
		return clusterplan.Selection{}, err
	}
	// A speed-optimized subset can fit the weights but not the KV/context.
	// Preserve correctness by retrying placement with all healthy Guests.
	full, fullErr := clusterplan.SelectWithHostSpeed(meta.BlockCount, size, hostBudget, hostSpeed, candidates, "capacity")
	if fullErr != nil {
		return clusterplan.Selection{}, fullErr
	}
	if contextErr := validatePlacementMemory(meta, s, size, hostBudget, full.Guests); contextErr != nil {
		return clusterplan.Selection{}, contextErr
	}
	if err := applyLayerBytes(meta, s, size, hostBudget, guests, &full); err != nil {
		return clusterplan.Selection{}, err
	}
	full.Reason += "; memory admission requires additional RPC Guests; speed subset rejected"
	full.Excluded = append(full.Excluded, unavailable...)
	return full, nil
}

func applyLayerBytes(meta local.GGUFInfo, s Settings, size, host uint64, available []RPCGuest, selection *clusterplan.Selection) error {
	if os.Getenv("NODEXLM_GGUF_LAYER_BYTES") != "1" || len(meta.BlockBytes) < 2 || len(meta.BlockBytes) != int(meta.BlockCount) || len(selection.Guests) == 0 {
		return nil
	}
	uniform := true
	for _, weight := range meta.BlockBytes[1:] {
		if weight != meta.BlockBytes[0] {
			uniform = false
			break
		}
	}
	if uniform {
		return nil
	}
	// Without explicit native device ordering, the exact layer split cannot
	// be expressed safely. Keep the historical legacy placement.
	byID := make(map[string]RPCGuest, len(available))
	for _, guest := range available {
		byID[guest.ID] = guest
	}
	for _, chosen := range selection.Guests {
		if !validRPCDeviceKinds(byID[chosen.ID]) {
			return nil
		}
	}
	budgets := make([]uint64, len(selection.Guests))
	for i, guest := range selection.Guests {
		budgets[i] = guest.CapacityBytes
	}
	allocation, ok := clusterplan.LayerAllocation(meta.BlockBytes, meta.NonBlockBytes, host, budgets, selection.LocalLayers)
	if !ok {
		return errors.New("estimated contiguous GGUF layers do not fit selected node budgets")
	}
	candidate := *selection
	candidate.LocalLayers = allocation[0]
	candidate.GuestLayers = allocation[1:]
	if plan := planRPCDevices(candidate, available, meta, s, size); len(plan.Devices) == 0 {
		return errors.New("estimated GGUF layer split exceeds native device budgets")
	}
	*selection = candidate
	selection.Reason += "; estimated actual GGUF tensor bytes"
	return nil
}
