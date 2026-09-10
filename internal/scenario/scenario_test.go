package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadInstalledCatalog(t *testing.T) {
	scenarios, err := Load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != len(requiredIDs) {
		t.Fatalf("loaded %d scenarios, want %d", len(scenarios), len(requiredIDs))
	}
	seen := make(map[string]bool, len(scenarios))
	for _, definition := range scenarios {
		seen[definition.ID] = true
	}
	for id := range requiredIDs {
		if !seen[id] {
			t.Errorf("missing scenario %q", id)
		}
	}
}

func TestLoadRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(t *testing.T, root string)
		wantErr string
	}{
		{
			name: "unknown field",
			mutate: func(t *testing.T, root string) {
				appendToScenario(t, root, "cpu-saturation", "unexpected: true\n")
			},
			wantErr: "field unexpected not found",
		},
		{
			name: "schema version",
			mutate: func(t *testing.T, root string) {
				replaceInScenario(t, root, "cpu-saturation", "schema_version: 1", "schema_version: 2")
			},
			wantErr: "schema_version must be 1",
		},
		{
			name: "duration",
			mutate: func(t *testing.T, root string) {
				replaceInScenario(t, root, "cpu-saturation", "hold_duration: 15m", "hold_duration: 0s")
			},
			wantErr: "traffic hold_duration must be",
		},
		{
			name: "fault configuration",
			mutate: func(t *testing.T, root string) {
				replaceInScenario(t, root, "cpu-saturation", "work_units: 1000000", "probability: 0.5")
			},
			wantErr: "cpu_work requires target api and positive config.work_units only",
		},
		{
			name: "duplicate id",
			mutate: func(t *testing.T, root string) {
				source := scenarioPath(root, "cpu-saturation")
				data, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "scenarios", "duplicate.yaml")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "duplicate scenario id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := copyCatalog(t)
			test.mutate(t, root)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func copyCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for id := range requiredIDs {
		source := scenarioPath(filepath.Join("..", ".."), id)
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		path := scenarioPath(root, id)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func appendToScenario(t *testing.T, root, id, suffix string) {
	t.Helper()
	path := scenarioPath(root, id)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(suffix); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func replaceInScenario(t *testing.T, root, id, old, new string) {
	t.Helper()
	path := scenarioPath(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%q not found in %s", old, path)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0600); err != nil {
		t.Fatal(err)
	}
}

func scenarioPath(root, id string) string {
	return filepath.Join(root, "scenarios", id, "scenario.yaml")
}
