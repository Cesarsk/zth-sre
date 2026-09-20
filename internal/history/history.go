package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Record struct {
	RunID        string    `json:"runID"`
	Scenario     string    `json:"scenario"`
	StartedAt    time.Time `json:"startedAt"`
	CheckedAt    time.Time `json:"checkedAt"`
	Passed       bool      `json:"passed"`
	Feedback     string    `json:"feedback"`
	Availability float64   `json:"availability,omitempty"`
	P95MS        float64   `json:"p95LatencyMS,omitempty"`
	Score        int       `json:"score,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	path    string
	records []Record
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, records: []Record{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run history: %w", err)
	}
	if err := json.Unmarshal(data, &s.records); err != nil {
		return nil, fmt.Errorf("decode run history: %w", err)
	}
	if len(s.records) > 100 {
		s.records = s.records[len(s.records)-100:]
	}
	return s, nil
}

func (s *Store) List() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Record{}, s.records...)
}

func (s *Store) Add(record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	if len(s.records) > 100 {
		s.records = s.records[len(s.records)-100:]
	}
	data, err := json.Marshal(s.records)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".runs-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, s.path)
}
