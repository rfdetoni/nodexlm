package rpcmesh

import (
	"errors"
	"math/rand/v2"
	"time"
)

// HTTP 503 can be a temporary capacity limit: retry less frequently without
// making the eight Guest slots reconnect at exactly the same instant.
func nextRPCBackoff(previous time.Duration, err error) time.Duration {
	if err == nil {
		return time.Second
	}
	ceiling := 10 * time.Second
	if errors.Is(err, ErrTunnelCapacity) {
		ceiling = 30 * time.Second
	}
	if previous < time.Second {
		previous = time.Second
	}
	if previous >= ceiling/2 {
		return ceiling
	}
	return previous * 2
}

func jitteredRPCBackoff(delay time.Duration) time.Duration {
	if delay <= 0 {
		delay = time.Second
	}
	return delay + time.Duration(rand.Int64N(int64(delay/4)+1))
}
