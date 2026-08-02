//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// postWorkspacePing hits the scaffolding workspace endpoint on the live
// krang instance and returns the status code plus decoded body.
func postWorkspacePing(e *TestEnv, task, message string) (int, map[string]interface{}) {
	e.t.Helper()

	body, _ := json.Marshal(map[string]string{"task": task, "message": message})
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/api/workspace/ping", e.hookPort),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		e.t.Fatalf("posting workspace ping: %v", err)
	}
	defer resp.Body.Close()

	var decoded map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		e.t.Fatalf("decoding workspace ping response: %v", err)
	}
	return resp.StatusCode, decoded
}

// The whole request path, in the real binary: HTTP handler → channel →
// Bubble Tea Update → tea.Cmd → reply → HTTP response, with the events
// row the mutation leaves behind.
func TestWorkspacePingRoundTripsThroughTUI(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("ping-test")
	env.WaitForPaneContent("ping-test")

	status, body := postWorkspacePing(env, "ping-test", "hello")

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, body)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %v, want \"ok\"", body["status"])
	}
	data, _ := body["data"].(map[string]interface{})
	if data["echo"] != "hello" {
		t.Errorf("echo = %v, want %q", data["echo"], "hello")
	}
	if data["task"] != "ping-test" {
		t.Errorf("task = %v, want %q", data["task"], "ping-test")
	}

	env.WaitForEvent("ping-test", "workspace_ping")
}

func TestWorkspacePingUnknownTaskIs404(t *testing.T) {
	env := NewTestEnv(t)

	status, body := postWorkspacePing(env, "no-such-task", "")

	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %v)", status, body)
	}
	if body["reason"] != "unknown_task" {
		t.Errorf("reason = %v, want %q", body["reason"], "unknown_task")
	}
	if body["applied"] != "no" {
		t.Errorf("applied = %v, want %q", body["applied"], "no")
	}
}
