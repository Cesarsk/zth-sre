package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"sre-lab/internal/scenario"
)

type scenarioView struct {
	ID                 string           `json:"id"`
	Title              string           `json:"title"`
	Difficulty         string           `json:"difficulty"`
	Description        string           `json:"description"`
	LearningObjectives []string         `json:"learningObjectives"`
	Hints              []string         `json:"hints"`
	TrafficProfile     string           `json:"trafficProfile"`
	GradingKind        string           `json:"gradingKind"`
	Available          bool             `json:"available"`
	Topology           string           `json:"topology"`
	Diagram            scenario.Diagram `json:"diagram"`
	Goal               string           `json:"goal"`
	SuccessCriteria    []string         `json:"successCriteria"`
}

func handleRuntime(w http.ResponseWriter, r *http.Request, runtime *runtimeManager) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	if r.Method == http.MethodGet && path == "scenarios" {
		views := make([]scenarioView, 0, len(runtime.scenarios))
		for _, item := range runtime.scenarios {
			views = append(views, scenarioView{ID: item.ID, Title: item.Title, Difficulty: item.Difficulty, Description: item.Description, LearningObjectives: item.LearningObjectives, Hints: item.Hints, TrafficProfile: item.Traffic.Profile, GradingKind: item.Grading.Kind, Available: true, Topology: item.Environment.Topology, Diagram: item.Diagram, Goal: item.Goal, SuccessCriteria: item.SuccessCriteria})
		}
		writeJSONResponse(w, http.StatusOK, views)
		return
	}
	if r.Method == http.MethodGet && path == "run" {
		writeJSONResponse(w, http.StatusOK, runtime.status())
		return
	}
	if r.Method == http.MethodGet && path == "runs" {
		writeJSONResponse(w, http.StatusOK, runtime.history.List())
		return
	}
	if path == "run/check" && r.Method == http.MethodPost {
		writeJSONResponse(w, http.StatusOK, runtime.check(r.Context()))
		return
	}
	if path == "run/reset" && r.Method == http.MethodPost {
		if err := runtime.reset(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]string{"state": "idle"})
		return
	}
	if path == "run/capacity" && r.Method == http.MethodPost {
		var input struct {
			ActiveBackends int `json:"activeBackends"`
		}
		if !decodeJSON(w, r, &input) || input.ActiveBackends < 1 {
			return
		}
		if err := runtime.setCapacity(r.Context(), input.ActiveBackends); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSONResponse(w, http.StatusOK, runtime.status())
		return
	}
	if path == "run/phase" && r.Method == http.MethodPost {
		var input struct {
			Phase string `json:"phase"`
		}
		if !decodeJSON(w, r, &input) || input.Phase == "" {
			return
		}
		if err := runtime.setPhase(r.Context(), input.Phase); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSONResponse(w, http.StatusOK, runtime.status())
		return
	}
	if path == "run/intervention" && r.Method == http.MethodPost {
		var input struct {
			Action string `json:"action"`
		}
		if !decodeJSON(w, r, &input) || input.Action == "" {
			return
		}
		if err := runtime.intervene(r.Context(), input.Action); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSONResponse(w, http.StatusOK, runtime.status())
		return
	}
	if path == "run/file-evidence" && r.Method == http.MethodPost {
		if !decodeJSON(w, r, &struct{}{}) {
			return
		}
		if err := runtime.markFileEvidence(); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSONResponse(w, http.StatusOK, runtime.status())
		return
	}
	if path == "run/diagnosis" && r.Method == http.MethodPost {
		var input struct {
			Diagnosis string `json:"diagnosis"`
			Evidence  string `json:"evidence"`
		}
		if !decodeJSON(w, r, &input) || input.Diagnosis == "" {
			return
		}
		if err := runtime.setDiagnosis(input.Diagnosis, input.Evidence); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSONResponse(w, http.StatusOK, runtime.status())
		return
	}
	if path == "run/alert" && r.Method == http.MethodPut {
		var input struct {
			Expression string `json:"expression"`
		}
		if !decodeJSON(w, r, &input) || input.Expression == "" {
			return
		}
		if err := runtime.setAlert(r.Context(), input.Expression); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSONResponse(w, http.StatusOK, runtime.status())
		return
	}
	if strings.HasPrefix(path, "scenarios/") && strings.HasSuffix(path, "/start") && r.Method == http.MethodPost {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "scenarios/"), "/start")
		started, err := runtime.start(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSONResponse(w, http.StatusCreated, started)
		return
	}
	if strings.HasPrefix(path, "run/hint") && r.Method == http.MethodPost {
		hints, err := runtime.revealHint()
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"revealedHints": hints})
		return
	}
	http.NotFound(w, r)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
