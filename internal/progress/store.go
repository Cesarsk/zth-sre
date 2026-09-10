// Package progress persists learner checklist state for the local lab.
package progress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Record struct {
	CompletedSteps []int     `json:"completedSteps"`
	Notes          string    `json:"notes"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Store struct {
	mu      sync.RWMutex
	path    string
	records map[string]Record
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("progress path is required")
	}
	store := &Store{path: path, records: make(map[string]Record)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read progress: %w", err)
	}
	if err := json.Unmarshal(data, &store.records); err != nil {
		return nil, fmt.Errorf("decode progress: %w", err)
	}
	for id, record := range store.records {
		if !validID.MatchString(id) {
			return nil, fmt.Errorf("invalid persisted exercise id %q", id)
		}
		steps, err := normalize(record.CompletedSteps)
		if err != nil {
			return nil, fmt.Errorf("invalid persisted progress for %q: %w", id, err)
		}
		record.CompletedSteps = steps
		store.records[id] = record
	}
	return store, nil
}

func (s *Store) Get(id string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[id]
	return record, ok
}

func (s *Store) Put(id string, steps []int, notes string) (Record, error) {
	if !validID.MatchString(id) {
		return Record{}, fmt.Errorf("invalid exercise id")
	}
	steps, err := normalize(steps)
	if err != nil {
		return Record{}, err
	}
	if len(notes) > 16000 {
		return Record{}, fmt.Errorf("notes exceed 16000 bytes")
	}
	record := Record{CompletedSteps: steps, Notes: notes, UpdatedAt: time.Now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]Record, len(s.records)+1)
	for key, value := range s.records {
		next[key] = value
	}
	next[id] = record
	data, err := json.Marshal(next)
	if err != nil {
		return Record{}, fmt.Errorf("encode progress: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return Record{}, fmt.Errorf("create progress directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".progress-*")
	if err != nil {
		return Record{}, fmt.Errorf("create progress file: %w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Record{}, fmt.Errorf("write progress: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return Record{}, fmt.Errorf("replace progress: %w", err)
	}
	s.records = next
	return record, nil
}

func normalize(steps []int) ([]int, error) {
	if len(steps) > 100 {
		return nil, fmt.Errorf("too many completed steps")
	}
	set := make(map[int]struct{}, len(steps))
	for _, step := range steps {
		if step < 0 || step > 999 {
			return nil, fmt.Errorf("invalid completed step")
		}
		set[step] = struct{}{}
	}
	normalized := make([]int, 0, len(set))
	for step := range set {
		normalized = append(normalized, step)
	}
	sort.Ints(normalized)
	return normalized, nil
}
