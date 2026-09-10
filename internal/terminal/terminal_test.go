package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDecodeMessage(t *testing.T) {
	for _, data := range []string{`{"type":"input","data":"ls\r"}`, `{"type":"resize","cols":120,"rows":30}`} {
		if _, err := decodeMessage(websocket.TextMessage, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	for _, data := range []string{`{}`, `{"type":"exec","data":"ls"}`, `{"type":"resize","cols":0,"rows":30}`, `{"type":"resize","cols":501,"rows":30}`, `{"type":"resize","cols":80,"rows":201}`, `{"type":"resize","cols":80.5,"rows":30}`, `{"type":"input","data":"ls","other":1}`, `{"type":"input"}{}`, `{"type":"input","cols":10}`, `{"type":"resize","cols":80,"rows":24,"data":"ls"}`} {
		if _, err := decodeMessage(websocket.TextMessage, []byte(data)); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
	data, _ := json.Marshal(message{Type: "input", Data: strings.Repeat("x", MaxInputBytes+1)})
	if _, err := decodeMessage(websocket.TextMessage, data); err == nil {
		t.Error("accepted oversized input")
	}
	if _, err := decodeMessage(websocket.BinaryMessage, []byte(`{"type":"input"}`)); err == nil {
		t.Error("accepted binary input")
	}
}

func testAgent(t *testing.T) (*Agent, *httptest.Server) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("real PTY tests require the non-root toolbox/container test user")
	}
	a, err := New([]string{"http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(a)
	t.Cleanup(func() { a.Close(); s.Close() })
	return a, s
}

func dial(t *testing.T, s *httptest.Server) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/terminal", http.Header{"Origin": []string{"http://localhost:8080"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func waitOutput(t *testing.T, c *websocket.Conn, needle string) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var out strings.Builder
	for !strings.Contains(out.String(), needle) {
		kind, data, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v; output %q", needle, err, out.String())
		}
		if kind != websocket.BinaryMessage {
			t.Fatalf("nonbinary output %d", kind)
		}
		out.Write(data)
	}
	return out.String()
}

func waitSessions(t *testing.T, a *Agent, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		active := a.active
		a.mu.Unlock()
		if active == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sessions did not reach %d", n)
}

func TestOriginRejection(t *testing.T) {
	_, s := testAgent(t)
	for _, origin := range []string{"", "null", "https://localhost:8080", "http://localhost:8080/", "http://evil.example"} {
		headers := http.Header{"X-Forwarded-Host": []string{"localhost:8080"}, "X-Forwarded-Proto": []string{"http"}}
		if origin != "" {
			headers.Set("Origin", origin)
		}
		c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/terminal", headers)
		if c != nil {
			c.Close()
		}
		if err == nil || resp == nil || resp.StatusCode != 403 {
			t.Fatalf("origin %q: %v %+v", origin, err, resp)
		}
		resp.Body.Close()
	}
}

func TestRealPTYResizeDisconnectAndCap(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux toolbox PTY integration")
	}
	a, s := testAgent(t)
	c := dial(t, s)
	// Disable echo so matches prove execution, not merely PTY input echo.
	if err := c.WriteJSON(message{Type: "input", Data: "stty -echo\nprintf '\\nREADY_%s\\n' OK\n"}); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, c, "READY_OK")
	if err := c.WriteJSON(message{Type: "resize", Cols: 120, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteJSON(message{Type: "input", Data: "stty size; printf 'UID=%s\\n' \"$(id -u)\"; printf 'PID=%s\\n' \"$$\"\n"}); err != nil {
		t.Fatal(err)
	}
	out := waitOutput(t, c, "PID=")
	for !strings.Contains(strings.SplitN(out, "PID=", 2)[1], "\n") {
		_, b, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		out += string(b)
	}
	if !strings.Contains(out, "30 120") || strings.Contains(out, "UID=0\r") {
		t.Fatalf("bad geometry/uid: %q", out)
	}
	pidText := strings.Fields(strings.SplitN(out, "PID=", 2)[1])[0]
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		t.Fatal(err)
	}
	second := dial(t, s)
	third, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/terminal", http.Header{"Origin": []string{"http://localhost:8080"}})
	if third != nil {
		third.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != 503 {
		t.Fatalf("session cap: %v %+v", err, resp)
	}
	resp.Body.Close()
	c.Close()
	waitSessions(t, a, 1)
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("shell %d not reaped: %v", pid, err)
	}
	second.Close()
	waitSessions(t, a, 0)
	replacement := dial(t, s)
	if err := replacement.WriteJSON(message{Type: "input", Data: "printf '\\nRECONNECTED_%s\\n' OK\n"}); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, replacement, "RECONNECTED_OK")
	a.Close()
	waitSessions(t, a, 0)
	replacement.SetReadDeadline(time.Now().Add(time.Second))
	for {
		if _, _, err := replacement.ReadMessage(); err != nil {
			break
		}
	}
}

