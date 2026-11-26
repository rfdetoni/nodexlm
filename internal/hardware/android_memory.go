package hardware

import (
	"encoding/json"
	"io"
	"os"
	"time"
)

func androidMemory() (total, available, reserve uint64, ok bool) {
	path := os.Getenv("NODEXLM_ANDROID_MEMORY_FILE")
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 || time.Since(info.ModTime()) > 45*time.Second {
		return
	}
	var sample struct {
		Total     uint64 `json:"total"`
		Available uint64 `json:"available"`
		Reserve   uint64 `json:"reserve"`
	}
	if err = json.NewDecoder(io.LimitReader(f, 1024)).Decode(&sample); err != nil ||
		sample.Total == 0 || sample.Total > 1<<50 || sample.Available > sample.Total || sample.Reserve > sample.Total {
		return
	}
	return sample.Total, sample.Available, sample.Reserve, true
}
