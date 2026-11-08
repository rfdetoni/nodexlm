package openai

import (
	"encoding/json"
	"sync"
	"time"
)

// Responses are private to the authenticated credential and bounded in RAM.
// Restarting the Host clears them; store:false never retains a response.
type storedResponse struct {
	Owner   string
	Created time.Time
	Body    json.RawMessage
	Input   []json.RawMessage
	Output  []json.RawMessage
	Size    int
}
type responseStore struct {
	mu      sync.Mutex
	entries map[string]storedResponse
	bytes   int
}

const maxStoredBytes = 32 << 20

func (s *responseStore) pruneLocked() {
	for id, v := range s.entries {
		if time.Since(v.Created) > time.Hour {
			delete(s.entries, id)
			s.bytes -= v.Size
		}
	}
}
func (s *responseStore) get(id, owner string) (storedResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	v, ok := s.entries[id]
	return v, ok && v.Owner == owner
}
func (s *responseStore) remove(id, owner string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	v, ok := s.entries[id]
	if !ok || v.Owner != owner {
		return false
	}
	delete(s.entries, id)
	s.bytes -= v.Size
	return true
}
func (s *responseStore) put(id string, v storedResponse) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if s.entries == nil {
		s.entries = map[string]storedResponse{}
	}
	input, _ := json.Marshal(v.Input)
	output, _ := json.Marshal(v.Output)
	v.Size = len(v.Body) + len(input) + len(output)
	if v.Size > maxStoredBytes {
		return false
	}
	for len(s.entries) >= 256 || s.bytes+v.Size > maxStoredBytes {
		var oldest string
		var created time.Time
		for id, item := range s.entries {
			if oldest == "" || item.Created.Before(created) {
				oldest = id
				created = item.Created
			}
		}
		s.bytes -= s.entries[oldest].Size
		delete(s.entries, oldest)
	}
	if previous, ok := s.entries[id]; ok {
		s.bytes -= previous.Size
	}
	s.entries[id] = v
	s.bytes += v.Size
	return true
}
