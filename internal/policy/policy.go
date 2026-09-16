package policy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
)

type Service struct {
	upstream *url.URL
	blocked  atomic.Bool
	client   *http.Client
}

func New(raw string) (*Service, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("UPSTREAM must be an HTTP origin")
	}
	return &Service{upstream: u, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`+"\n")
		return
	case "/rules":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, map[string]any{"rules": []map[string]any{{"chain": "egress", "source": "api", "destination": "dependency", "protocol": "tcp", "port": 8080, "action": map[bool]string{true: "drop", false: "accept"}[s.blocked.Load()]}}})
		return
	case "/iptables":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		action := "ACCEPT"
		if s.blocked.Load() {
			action = "DROP"
		}
		_, _ = fmt.Fprintf(w, "-A EGRESS -s api -d dependency -p tcp --dport 8080 -j %s\n", action)
		return
	case "/admin/state":
		if r.Method != http.MethodPut {
			methodNotAllowed(w)
			return
		}
		var input struct {
			Blocked bool `json:"blocked"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		s.blocked.Store(input.Blocked)
		writeJSON(w, map[string]bool{"blocked": input.Blocked})
		return
	}
	if r.Method != http.MethodGet || r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.blocked.Load() {
		http.Error(w, "egress policy denied", http.StatusGatewayTimeout)
		return
	}
	u := *s.upstream
	u.Path = r.URL.Path
	u.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		http.Error(w, "proxy request failed", http.StatusBadGateway)
		return
	}
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
