package hooks

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
)

const ListenAddr = "127.0.0.1:19283"

type EventCallback func(HookEvent)

type Server struct {
	httpServer *http.Server
	onEvent    EventCallback
}

func NewServer(onEvent EventCallback) *Server {
	s := &Server{onEvent: onEvent}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/event", s.handleEvent)
	mux.HandleFunc("GET /health", s.handleHealth)

	s.httpServer = &http.Server{
		Addr:    ListenAddr,
		Handler: mux,
	}
	return s
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", ListenAddr)
	if err != nil {
		return err
	}

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("hook server error: %v", err)
		}
	}()

	return nil
}

func (s *Server) Stop() {
	_ = s.httpServer.Shutdown(context.Background())
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
