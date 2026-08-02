// Package workspaceclient talks to a running krang instance's workspace
// HTTP API from outside the TUI process.
//
// It exists so `krang workspace …` can be a thin set of cobra bindings:
// everything a subcommand does — finding the instance, calling the
// endpoint, turning the envelope into output and an exit code — lives
// here where it can be tested against an httptest server (and against a
// real hooks.Server) without a terminal, a tmux session, or a TUI.
//
// The transport is deliberately dumb. Every endpoint answers with the
// same hooks.WorkspaceResponse envelope, so there is one decode path and
// one exit-code mapping, and the CLI never has to interpret an HTTP
// status code — the envelope's reason and applied fields say everything
// a caller needs.
package workspaceclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dpetersen/krang/internal/hooks"
)

// StateFileEnv names the environment variable krang sets in every task
// window it launches. It holds the path to the instance's state file,
// which is how anything outside the TUI finds the loopback port.
const StateFileEnv = "KRANG_STATEFILE"

// TimeoutSlack is how much longer than the server's own deadline the
// client waits.
//
// The client must always be the more patient of the two. When the server
// times out it answers with a structured envelope that says whether the
// work may still land (reason "timeout", applied "unknown"); when the
// client times out first, all the caller gets is a dead socket and no
// idea what happened. Being late by a margin turns the ambiguous case
// into the documented one.
const TimeoutSlack = 10 * time.Second

// DefaultTimeout bounds a CLI call. It tracks the server's own default
// so the two can't drift apart silently.
const DefaultTimeout = hooks.DefaultWorkspaceTimeout + TimeoutSlack

// EnvironmentError means there is no krang instance to talk to: the
// state file is unset, unreadable, or doesn't name a port. Nothing was
// applied, and retrying without changing the environment won't help.
// Error() is the message the CLI prints verbatim, so it is written for
// the person (or agent) reading stderr.
type EnvironmentError struct {
	Message string
	Err     error
}

func (e *EnvironmentError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *EnvironmentError) Unwrap() error { return e.Err }

// TransportError means the instance the state file names did not
// answer. The request never reached a handler, so nothing was applied.
type TransportError struct {
	BaseURL       string
	StateFilePath string
	Err           error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("krang TUI not running, or no longer listening at %s: %v\n"+
		"%s says that is where it is. Start krang in the metarepo directory "+
		"(or check that this task's instance is still alive) and try again.",
		e.BaseURL, e.Err, e.StateFilePath)
}

func (e *TransportError) Unwrap() error { return e.Err }

// UnsupportedAPIError means krang answered, but the endpoint is not
// there. A router answers "no such route" before any handler runs, so
// this is a hard guarantee that nothing was applied — which is why it is
// not a ProtocolError. In practice it means the running instance predates
// the workspace API: krang was launched from an older build and has been
// up ever since.
type UnsupportedAPIError struct {
	HTTPStatus int
	Endpoint   string
}

func (e *UnsupportedAPIError) Error() string {
	return fmt.Sprintf("krang is running, but does not serve %s (HTTP %d).\n"+
		"The instance predates the workspace API. Quit krang and relaunch it from a build that has it; "+
		"nothing was applied.", e.Endpoint, e.HTTPStatus)
}

// ProtocolError means krang answered with something that is not a
// workspace envelope, and not a plain "no such endpoint" either. The
// port may have been recycled by an unrelated process. Whether the
// operation applied is unknowable from here, so the CLI treats it the
// way it treats every other unknown.
type ProtocolError struct {
	HTTPStatus int
	Body       []byte
	Err        error
}

func (e *ProtocolError) Error() string {
	body := strings.TrimSpace(string(e.Body))
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return fmt.Sprintf("krang answered HTTP %d with something that is not a workspace envelope (%v): %s",
		e.HTTPStatus, e.Err, body)
}

func (e *ProtocolError) Unwrap() error { return e.Err }

// StateFilePath reads the state file location out of the environment.
func StateFilePath(getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	path := strings.TrimSpace(getenv(StateFileEnv))
	if path == "" {
		return "", &EnvironmentError{Message: StateFileEnv + " is not set, so there is no krang instance to talk to.\n" +
			"This command only works from inside a krang task window — krang sets " + StateFileEnv + "\n" +
			"for the sessions it launches. Run it there, or point " + StateFileEnv + " at a running\n" +
			"instance's state file (~/.local/state/krang/instances/<encoded-cwd>/krang-state.json)."}
	}
	return path, nil
}

// BaseURL turns a state file into the loopback base URL of the instance
// that wrote it. It does not check that the instance is alive: that is
// what the first request finds out, and a liveness probe would only add
// a round trip and a second way to be wrong.
func BaseURL(stateFilePath string) (string, error) {
	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		return "", &EnvironmentError{
			Message: fmt.Sprintf("cannot read %s at %s.\nThe krang instance that wrote it has probably exited",
				StateFileEnv, stateFilePath),
			Err: err,
		}
	}

	var state struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return "", &EnvironmentError{
			Message: fmt.Sprintf("%s at %s is not valid JSON", StateFileEnv, stateFilePath),
			Err:     err,
		}
	}
	if state.Port <= 0 {
		return "", &EnvironmentError{Message: fmt.Sprintf(
			"%s at %s names no port, so krang is not listening.\nIt is probably a leftover from an instance that has exited.",
			StateFileEnv, stateFilePath)}
	}

	return fmt.Sprintf("http://127.0.0.1:%d", state.Port), nil
}

