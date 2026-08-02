//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// callWorkspaceAPI hits a workspace endpoint on the live krang instance
// and returns the status code plus the decoded envelope.
func callWorkspaceAPI(e *TestEnv, method, path string, body map[string]any) (int, map[string]any) {
	e.t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("encoding request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", e.hookPort, path), reader)
	if err != nil {
		e.t.Fatalf("building %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		e.t.Fatalf("decoding %s %s response: %v", method, path, err)
	}
	return resp.StatusCode, decoded
}

// workspaceSlots pulls the slot array out of a list response.
func workspaceSlots(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()

	raw, _ := body["slots"].([]any)
	slots := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		slot, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("slot entry is %T, want an object: %v", item, item)
		}
		slots = append(slots, slot)
	}
	return slots
}

func slotWithDir(slots []map[string]any, dir string) map[string]any {
	for _, slot := range slots {
		if slot["dir"] == dir {
			return slot
		}
	}
	return nil
}

// The whole workspace API, in the real binary, against real jj repos:
// add a slot, see it in the listing, remove it again. Every hop is
// exercised — HTTP handler → channel → Bubble Tea Update → tea.Cmd →
// jj → reply → HTTP response — plus the events rows and the DB
// provenance the operations leave behind.
func TestWorkspaceAddListRemoveRoundTripsThroughTUI(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "multi_repo", "jj", []string{"alpha", "beta"})

	env.CreateMultiRepoTask("wsapi", 1)
	workspaceDir := env.TaskWorkspaceDir("wsapi")
	canonical := filepath.Join(env.ReposDir(), "alpha")

	// --- list: the task's initial slot ---
	status, body := callWorkspaceAPI(env, http.MethodGet, "/api/workspace?task=wsapi", nil)
	if status != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (body %v)", status, body)
	}
	if body["task"] != "wsapi" {
		t.Errorf("task = %v, want wsapi", body["task"])
	}
	slots := workspaceSlots(t, body)
	if len(slots) != 1 {
		t.Fatalf("got %d slots, want the one initial working copy: %v", len(slots), slots)
	}
	initial := slots[0]
	if initial["dir"] != "alpha" || initial["repo"] != "alpha" || initial["slot"] != "" {
		t.Errorf("initial slot = %v, want the bare repo name and no label", initial)
	}
	if initial["recorded"] != true || initial["exists"] != true {
		t.Errorf("initial slot = %v, want recorded and present", initial)
	}
	if initial["canonical_repo_path"] != canonical {
		t.Errorf("canonical_repo_path = %v, want %q", initial["canonical_repo_path"], canonical)
	}
	if initial["vcs"] != "jj" || initial["vcs_name"] != "wsapi" {
		t.Errorf("initial slot = %v, want a jj workspace named after the task", initial)
	}

	// --- repos: what else is available ---
	status, body = callWorkspaceAPI(env, http.MethodGet, "/api/workspace/repos?task=wsapi", nil)
	if status != http.StatusOK {
		t.Fatalf("repos status = %d, want 200 (body %v)", status, body)
	}
	inTask := map[string]bool{}
	for _, item := range body["repos"].([]any) {
		repo := item.(map[string]any)
		inTask[repo["name"].(string)] = repo["in_task"].(bool)
	}
	if len(inTask) != 2 || !inTask["alpha"] || inTask["beta"] {
		t.Errorf("repos = %v, want alpha in the task and beta not", inTask)
	}

	// --- add: a second slot of a repo the task already holds ---
	status, body = callWorkspaceAPI(env, http.MethodPost, "/api/workspace/add", map[string]any{
		"task": "wsapi", "repo": "alpha", "label": "tests",
	})
	if status != http.StatusOK {
		t.Fatalf("add status = %d, want 200 (body %v)", status, body)
	}
	added, _ := body["slot"].(map[string]any)
	if added == nil || added["dir"] != "alpha--tests" {
		t.Fatalf("added slot = %v, want alpha--tests", added)
	}
	if added["vcs_name"] != "wsapi--alpha--tests" {
		t.Errorf("added vcs_name = %v, want wsapi--alpha--tests", added["vcs_name"])
	}
	env.WaitForEvent("wsapi", "workspace_add")

	slotDir := filepath.Join(workspaceDir, "alpha--tests")
	if _, err := os.Stat(slotDir); err != nil {
		t.Fatalf("the new slot is not on disk: %v", err)
	}

	// The slot came from the canonical repo, not from a sibling
	// working copy: .jj/repo points at the canonical store.
	pointer, err := os.ReadFile(filepath.Join(slotDir, ".jj", "repo"))
	if err != nil {
		t.Fatalf("reading .jj/repo: %v", err)
	}
	target := strings.TrimSpace(string(pointer))
	if !filepath.IsAbs(target) {
		target = filepath.Join(slotDir, ".jj", target)
	}
	gotStore, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolving %q: %v", target, err)
	}
	wantStore, err := filepath.EvalSymlinks(filepath.Join(canonical, ".jj", "repo"))
	if err != nil {
		t.Fatalf("resolving the canonical store: %v", err)
	}
	if gotStore != wantStore {
		t.Errorf(".jj/repo resolves to %q, want the canonical repo's store %q", gotStore, wantStore)
	}

	if listed := jjWorkspaceList(t, canonical); !strings.Contains(listed, "wsapi--alpha--tests") {
		t.Errorf("jj workspace list does not know the new slot:\n%s", listed)
	}

	// --- list again: both slots ---
	_, body = callWorkspaceAPI(env, http.MethodGet, "/api/workspace?task=wsapi", nil)
	slots = workspaceSlots(t, body)
	if len(slots) != 2 {
		t.Fatalf("got %d slots, want 2: %v", len(slots), slots)
	}
	labeled := slotWithDir(slots, "alpha--tests")
	if labeled == nil || labeled["slot"] != "tests" || labeled["recorded"] != true {
		t.Errorf("labeled slot = %v, want a recorded slot labeled tests", labeled)
	}

	// --- add without a label: refused, with a suggestion ---
	status, body = callWorkspaceAPI(env, http.MethodPost, "/api/workspace/add", map[string]any{
		"task": "wsapi", "repo": "alpha",
	})
	if status != http.StatusBadRequest {
		t.Errorf("unlabeled second add status = %d, want 400 (body %v)", status, body)
	}
	if body["reason"] != "label_required" {
		t.Errorf("reason = %v, want label_required", body["reason"])
	}
	if message, _ := body["message"].(string); !strings.Contains(message, `"2"`) {
		t.Errorf("message %q does not suggest a free label", message)
	}

	// --- remove: the labeled slot ---
	status, body = callWorkspaceAPI(env, http.MethodDelete, "/api/workspace/slot", map[string]any{
		"task": "wsapi", "dir": "alpha--tests",
	})
	if status != http.StatusOK {
		t.Fatalf("remove status = %d, want 200 (body %v)", status, body)
	}
	if body["repo_dropped"] == "true" {
		t.Error("removing one of two slots reported the repo as dropped")
	}
	env.WaitForEvent("wsapi", "workspace_remove_slot")

	if _, err := os.Stat(slotDir); !os.IsNotExist(err) {
		t.Errorf("the removed slot is still on disk: %v", err)
	}
	if listed := jjWorkspaceList(t, canonical); strings.Contains(listed, "wsapi--alpha--tests") {
		t.Errorf("jj still knows the forgotten workspace:\n%s", listed)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "alpha")); err != nil {
		t.Errorf("the surviving slot was destroyed: %v", err)
	}

	// The provenance row went with it, and only it.
	var dirs []string
	rows, err := env.db.Query(`
		SELECT wr.dir_name FROM workspace_repos wr
		JOIN tasks t ON t.id = wr.task_id
		WHERE t.name = 'wsapi' ORDER BY wr.dir_name`)
	if err != nil {
		t.Fatalf("querying workspace_repos: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) != 1 || dirs[0] != "alpha" {
		t.Errorf("workspace_repos rows = %v, want only the surviving initial slot", dirs)
	}

	// --- and the listing agrees ---
	_, body = callWorkspaceAPI(env, http.MethodGet, "/api/workspace?task=wsapi", nil)
	if slots = workspaceSlots(t, body); len(slots) != 1 || slots[0]["dir"] != "alpha" {
		t.Errorf("final listing = %v, want just the initial slot", slots)
	}
}

