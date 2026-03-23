package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type stateFile struct {
	Port int `json:"port"`
}

type EventCallback func(HookEvent)

type Server struct {
	httpServer    *http.Server
	onEvent       EventCallback
	stateFilePath string
	listener      net.Listener
}

func NewServer(stateFilePath string, onEvent EventCallback) *Server {
	s := &Server{onEvent: onEvent, stateFilePath: stateFilePath}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/event", s.handleEvent)
	mux.HandleFunc("GET /health", s.handleHealth)

	s.httpServer = &http.Server{
		Handler: mux,
	}
	return s
}

func (s *Server) Start() error {
	if err := s.checkExistingInstance(); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("binding to dynamic port: %w", err)
	}
	s.listener = ln

	if err := s.writeStateFile(); err != nil {
		ln.Close()
		return fmt.Errorf("writing state file: %w", err)
	}

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("hook server error: %v", err)
		}
	}()

	return nil
}

func (s *Server) Port() int {
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *Server) Stop() {
	_ = s.httpServer.Shutdown(context.Background())
	_ = os.Remove(s.stateFilePath)
}

// checkExistingInstance reads an existing state file and checks whether
// a krang instance is already running on that port. Returns an error if
// a live instance is found; silently proceeds if the state file is
// missing or stale.
func (s *Server) checkExistingInstance() error {
	data, err := os.ReadFile(s.stateFilePath)
	if err != nil {
		return nil // No state file — proceed.
	}

	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil || state.Port == 0 {
		return nil // Corrupt state file — overwrite.
	}

	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", state.Port))
	if err != nil {
		return nil // Not responding — stale.
	}
	resp.Body.Close()

	return fmt.Errorf("krang is already running for this directory on port %d", state.Port)
}

func (s *Server) writeStateFile() error {
	dir := filepath.Dir(s.stateFilePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	state := stateFile{Port: s.Port()}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	return os.WriteFile(s.stateFilePath, data, 0o644)
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var event HookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	event.RawPayload = string(body)

	if s.onEvent != nil {
		s.onEvent(event)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