// Client calls one krang instance's workspace endpoints.
type Client struct {
	BaseURL string
	// StateFilePath is carried only so transport failures can say where
	// the address came from.
	StateFilePath string
	HTTP          *http.Client
}

// New builds a client against an already-known base URL. Tests use it
// directly; the CLI goes through NewFromEnv.
func New(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{BaseURL: strings.TrimSuffix(baseURL, "/"), HTTP: &http.Client{Timeout: timeout}}
}

// NewFromEnv finds the running instance the way every out-of-TUI caller
// does: the state file named by KRANG_STATEFILE.
func NewFromEnv(getenv func(string) string, timeout time.Duration) (*Client, error) {
	path, err := StateFilePath(getenv)
	if err != nil {
		return nil, err
	}
	baseURL, err := BaseURL(path)
	if err != nil {
		return nil, err
	}
	c := New(baseURL, timeout)
	c.StateFilePath = path
	return c, nil
}

// Params carries every parameter any workspace endpoint accepts. One
// struct rather than one per operation mirrors the server's single
// request body: "task"/"cwd" mean the same thing everywhere, and an
// operation simply ignores what it has no use for.
type Params struct {
	Task  string
	Cwd   string
	Repo  string
	Label string
	Dir   string
	Base  string
	Force bool
}

// wireBody is the JSON the mutating endpoints accept. omitempty keeps
// the request to what the caller actually asked for, so a server-side
// "you named neither a task nor a cwd" stays reachable.
type wireBody struct {
	Task  string `json:"task,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	Repo  string `json:"repo,omitempty"`
	Label string `json:"label,omitempty"`
	Dir   string `json:"dir,omitempty"`
	Base  string `json:"base,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// Result is one answered call: the decoded envelope plus the bytes it
// was decoded from, because --json prints exactly what krang said rather
// than a re-encoding of it.
type Result struct {
	HTTPStatus int
	Raw        []byte
	Envelope   hooks.WorkspaceResponse
}

// OK reports whether the envelope says the operation succeeded.
func (r Result) OK() bool { return r.Envelope.Status == hooks.WorkspaceStatusOK }

// Call issues one workspace operation.
func (c *Client) Call(ctx context.Context, op hooks.WorkspaceOp, params Params) (Result, error) {
	method, path, query, body, err := requestFor(op, params)
	if err != nil {
		return Result{}, err
	}

	endpoint := c.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return Result{}, fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return Result{}, fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, &TransportError{BaseURL: c.BaseURL, StateFilePath: c.StateFilePath, Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, &TransportError{BaseURL: c.BaseURL, StateFilePath: c.StateFilePath, Err: err}
	}

	result := Result{HTTPStatus: resp.StatusCode, Raw: raw}
	decodeErr := json.Unmarshal(raw, &result.Envelope)
	// An envelope always has a status. Anything without one didn't come
	// from a workspace handler, whether or not it happened to be JSON.
	if decodeErr != nil || result.Envelope.Status == "" {
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			return result, &UnsupportedAPIError{HTTPStatus: resp.StatusCode, Endpoint: method + " " + path}
		}
		if decodeErr == nil {
			decodeErr = fmt.Errorf("no %q field", "status")
		}
		return result, &ProtocolError{HTTPStatus: resp.StatusCode, Body: raw, Err: decodeErr}
	}
	if result.Envelope.Op == "" {
		result.Envelope.Op = op
	}
	return result, nil
}

// requestFor maps an operation onto the endpoint that serves it. The
// mapping lives in one place so a new operation can't be half-added.
func requestFor(op hooks.WorkspaceOp, params Params) (method, path string, query url.Values, body any, err error) {
	switch op {
	case hooks.WorkspaceOpList, hooks.WorkspaceOpRepos:
		path = "/api/workspace"
		if op == hooks.WorkspaceOpRepos {
			path = "/api/workspace/repos"
		}
		query = url.Values{}
		if params.Task != "" {
			query.Set("task", params.Task)
		}
		if params.Cwd != "" {
			query.Set("cwd", params.Cwd)
		}
		return http.MethodGet, path, query, nil, nil

	case hooks.WorkspaceOpAdd:
		return http.MethodPost, "/api/workspace/add", nil, wireBody{
			Task: params.Task, Cwd: params.Cwd, Repo: params.Repo,
			Label: params.Label, Base: params.Base,
		}, nil

	case hooks.WorkspaceOpRemoveSlot:
		return http.MethodDelete, "/api/workspace/slot", nil, wireBody{
			Task: params.Task, Cwd: params.Cwd, Dir: params.Dir,
			Repo: params.Repo, Label: params.Label, Force: params.Force,
		}, nil

	default:
		return "", "", nil, nil, fmt.Errorf("no endpoint for workspace operation %q", op)
	}
}
