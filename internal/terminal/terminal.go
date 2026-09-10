// Package terminal runs shells only in the isolated, non-root toolbox agent.
package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"sre-lab/internal/server"
)

const (
	MaxSessions     = 2
	MaxInputBytes   = 16 << 10
	MaxMessageBytes = 128 << 10
	MaxCols         = 500
	MaxRows         = 200
	IdleTimeout     = 15 * time.Minute
	Lifetime        = time.Hour
)

type Agent struct {
	origins     map[string]bool
	mu          sync.Mutex
	sessions    map[*websocket.Conn]context.CancelFunc
	active      int
	closed      bool
	wg          sync.WaitGroup
	idleTimeout time.Duration
	lifetime    time.Duration
}

func New(origins []string) (*Agent, error) {
	allowed, _, err := server.ParseOrigins(origins)
	if err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		return nil, fmt.Errorf("toolbox must run as non-root")
	}
	return &Agent{origins: allowed, sessions: make(map[*websocket.Conn]context.CancelFunc), idleTimeout: IdleTimeout, lifetime: Lifetime}, nil
}

// Close also stops hijacked WebSockets, which http.Server.Shutdown does not own.
func (a *Agent) Close() {
	a.mu.Lock()
	a.closed = true
	for conn, cancel := range a.sessions {
		cancel()
		conn.Close()
	}
	a.mu.Unlock()
	a.wg.Wait()
}

func (a *Agent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/healthz" && r.URL.Path != "/terminal" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/healthz" {
		io.WriteString(w, "ok\n")
		return
	}
	if len(r.Header.Values("Origin")) != 1 || !a.origins[r.Header.Get("Origin")] {
		http.Error(w, "origin forbidden", http.StatusForbidden)
		return
	}
	a.mu.Lock()
	if a.closed || a.active >= MaxSessions {
		a.mu.Unlock()
		http.Error(w, "terminal sessions unavailable", http.StatusServiceUnavailable)
		return
	}
	a.active++
	a.wg.Add(1)
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.active--; a.mu.Unlock(); a.wg.Done() }()
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool { return a.origins[r.Header.Get("Origin")] }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(r.Context(), a.lifetime)
	defer cancel()
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.sessions[conn] = cancel
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.sessions, conn); a.mu.Unlock() }()
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-i")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "shell unavailable"), time.Now().Add(time.Second))
		return
	}
	// Register a nonblocking descriptor with Go's poller so Close interrupts I/O.
	fd, err := syscall.Dup(int(master.Fd()))
	master.Close()
	if err == nil {
		err = syscall.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			syscall.Close(fd)
		}
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		return
	}
	master = os.NewFile(uintptr(fd), "terminal-pty")
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		master.Close()
		cmd.Wait()
	}()
	// Interactive startup enables job control even with bash +m. Queue this
	// before client input so ordinary children stay in our cleanup process group.
	master.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := master.Write([]byte("set +m\n")); err != nil {
		return
	}
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				lastActivity.Store(time.Now().UnixNano())
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		defer cancel()
		conn.SetReadLimit(MaxMessageBytes)
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			msg, err := decodeMessage(kind, data)
			if err != nil {
				conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid terminal message"), time.Now().Add(time.Second))
				return
			}
			lastActivity.Store(time.Now().UnixNano())
			if msg.Type == "resize" {
				if err := resize(master, msg.Cols, msg.Rows); err != nil {
					return
				}
			} else {
				master.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := master.Write([]byte(msg.Data)); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, lastActivity.Load())) >= a.idleTimeout {
					cancel()
					return
				}
			}
		}
	}()
	<-ctx.Done()
	conn.Close()
	master.Close()
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	workers.Wait()
}

func resize(master *os.File, cols, rows int) error {
	// File.Fd (used by pty.Setsize) switches a pollable file back to blocking I/O.
	// Use RawConn instead so disconnect can always interrupt the reader.
	raw, err := master.SyscallConn()
	if err != nil {
		return err
	}
	size := pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
	var ioctlErr syscall.Errno
	err = raw.Control(func(fd uintptr) {
		_, _, ioctlErr = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&size)))
	})
	if err != nil {
		return err
	}
	if ioctlErr != 0 {
		return ioctlErr
	}
	return nil
}

type message struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func decodeMessage(kind int, data []byte) (message, error) {
	var msg message
	if kind != websocket.TextMessage || len(data) > MaxMessageBytes {
		return msg, errors.New("expected bounded JSON text")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&msg); err != nil {
		return msg, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return msg, errors.New("trailing JSON")
	}
	switch msg.Type {
	case "input":
		if len(msg.Data) > MaxInputBytes || msg.Cols != 0 || msg.Rows != 0 {
			return msg, errors.New("invalid input")
		}
	case "resize":
		if msg.Cols < 1 || msg.Cols > MaxCols || msg.Rows < 1 || msg.Rows > MaxRows || msg.Data != "" {
			return msg, errors.New("invalid geometry")
		}
	default:
		return msg, errors.New("unknown message type")
	}
	return msg, nil
}
