package progress

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStorePersistsNormalizedProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "progress.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Put("first-investigation", []int{4, 1, 4}, "API calls dependency")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record.CompletedSteps, []int{1, 4}) || record.Notes != "API calls dependency" || record.UpdatedAt.IsZero() {
		t.Fatalf("unexpected record: %#v", record)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("progress mode = %v, err = %v", info.Mode(), err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get("first-investigation")
	if !ok || !reflect.DeepEqual(got.CompletedSteps, []int{1, 4}) || got.Notes != "API calls dependency" {
		t.Fatalf("reopened record = %#v, present = %t", got, ok)
	}
}

func TestStoreRejectsInvalidInput(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]int{{-1}, {1000}, make([]int, 101)} {
		if _, err := store.Put("first-investigation", input, ""); err == nil {
			t.Fatalf("Put(%v) unexpectedly succeeded", input)
		}
	}
	if _, err := store.Put("../escape", []int{1}, ""); err == nil {
		t.Fatal("invalid ID unexpectedly succeeded")
	}
	if _, err := store.Put("first-investigation", []int{}, string(make([]byte, 16001))); err == nil {
		t.Fatal("oversized notes unexpectedly succeeded")
	}
}
