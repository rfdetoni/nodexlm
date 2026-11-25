// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
	"errors"
	"math"
	"net"
	"time"
)

type TensorMeasurement struct {
	Device               uint32  `json:"device"`
	Bytes                uint32  `json:"bytes"`
	UploadMiBPerSecond   float64 `json:"upload_mib_per_second"`
	ReadbackMiBPerSecond float64 `json:"readback_mib_per_second"`
}

type Microbenchmark struct {
	Source     string              `json:"source"`
	MeasuredAt time.Time           `json:"measured_at"`
	Devices    []TensorMeasurement `json:"devices"`
}

// Microbench uses the authenticated private relay, with at most two native
// devices (the supported CPU/GPU topology), 160 MiB wire payload and 30 s.
func Microbench(ctx context.Context, endpoint string) (Microbenchmark, error) {
	result := Microbenchmark{Source: "auto"}
	_, err := withRPCProbe(ctx, endpoint, 30*time.Second, func(conn net.Conn, count uint32) error {
		if count > 2 {
			return errors.New("microbenchmark supports at most two RPC devices")
		}
		for device := uint32(0); device < count; device++ {
			for _, size := range []uint32{8 << 20, 32 << 20} {
				if err := ctx.Err(); err != nil {
					return err
				}
				sample, err := measureNativeTensor(conn, device, size)
				if err != nil {
					return err
				}
				if sample.UploadMiBPerSecond <= 0 || sample.ReadbackMiBPerSecond <= 0 ||
					math.IsInf(sample.UploadMiBPerSecond, 0) || math.IsInf(sample.ReadbackMiBPerSecond, 0) {
					return errors.New("invalid microbenchmark duration")
				}
				result.Devices = append(result.Devices, sample)
			}
		}
		return nil
	})
	if err != nil {
		return Microbenchmark{}, err
	}
	result.MeasuredAt = time.Now()
	return result, nil
}
