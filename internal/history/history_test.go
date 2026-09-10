package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistsAndBoundsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		if err := store.Add(Record{RunID: string(rune('a' + i%26)), Scenario: "cpu-saturation", StartedAt: time.Now().UTC(), Passed: i%2 == 0}); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reloaded.List()); got != 100 {
		t.Fatalf("history length = %d, want 100", got)
	}
}