// AC: completing a task forgets every VCS identity it holds, slots
// included. The slot is added through the real API rather than seeded,
// because the identity cleanup has to forget is the one creation
// recorded — this is the only test where the whole chain runs: HTTP add
// → provenance row → keyboard completion → jj workspace forget.
func TestCompletingATaskForgetsEverySlotIdentity(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "multi_repo", "jj", []string{"alpha", "beta"})

	env.CreateMultiRepoTask("slotty", 2)
	workspaceDir := env.TaskWorkspaceDir("slotty")
	alphaSrc := filepath.Join(env.ReposDir(), "alpha")
	betaSrc := filepath.Join(env.ReposDir(), "beta")

	status, body := callWorkspaceAPI(env, http.MethodPost, "/api/workspace/add", map[string]any{
		"task": "slotty", "repo": "alpha", "label": "tests",
	})
	if status != http.StatusOK {
		t.Fatalf("add status = %d, want 200 (body %v)", status, body)
	}
	env.WaitForEvent("slotty", "workspace_add")

	// Three working copies, two of them checkouts of alpha under
	// identities that differ only because the slot has a label.
	for _, want := range []string{"slotty", "slotty--alpha--tests"} {
		if listed := jjWorkspaceList(t, alphaSrc); !strings.Contains(listed, want) {
			t.Fatalf("jj does not know workspace %q before completion:\n%s", want, listed)
		}
	}
	slotDir := filepath.Join(workspaceDir, "alpha--tests")
	if _, err := os.Stat(slotDir); err != nil {
		t.Fatalf("the slot is not on disk: %v", err)
	}

	// Complete from the keyboard: detail modal, c, confirm.
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("c")
	time.Sleep(500 * time.Millisecond)

	// The confirmation counts every working copy, the slot included.
	env.WaitForPaneContent("3 working copies")
	env.SendKeys("y")

	env.WaitForTaskState("slotty", "completed")
	env.WaitFor("workspace removed", 25*time.Second, func() bool {
		_, err := os.Stat(workspaceDir)
		return os.IsNotExist(err)
	})

	// The source repos are clean: no leftover jj workspace for either
	// the task's initial checkout or its slot. "slotty" is a prefix of
	// "slotty--alpha--tests", so one absence check covers both.
	if listed := jjWorkspaceList(t, alphaSrc); strings.Contains(listed, "slotty") {
		t.Errorf("alpha still lists a workspace of the completed task:\n%s", listed)
	}
	if listed := jjWorkspaceList(t, betaSrc); strings.Contains(listed, "slotty") {
		t.Errorf("beta still lists a workspace of the completed task:\n%s", listed)
	}

	// And the provenance went with it, so the name is free again.
	var rows int
	if err := env.db.QueryRow(`
		SELECT COUNT(*) FROM workspace_repos wr
		JOIN tasks t ON t.id = wr.task_id
		WHERE t.name = 'slotty'`).Scan(&rows); err != nil {
		t.Fatalf("counting workspace_repos: %v", err)
	}
	if rows != 0 {
		t.Errorf("%d provenance rows survived the completion, want 0", rows)
	}
}

