package progression

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Badge struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	EarnedAt    time.Time `json:"earnedAt"`
}

type Award struct {
	RunID    string    `json:"runID"`
	Scenario string    `json:"scenario"`
	Score    int       `json:"score"`
	EarnedAt time.Time `json:"earnedAt"`
}

type Profile struct {
	XP           int            `json:"xp"`
	Rank         string         `json:"rank"`
	NextRank     string         `json:"nextRank,omitempty"`
	NextRankXP   int            `json:"nextRankXP,omitempty"`
	Badges       []Badge        `json:"badges"`
	Awards       []Award        `json:"awards"`
	CompletedIDs []string       `json:"completedIDs"`
	BestScores   map[string]int `json:"bestScores"`
}

type Store struct {
	mu      sync.RWMutex
	path    string
	profile Profile
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, profile: Profile{Badges: []Badge{}, Awards: []Award{}, CompletedIDs: []string{}, BestScores: map[string]int{}}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		s.applyRank()
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read learner profile: %w", err)
	}
	if err := json.Unmarshal(data, &s.profile); err != nil {
		return nil, fmt.Errorf("decode learner profile: %w", err)
	}
	s.normalize()
	s.applyRank()
	return s, nil
}

func (s *Store) Profile() Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.profile)
}

// Award records a completed mission exactly once per run. Replays earn only
// the improvement over the scenario's previous best score.
func (s *Store) Award(runID, scenario string, score int, badges []Badge) (Profile, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.profile)
	for _, award := range next.Awards {
		if award.RunID == runID {
			return clone(s.profile), 0, nil
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	previous := next.BestScores[scenario]
	xp := score - previous
	if xp < 0 {
		xp = 0
	}
	now := time.Now().UTC()
	next.XP += xp
	next.Awards = append(next.Awards, Award{RunID: runID, Scenario: scenario, Score: score, EarnedAt: now})
	if score > previous {
		next.BestScores[scenario] = score
	}
	if !contains(next.CompletedIDs, scenario) {
		next.CompletedIDs = append(next.CompletedIDs, scenario)
		sort.Strings(next.CompletedIDs)
	}
	for _, badge := range badges {
		if !hasBadge(next.Badges, badge.ID) {
			badge.EarnedAt = now
			next.Badges = append(next.Badges, badge)
		}
	}
	next.Rank, next.NextRank, next.NextRankXP = rankFor(next.XP)
	if err := persist(s.path, next); err != nil {
		return clone(s.profile), 0, err
	}
	s.profile = next
	return clone(s.profile), xp, nil
}

func (s *Store) applyRank() {
	s.profile.Rank, s.profile.NextRank, s.profile.NextRankXP = rankFor(s.profile.XP)
}

func (s *Store) normalize() {
	if s.profile.Badges == nil {
		s.profile.Badges = []Badge{}
	}
	if s.profile.Awards == nil {
		s.profile.Awards = []Award{}
	}
	if s.profile.CompletedIDs == nil {
		s.profile.CompletedIDs = []string{}
	}
	if s.profile.BestScores == nil {
		s.profile.BestScores = map[string]int{}
	}
}

func persist(path string, profile Profile) error {
	data, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".learner-*")
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
	return os.Rename(name, path)
}

func rankFor(xp int) (string, string, int) {
	switch {
	case xp < 250:
		return "Observer", "Investigator", 250
	case xp < 750:
		return "Investigator", "Responder", 750
	case xp < 1500:
		return "Responder", "Incident Commander", 1500
	default:
		return "Incident Commander", "", 0
	}
}

func clone(profile Profile) Profile {
	if profile.Badges == nil {
		profile.Badges = []Badge{}
	}
	if profile.Awards == nil {
		profile.Awards = []Award{}
	}
	if profile.CompletedIDs == nil {
		profile.CompletedIDs = []string{}
	}
	if profile.BestScores == nil {
		profile.BestScores = map[string]int{}
	}
	profile.Badges = append([]Badge(nil), profile.Badges...)
	profile.Awards = append([]Award(nil), profile.Awards...)
	profile.CompletedIDs = append([]string(nil), profile.CompletedIDs...)
	profile.BestScores = maps.Clone(profile.BestScores)
	return profile
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func hasBadge(items []Badge, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