func TestInvalidFrameClosesSession(t *testing.T) {
	a, s := testAgent(t)
	for _, data := range []string{`{"type":"resize","cols":9999,"rows":24}`, strings.Repeat("x", MaxMessageBytes+1)} {
		c := dial(t, s)
		if err := c.WriteMessage(websocket.TextMessage, []byte(data)); err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				break
			}
		}
		c.Close()
		waitSessions(t, a, 0)
	}
}

func TestRefusesRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-only startup guard test")
	}
	if _, err := New([]string{"http://localhost:8080"}); err == nil {
		t.Fatal("root agent accepted")
	}
}

func TestSessionTimeouts(t *testing.T) {
	for _, idle := range []bool{true, false} {
		name := "lifetime"
		if idle {
			name = "idle"
		}
		t.Run(name, func(t *testing.T) {
			if os.Geteuid() == 0 {
				t.Skip("requires non-root")
			}
			a, err := New([]string{"http://localhost:8080"})
			if err != nil {
				t.Fatal(err)
			}
			if idle {
				a.idleTimeout = 50 * time.Millisecond
			} else {
				a.lifetime = 100 * time.Millisecond
			}
			s := httptest.NewServer(a)
			defer s.Close()
			defer a.Close()
			c := dial(t, s)
			c.SetReadDeadline(time.Now().Add(3 * time.Second))
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					if e, ok := err.(interface{ Timeout() bool }); ok && e.Timeout() {
						t.Fatal("session timeout was not enforced")
					}
					break
				}
			}
			waitSessions(t, a, 0)
		})
	}
}

func TestDisconnectKillsChildGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process groups")
	}
	a, s := testAgent(t)
	c := dial(t, s)
	if err := c.WriteJSON(message{Type: "input", Data: "stty -echo\nprintf '\\nREADY_%s\\n' OK\n"}); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, c, "READY_OK")
	if err := c.WriteJSON(message{Type: "input", Data: "sleep 60 & child=$!; printf 'GROUP=%s CHILD=%s END\\n' \"$$\" \"$child\"\n"}); err != nil {
		t.Fatal(err)
	}
	out := waitOutput(t, c, " END")
	groupText := strings.Fields(strings.SplitN(out, "GROUP=", 2)[1])[0]
	childText := strings.Fields(strings.SplitN(out, "CHILD=", 2)[1])[0]
	group, err := strconv.Atoi(groupText)
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(childText)
	if err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(child)
	if err != nil || pgid != group {
		t.Fatalf("child not in shell group: %d != %d, %v", pgid, group, err)
	}
	c.Close()
	waitSessions(t, a, 0)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile("/proc/" + childText + "/stat")
		if os.IsNotExist(err) {
			return
		}
		// The container's init owns orphan reaping; a zombie is no longer running.
		if err == nil && strings.Contains(string(data), ") Z ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child still running after disconnect")
}