// AC: a mutation an agent asked for over the API is visible to the
// human at the keyboard. The events row is asserted at unit level; what
// only the real binary can show is that the lines actually reach the
// screen, so this drives a genuine HTTP call and reads krang's pane.
func TestWorkspaceRequestSurfacesInTheTUI(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "multi_repo", "git", []string{"alpha", "beta"})

	env.CreateMultiRepoTask("wsvis", 1)

	// A labeled second checkout of a repo the task already holds: the
	// case the detail modal has to render as distinct from the initial
	// working copy.
	status, body := callWorkspaceAPI(env, http.MethodPost, "/api/workspace/add", map[string]any{
		"task": "wsvis", "repo": "alpha", "label": "tests",
	})
	if status != http.StatusOK {
		t.Fatalf("add status = %d, want 200 (body %v)", status, body)
	}
	env.WaitForEvent("wsvis", "workspace_add")

	// The debug log says the request started and how it turned out. Both
	// lines are the durable record a human scrolls back to; the status
	// line that spins between them is gone by the time we can look.
	env.WaitForPaneContent("workspace add task=wsvis started")
	env.WaitForPaneContent("workspace add task=wsvis ok")

	// And the detail modal lists the slot the agent added, under the repo
	// it is a checkout of and marked with the label that tells it apart
	// from the task's initial working copy of the same repo.
	env.SendKeys("Tab")
	env.WaitForPaneContent("Working copies (2):")
	env.WaitForPaneContent("alpha--tests")
	env.WaitForPaneContent("slot tests")
	env.SendKeys("Escape")
}

// An agent inside a workspace says where it is rather than what the
// task is called, and krang works out the rest.
func TestWorkspaceListResolvesTaskFromCwd(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "multi_repo", "git", []string{"alpha"})

	env.CreateMultiRepoTask("bycwd", 1)
	inside := filepath.Join(env.TaskWorkspaceDir("bycwd"), "alpha")

	status, body := callWorkspaceAPI(env, http.MethodGet, "/api/workspace?cwd="+inside, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, body)
	}
	if body["task"] != "bycwd" {
		t.Errorf("task = %v, want bycwd resolved from the cwd", body["task"])
	}
}

func TestWorkspaceRequestUnknownTaskIs404(t *testing.T) {
	env := NewTestEnv(t)

	status, body := callWorkspaceAPI(env, http.MethodGet, "/api/workspace?task=no-such-task", nil)

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

// A task with no workspace has nothing to list, and says so.
func TestWorkspaceListOnANonWorkspaceTaskIsRefused(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("plain-task")
	env.WaitForPaneContent("plain-task")

	status, body := callWorkspaceAPI(env, http.MethodGet, "/api/workspace?task=plain-task", nil)

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %v)", status, body)
	}
	if body["reason"] != "no_workspace" {
		t.Errorf("reason = %v, want no_workspace", body["reason"])
	}
}
