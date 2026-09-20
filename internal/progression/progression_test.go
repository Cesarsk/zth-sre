package progression

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAwardsArePersistentAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "learner.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	badge := Badge{ID: "evidence-first", Title: "Evidence First"}
	profile, xp, err := store.Award("run-1", "cpu-saturation", 70, []Badge{badge})
	if err != nil || xp != 70 || profile.XP != 70 || profile.Rank != "Observer" || len(profile.Badges) != 1 {
		t.Fatalf("first award = %+v, %v, %v", profile, xp, err)
	}
	profile, xp, err = store.Award("run-2", "cpu-saturation", 90, []Badge{badge})
	if err != nil || xp != 20 || profile.XP != 90 || profile.BestScores["cpu-saturation"] != 90 {
		t.Fatalf("improved replay = %+v, %v, %v", profile, xp, err)
	}
	profile, xp, err = store.Award("run-3", "cpu-saturation", 80, []Badge{badge})
	if err != nil || xp != 0 || profile.XP != 90 || len(profile.Awards) != 3 {
		t.Fatalf("lower replay = %+v, %v, %v", profile, xp, err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Profile(); got.XP != 90 || got.BestScores["cpu-saturation"] != 90 || len(got.CompletedIDs) != 1 || got.CompletedIDs[0] != "cpu-saturation" {
		t.Fatalf("persisted profile = %+v", got)
	}
}

func TestFailedPersistenceDoesNotConsumeAward(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(dir, "learner.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blocker, "learner.json")
	if profile, xp, err := store.Award("run-1", "cpu-saturation", 100, nil); err == nil || xp != 0 || profile.XP != 0 {
		t.Fatalf("failed persistence = %+v, %d, %v", profile, xp, err)
	}
	store.path = filepath.Join(dir, "learner.json")
	if profile, xp, err := store.Award("run-1", "cpu-saturation", 100, nil); err != nil || xp != 100 || profile.XP != 100 {
		t.Fatalf("retry after persistence failure = %+v, %d, %v", profile, xp, err)
	}
}
