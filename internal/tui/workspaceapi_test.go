package tui

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/task"
	"github.com/dpetersen/krang/internal/workspace"
)

// The workspace API tests run against real repositories. Slot creation
// and removal are almost entirely about what git and jj will accept, so
// asserting on the commands krang builds would test the wrong thing.

type wsFixture struct {
	t             *testing.T
	model         Model
	database      *sql.DB
	reposDir      string
	workspacesDir string
	workspaceDir  string
	taskStore     *db.TaskStore
	repoRows      *db.WorkspaceRepoStore
	repoSets      *workspace.RepoSets
}

// newWSFixture builds a model backed by a real metarepo: git repos in
// repos/, a multi_repo workspace directory for a live task named
// "alpha", and the stores the API reads and writes.
func newWSFixture(t *testing.T, repoNames ...string) *wsFixture {
	t.Helper()
	return newWSFixtureWith(t, workspace.StrategyMultiRepo, "git", repoNames...)
}

func newWSFixtureWith(t *testing.T, strategy workspace.WorkspaceStrategy, vcs string, repoNames ...string) *wsFixture {
	t.Helper()

	if vcs == "jj" {
		if _, err := exec.LookPath("jj"); err != nil {
			t.Skip("jj not installed")
		}
	}

	metarepoDir := t.TempDir()
	t.Setenv("KRANG_DB", filepath.Join(metarepoDir, "krang.db"))

	database, err := db.Open(metarepoDir)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	reposDir := filepath.Join(metarepoDir, "repos")
	workspacesDir := filepath.Join(metarepoDir, "workspaces")
	for _, dir := range []string{reposDir, workspacesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	for _, name := range repoNames {
		switch vcs {
		case "jj":
			initJJRepo(t, filepath.Join(reposDir, name))
		default:
			initGitRepo(t, filepath.Join(reposDir, name))
		}
	}

	repoSets := &workspace.RepoSets{
		MetarepoDir:       metarepoDir,
		WorkspaceStrategy: strategy,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	taskStore := db.NewTaskStore(database)
	eventStore := db.NewEventStore(database)
	repoRows := db.NewWorkspaceRepoStore(database)

	workspaceDir := filepath.Join(workspacesDir, "alpha")
	if strategy == workspace.StrategyMultiRepo {
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			t.Fatalf("creating workspace dir: %v", err)
		}
	}

	if err := taskStore.Create(&db.Task{
		ID: "01ALPHA", Name: "alpha", State: db.StateActive, Attention: db.AttentionOK,
		Cwd: workspaceDir, WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	manager := task.NewManager(
		taskStore, eventStore, repoRows,
		"krang-tui-test", "krang-tui-test-parked",
		nil, "", "", metarepoDir, repoSets,
	)

	return &wsFixture{
		t: t,
		model: Model{
			manager:        manager,
			taskStore:      taskStore,
			eventStore:     eventStore,
			workspaceRepos: repoRows,
			repoSets:       repoSets,
		},
		database:      database,
		reposDir:      reposDir,
		workspacesDir: workspacesDir,
		workspaceDir:  workspaceDir,
		taskStore:     taskStore,
		repoRows:      repoRows,
		repoSets:      repoSets,
	}
}

// run drives one request the way Bubble Tea would and returns the reply
// along with the model after the completion message landed.
func (f *wsFixture) run(req hooks.WorkspaceRequest) hooks.WorkspaceResponse {
	f.t.Helper()

	model, cmd := f.model.Update(workspaceRequestMsg{Request: req})
	m := model.(Model)
	for _, msg := range runCmd(f.t, cmd) {
		if done, ok := msg.(workspaceRequestDoneMsg); ok {
			model, _ = m.Update(done)
			m = model.(Model)
		}
	}
	f.model = m
	return replyOrFail(f.t, req)
}

// add is the happy-path helper: fail the test if the slot wasn't made.
func (f *wsFixture) add(repo, label string) hooks.SlotInfo {
	f.t.Helper()

	resp := f.run(addRequest(repo, label))
	if resp.Status != hooks.WorkspaceStatusOK {
		f.t.Fatalf("add %s/%s: %+v", repo, label, resp)
	}
	return *resp.Slot
}

func addRequest(repo, label string) hooks.WorkspaceRequest {
	req := hooks.NewWorkspaceRequest(hooks.WorkspaceOpAdd, "alpha")
	req.Repo = repo
	req.Label = label
	return req
}

func listRequest() hooks.WorkspaceRequest {
	return hooks.NewWorkspaceRequest(hooks.WorkspaceOpList, "alpha")
}

func removeRequest(dir string, force bool) hooks.WorkspaceRequest {
	req := hooks.NewWorkspaceRequest(hooks.WorkspaceOpRemoveSlot, "alpha")
	req.Dir = dir
	req.Force = force
	return req
}

// initGitRepo makes a repo with a real (bare, local) origin it has
// pushed to. The remote is not decoration: HasUnpushedCommits asks git
// for commits unreachable from any remote ref, so in a repo with no
// remote at all every commit is unpushed and every removal would be
// refused. A krang-managed repo is always a clone, so the remote is
// what makes the fixture resemble one.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	mustRun(t, dir, "git", "init", "--bare", origin)
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"commit", "--allow-empty", "-m", "init"},
		{"remote", "add", "origin", origin},
		{"push", "-u", "origin", "HEAD"},
	} {
		mustRun(t, dir, "git", args...)
	}
}

func initJJRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	mustRun(t, dir, "jj", "git", "init", ".")
	mustRun(t, dir, "jj", "describe", "-m", "init")
	mustRun(t, dir, "jj", "new")
}

func mustRun(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s: %v: %s", name, args, dir, err, output)
	}
	return string(output)
}

func runOut(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, _ := cmd.CombinedOutput()
	return string(output)
}

// resolvePath follows symlinks so /var and /private/var comparisons on
// macOS don't fail a test that is actually about repository layout.
func resolvePath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %q: %v", path, err)
	}
	return resolved
}

func slotByDir(slots []hooks.SlotInfo, dir string) *hooks.SlotInfo {
	for i := range slots {
		if slots[i].Dir == dir {
			return &slots[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------
// ITEM 1 — GET /api/workspace
// ---------------------------------------------------------------

// AC: the listing joins the workspace_repos rows with what is on disk,
// and reports both kinds. A directory krang recorded is authoritative;
// one it merely found is reported with recorded:false rather than
// hidden, so the listing agrees with ls.
func TestWorkspaceListJoinsRecordedRowsWithOnDiskDirectories(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo")
	f.add("alpha-repo", "")
	f.add("alpha-repo", "tests")

	// A checkout nobody told krang about.
	initGitRepo(t, filepath.Join(f.workspaceDir, "beta-repo"))

	resp := f.run(listRequest())
	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("list: %+v", resp)
	}
	if len(resp.Slots) != 3 {
		t.Fatalf("got %d slots, want 3: %+v", len(resp.Slots), resp.Slots)
	}
	if resp.Task != "alpha" {
		t.Errorf("task = %q, want alpha echoed back", resp.Task)
	}

	initial := slotByDir(resp.Slots, "alpha-repo")
	if initial == nil {
		t.Fatalf("no slot for the initial working copy: %+v", resp.Slots)
	}
	if !initial.Recorded || !initial.Exists {
		t.Errorf("initial slot = %+v, want recorded and present", *initial)
	}
	if initial.Slot != "" {
		t.Errorf("initial slot label = %q, want empty", initial.Slot)
	}
	if initial.VCSName != "alpha" {
		t.Errorf("initial vcs_name = %q, want the task name (pre-slot naming)", initial.VCSName)
	}
	if initial.CanonicalRepoPath != filepath.Join(f.reposDir, "alpha-repo") {
		t.Errorf("canonical_repo_path = %q, want %q",
			initial.CanonicalRepoPath, filepath.Join(f.reposDir, "alpha-repo"))
	}

	labeled := slotByDir(resp.Slots, "alpha-repo--tests")
	if labeled == nil {
		t.Fatalf("no slot for the labeled working copy: %+v", resp.Slots)
	}
	if labeled.Repo != "alpha-repo" || labeled.Slot != "tests" {
		t.Errorf("labeled slot = %+v, want repo alpha-repo labeled tests", *labeled)
	}
	if labeled.VCSName != "alpha--alpha-repo--tests" {
		t.Errorf("labeled vcs_name = %q, want alpha--alpha-repo--tests", labeled.VCSName)
	}

	unrecorded := slotByDir(resp.Slots, "beta-repo")
	if unrecorded == nil {
		t.Fatalf("the hand-made checkout was not listed: %+v", resp.Slots)
	}
	if unrecorded.Recorded {
		t.Error("a directory with no workspace_repos row was reported as recorded")
	}
	if !unrecorded.Exists {
		t.Error("a directory that is right there was reported as not existing")
	}
}

// AC: works for a task holding nothing but initial slots — the shape a
// workspace has right after creation.
func TestWorkspaceListWorksWithOnlyInitialSlots(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo")
	f.add("alpha-repo", "")
	f.add("beta-repo", "")

	resp := f.run(listRequest())

	if len(resp.Slots) != 2 {
		t.Fatalf("got %d slots, want 2: %+v", len(resp.Slots), resp.Slots)
	}
	for _, slot := range resp.Slots {
		if slot.Slot != "" {
			t.Errorf("slot %q has label %q, want empty for an initial working copy", slot.Dir, slot.Slot)
		}
		if slot.Dir != slot.Repo {
			t.Errorf("initial slot dir = %q, want the bare repo name %q", slot.Dir, slot.Repo)
		}
		if !slot.Recorded || !slot.Exists || slot.VCS != "git" {
			t.Errorf("slot = %+v, want a recorded, present git checkout", slot)
		}
	}
}

// A recorded slot whose directory somebody deleted behind krang's back
// is still listed, with exists:false. Dropping it would hide the very
// inconsistency the caller needs to see.
func TestWorkspaceListReportsRecordedSlotMissingFromDisk(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")

	if err := os.RemoveAll(filepath.Join(f.workspaceDir, "alpha-repo")); err != nil {
		t.Fatalf("removing the slot directory: %v", err)
	}

	resp := f.run(listRequest())
	if len(resp.Slots) != 1 {
		t.Fatalf("got %d slots, want the recorded row to survive: %+v", len(resp.Slots), resp.Slots)
	}
	if resp.Slots[0].Exists {
		t.Error("a deleted directory was reported as existing")
	}
	if !resp.Slots[0].Recorded {
		t.Error("the row is still there; the slot should read as recorded")
	}
}

// AC: the caller may identify the task by its own working directory.
func TestWorkspaceRequestResolvesTaskByCwdInsideWorkspace(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")

	req := hooks.NewWorkspaceRequest(hooks.WorkspaceOpList, "")
	req.Cwd = filepath.Join(f.workspaceDir, "alpha-repo", "some", "subdir")

	resp := f.run(req)
	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("list by cwd: %+v", resp)
	}
	if resp.Task != "alpha" {
		t.Errorf("task = %q, want alpha resolved from the cwd", resp.Task)
	}
}

// A sibling directory whose name merely starts the same way is not
// inside the workspace, and must not resolve to it.
func TestWorkspaceRequestCwdMatchIsPathBoundaryAware(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")

	req := hooks.NewWorkspaceRequest(hooks.WorkspaceOpList, "")
	req.Cwd = f.workspaceDir + "-other"

	resp := f.run(req)
	if resp.Reason != hooks.ReasonUnknownTask {
		t.Errorf("reason = %q, want %q for a sibling directory", resp.Reason, hooks.ReasonUnknownTask)
	}
}

// Read-only operations must not wait on the human. A listing arriving
// while a modal is open runs anyway, and never touches the in-flight
// slot that the mutation queue hands out.
func TestWorkspaceListBypassesTheMutationQueue(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")

	f.model.mode = ModeWorkspaceProgress
	f.model.wsProgress = &wsProgressState{Title: "Creating workspace \"beta\""}

	req := listRequest()
	model, cmd := f.model.Update(workspaceRequestMsg{Request: req})
	m := model.(Model)

	if len(m.workspaceQueue) != 0 {
		t.Errorf("queue length = %d, want a read-only request not to queue", len(m.workspaceQueue))
	}
	if m.workspaceRequest != nil {
		t.Error("a read-only request took the in-flight mutation slot")
	}

	msgs := runCmd(t, cmd)
	resp := replyOrFail(t, req)
	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("list during a modal: %+v", resp)
	}
	for _, msg := range msgs {
		if done, ok := msg.(workspaceRequestDoneMsg); ok && !done.ReadOnly {
			t.Error("the completion message did not mark itself read-only; it would free somebody else's slot")
		}
	}
}

// Finishing a read must not release the slot a mutation is holding.
func TestReadOnlyCompletionDoesNotFreeAnInFlightMutation(t *testing.T) {
	held := hooks.NewWorkspaceRequest(hooks.WorkspaceOpAdd, "alpha")
	m := Model{workspaceRequest: &held}

	model, _ := m.Update(workspaceRequestDoneMsg{Op: hooks.WorkspaceOpList, TaskName: "alpha", ReadOnly: true})

	if model.(Model).workspaceRequest == nil {
		t.Error("a finished listing freed the in-flight slot of a running mutation")
	}
}

// ---------------------------------------------------------------
// ITEM 2 — POST /api/workspace/add
// ---------------------------------------------------------------

// AC: a repo the task doesn't hold but the registry does lands in the
// repo's first slot, whose directory is the bare repo name.
func TestWorkspaceAddGivesANewRepoItsFirstSlot(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")

	slot := f.add("alpha-repo", "")

	if slot.Dir != "alpha-repo" || slot.Slot != "" {
		t.Errorf("slot = %+v, want the bare repo name and no label", slot)
	}
	if _, err := os.Stat(filepath.Join(f.workspaceDir, "alpha-repo", ".git")); err != nil {
		t.Errorf("no working copy on disk: %v", err)
	}

	rows, _ := f.repoRows.ListByTask("01ALPHA")
	if len(rows) != 1 || rows[0].DirName != "alpha-repo" || rows[0].VCSName != "alpha" {
		t.Errorf("rows = %+v, want one row recording the initial slot", rows)
	}
}

// AC: a repo the task already holds needs a label, and the refusal
// suggests one that will actually work.
func TestWorkspaceAddRequiresALabelForASecondSlotAndSuggestsOne(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")

	resp := f.run(addRequest("alpha-repo", ""))

	if resp.Reason != hooks.ReasonLabelRequired {
		t.Fatalf("reason = %q, want %q: %+v", resp.Reason, hooks.ReasonLabelRequired, resp)
	}
	if resp.Applied != hooks.AppliedNo {
		t.Errorf("applied = %q, want %q", resp.Applied, hooks.AppliedNo)
	}
	if !strings.Contains(resp.Message, `"2"`) {
		t.Errorf("message %q does not suggest a free label", resp.Message)
	}

	// The suggestion has to be usable as-is.
	slot := f.add("alpha-repo", "2")
	if slot.Dir != "alpha-repo--2" {
		t.Errorf("slot dir = %q, want the suggested label to produce alpha-repo--2", slot.Dir)
	}
}

// AC: slots always come from the canonical repo, never from a sibling
// working copy. The git worktree's .git pointer names the source repo,
// so read it.
func TestWorkspaceAddCreatesGitSlotFromTheCanonicalRepo(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	slot := f.add("alpha-repo", "tests")

	if slot.Dir != "alpha-repo--tests" || slot.VCSName != "alpha--alpha-repo--tests" {
		t.Errorf("slot = %+v, want the labeled directory and VCS identity", slot)
	}

	pointer, err := os.ReadFile(filepath.Join(f.workspaceDir, "alpha-repo--tests", ".git"))
	if err != nil {
		t.Fatalf("reading the worktree's .git pointer: %v", err)
	}
	gitdir := strings.TrimPrefix(strings.TrimSpace(string(pointer)), "gitdir: ")
	canonical := resolvePath(t, filepath.Join(f.reposDir, "alpha-repo", ".git"))
	if !strings.HasPrefix(resolvePath(t, gitdir), canonical) {
		t.Errorf(".git pointer = %q, want it under the canonical repo %q", gitdir, canonical)
	}
}

// AC: the same, for jj — .jj/repo points at the canonical repo's store.
func TestWorkspaceAddCreatesJJSlotFromTheCanonicalRepo(t *testing.T) {
	f := newWSFixtureWith(t, workspace.StrategyMultiRepo, "jj", "alpha-repo")
	f.add("alpha-repo", "")
	slot := f.add("alpha-repo", "tests")

	if slot.VCS != "jj" {
		t.Fatalf("slot vcs = %q, want jj", slot.VCS)
	}

	slotDir := filepath.Join(f.workspaceDir, "alpha-repo--tests")
	pointer, err := os.ReadFile(filepath.Join(slotDir, ".jj", "repo"))
	if err != nil {
		t.Fatalf("reading .jj/repo: %v", err)
	}

	// The pointer is a path relative to the slot's own .jj directory.
	target := strings.TrimSpace(string(pointer))
	if !filepath.IsAbs(target) {
		target = filepath.Join(slotDir, ".jj", target)
	}
	resolved := resolvePath(t, target)
	want := resolvePath(t, filepath.Join(f.reposDir, "alpha-repo", ".jj", "repo"))
	if resolved != want {
		t.Errorf(".jj/repo resolves to %q, want the canonical repo's store %q", resolved, want)
	}

	// And the workspace was registered under the recorded identity.
	if listed := runOut(t, filepath.Join(f.reposDir, "alpha-repo"), "jj", "workspace", "list"); !strings.Contains(listed, slot.VCSName) {
		t.Errorf("jj workspace list = %q, want it to name %q", listed, slot.VCSName)
	}
}

// AC: --base reaches the VCS and is recorded, so the listing can say
// where a slot started even after the bookmark has moved.
func TestWorkspaceAddPlumbsAndRecordsAnExplicitBase(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	repoDir := filepath.Join(f.reposDir, "alpha-repo")

	first := strings.TrimSpace(runOut(t, repoDir, "git", "rev-parse", "HEAD"))
	mustRun(t, repoDir, "git", "commit", "--allow-empty", "-m", "second")

	req := addRequest("alpha-repo", "")
	req.Base = first
	resp := f.run(req)
	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("add with a base: %+v", resp)
	}
	if resp.Slot.Base != first {
		t.Errorf("slot base = %q, want the requested %q", resp.Slot.Base, first)
	}

	slotHead := strings.TrimSpace(runOut(t, filepath.Join(f.workspaceDir, "alpha-repo"), "git", "rev-parse", "HEAD"))
	if slotHead != first {
		t.Errorf("slot HEAD = %s, want the requested base %s", slotHead, first)
	}

	rows, _ := f.repoRows.ListByTask("01ALPHA")
	if len(rows) != 1 || rows[0].BaseRevision != first {
		t.Errorf("rows = %+v, want base_revision %q recorded", rows, first)
	}

	if listed := f.run(listRequest()); listed.Slots[0].Base != first {
		t.Errorf("listed base = %q, want %q", listed.Slots[0].Base, first)
	}
}

// AC: the per-task slot cap is enforced, and the refusal names the
// slots that could be removed to make room.
func TestWorkspaceAddEnforcesTheSlotCap(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo")

	f.add("alpha-repo", "")
	f.add("beta-repo", "")
	f.add("alpha-repo", "two")
	f.add("alpha-repo", "three")

	resp := f.run(addRequest("beta-repo", "spare"))

	if resp.Reason != hooks.ReasonSlotLimit {
		t.Fatalf("reason = %q, want %q: %+v", resp.Reason, hooks.ReasonSlotLimit, resp)
	}
	for _, dir := range []string{"alpha-repo", "beta-repo", "alpha-repo--two", "alpha-repo--three"} {
		if !strings.Contains(resp.Message, dir) {
			t.Errorf("message %q does not name removable slot %q", resp.Message, dir)
		}
	}
	if _, err := os.Stat(filepath.Join(f.workspaceDir, "beta-repo--spare")); err == nil {
		t.Error("the refused slot was created anyway")
	}
	if workspace.MaxSlotsPerTask != 4 {
		t.Errorf("MaxSlotsPerTask = %d; this test assumes 4", workspace.MaxSlotsPerTask)
	}
}

// AC: a workspace two tasks share has no owner for its slots, so adding
// to it is refused rather than recorded against an arbitrary one.
func TestWorkspaceAddRefusesASharedWorkspace(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")

	if err := f.taskStore.Create(&db.Task{
		ID: "01SHARE", Name: "alpha-fork", State: db.StateActive, Attention: db.AttentionOK,
		Cwd: f.workspaceDir, WorkspaceDir: f.workspaceDir,
	}); err != nil {
		t.Fatalf("creating the sharing task: %v", err)
	}

	resp := f.run(addRequest("alpha-repo", ""))

	if resp.Reason != hooks.ReasonSharedWorkspace {
		t.Fatalf("reason = %q, want %q: %+v", resp.Reason, hooks.ReasonSharedWorkspace, resp)
	}
	if !strings.Contains(resp.Message, "alpha-fork") {
		t.Errorf("message %q does not name the task it is shared with", resp.Message)
	}
	if entries, _ := os.ReadDir(f.workspaceDir); len(entries) != 0 {
		t.Errorf("workspace dir = %v, want nothing created", entries)
	}
}

func TestWorkspaceAddRefusesARepoTheRegistryDoesNotHave(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")

	resp := f.run(addRequest("nonexistent", ""))

	if resp.Reason != hooks.ReasonUnknownRepo {
		t.Fatalf("reason = %q, want %q: %+v", resp.Reason, hooks.ReasonUnknownRepo, resp)
	}
	if !strings.Contains(resp.Message, "/api/workspace/repos") {
		t.Errorf("message %q does not point at the discovery endpoint", resp.Message)
	}
}

// ---------------------------------------------------------------
// ITEM 3 — GET /api/workspace/repos
// ---------------------------------------------------------------

// AC: every directory in the repos dir is listed, with the sets it
// belongs to and whether the task already holds it.
func TestWorkspaceReposListsRegistryWithSetsAndMembership(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo", "gamma-repo")
	f.repoSets.Sets = map[string][]string{
		"backend": {"alpha-repo", "beta-repo"},
		"all":     {"alpha-repo", "beta-repo", "gamma-repo"},
	}
	f.add("alpha-repo", "")

	resp := f.run(hooks.NewWorkspaceRequest(hooks.WorkspaceOpRepos, "alpha"))
	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("repos: %+v", resp)
	}
	if len(resp.Repos) != 3 {
		t.Fatalf("got %d repos, want 3: %+v", len(resp.Repos), resp.Repos)
	}

	byName := map[string]hooks.RepoInfo{}
	for _, repo := range resp.Repos {
		byName[repo.Name] = repo
	}

	if !byName["alpha-repo"].InTask {
		t.Error("alpha-repo is in the workspace but in_task is false")
	}
	if byName["beta-repo"].InTask || byName["gamma-repo"].InTask {
		t.Error("a repo the task does not hold was reported as in_task")
	}
	if got := strings.Join(byName["alpha-repo"].Sets, ","); got != "all,backend" {
		t.Errorf("alpha-repo sets = %q, want %q (sorted)", got, "all,backend")
	}
	if got := strings.Join(byName["gamma-repo"].Sets, ","); got != "all" {
		t.Errorf("gamma-repo sets = %q, want %q", got, "all")
	}
}

// A second slot of a repo must not make the repo look like two repos,
// and a hand-made checkout still counts as "in the task".
func TestWorkspaceReposCountsExtraSlotsAndScannedDirsAsInTask(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo")
	f.add("alpha-repo", "")
	f.add("alpha-repo", "tests")
	initGitRepo(t, filepath.Join(f.workspaceDir, "beta-repo"))

	resp := f.run(hooks.NewWorkspaceRequest(hooks.WorkspaceOpRepos, "alpha"))

	if len(resp.Repos) != 2 {
		t.Fatalf("got %d repos, want the registry's 2: %+v", len(resp.Repos), resp.Repos)
	}
	for _, repo := range resp.Repos {
		if !repo.InTask {
			t.Errorf("repo %q should read as in_task", repo.Name)
		}
	}
}

// ---------------------------------------------------------------
// ITEM 4 — DELETE /api/workspace/slot
// ---------------------------------------------------------------

// AC: removal forgets the recorded VCS identity — not one guessed from
// the directory name — removes the directory, and drops the row. With
// two slots of one repo, guessing would forget the wrong worktree.
func TestWorkspaceRemoveSlotForgetsTheRecordedIdentity(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	f.add("alpha-repo", "tests")

	repoDir := filepath.Join(f.reposDir, "alpha-repo")
	resp := f.run(removeRequest("alpha-repo--tests", false))

	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("remove: %+v", resp)
	}
	if resp.Slot == nil || resp.Slot.Dir != "alpha-repo--tests" || resp.Slot.Exists {
		t.Errorf("slot = %+v, want the removed slot reported as gone", resp.Slot)
	}
	if resp.Data["repo_dropped"] != "false" {
		t.Errorf("repo_dropped = %q, want false: the initial slot is still there", resp.Data["repo_dropped"])
	}

	if _, err := os.Stat(filepath.Join(f.workspaceDir, "alpha-repo--tests")); !os.IsNotExist(err) {
		t.Errorf("the slot directory survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.workspaceDir, "alpha-repo")); err != nil {
		t.Errorf("the other slot of the same repo was destroyed: %v", err)
	}

	worktrees := runOut(t, repoDir, "git", "worktree", "list")
	if strings.Contains(worktrees, "alpha-repo--tests") {
		t.Errorf("git worktree list still holds the removed slot:\n%s", worktrees)
	}
	if !strings.Contains(worktrees, filepath.Join("alpha", "alpha-repo")) {
		t.Errorf("git worktree list lost the surviving slot:\n%s", worktrees)
	}

	rows, _ := f.repoRows.ListByTask("01ALPHA")
	if len(rows) != 1 || rows[0].DirName != "alpha-repo" {
		t.Errorf("rows = %+v, want only the surviving slot's row", rows)
	}
}

// AC: work that exists nowhere else stops the removal, and the refusal
// names what it is protecting.
func TestWorkspaceRemoveSlotRefusesToDestroyUncommittedWork(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	slotDir := filepath.Join(f.workspaceDir, "alpha-repo")

	if err := os.WriteFile(filepath.Join(slotDir, "NOTES.md"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("writing a scratch file: %v", err)
	}

	resp := f.run(removeRequest("alpha-repo", false))

	if resp.Reason != hooks.ReasonUnsavedWork {
		t.Fatalf("reason = %q, want %q: %+v", resp.Reason, hooks.ReasonUnsavedWork, resp)
	}
	if resp.Applied != hooks.AppliedNo {
		t.Errorf("applied = %q, want %q", resp.Applied, hooks.AppliedNo)
	}
	if len(resp.Blockers) != 1 || resp.Blockers[0].Kind != hooks.BlockerUncommittedChanges {
		t.Errorf("blockers = %+v, want the uncommitted-changes blocker", resp.Blockers)
	}
	if resp.Blockers[0].Dir != "alpha-repo" {
		t.Errorf("blocker dir = %q, want the slot it is about", resp.Blockers[0].Dir)
	}
	if _, err := os.Stat(slotDir); err != nil {
		t.Errorf("the refused removal deleted the slot anyway: %v", err)
	}
	rows, _ := f.repoRows.ListByTask("01ALPHA")
	if len(rows) != 1 {
		t.Errorf("rows = %+v, want the row untouched by a refusal", rows)
	}
}

func TestWorkspaceRemoveSlotRefusesUnpushedCommits(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	slotDir := filepath.Join(f.workspaceDir, "alpha-repo")

	mustRun(t, slotDir, "git", "config", "user.email", "test@example.com")
	mustRun(t, slotDir, "git", "config", "user.name", "Test User")
	mustRun(t, slotDir, "git", "commit", "--allow-empty", "-m", "unpushed work")

	resp := f.run(removeRequest("alpha-repo", false))

	if resp.Reason != hooks.ReasonUnsavedWork {
		t.Fatalf("reason = %q, want %q: %+v", resp.Reason, hooks.ReasonUnsavedWork, resp)
	}
	found := false
	for _, blocker := range resp.Blockers {
		if blocker.Kind == hooks.BlockerUnpushedCommits {
			found = true
		}
	}
	if !found {
		t.Errorf("blockers = %+v, want the unpushed-commits blocker", resp.Blockers)
	}
}

// AC: {"force": true} proceeds anyway.
func TestWorkspaceRemoveSlotForceOverridesTheGate(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	slotDir := filepath.Join(f.workspaceDir, "alpha-repo")

	if err := os.WriteFile(filepath.Join(slotDir, "NOTES.md"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("writing a scratch file: %v", err)
	}

	resp := f.run(removeRequest("alpha-repo", true))

	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("forced remove: %+v", resp)
	}
	if _, err := os.Stat(slotDir); !os.IsNotExist(err) {
		t.Errorf("the forced removal left the slot behind: %v", err)
	}
	rows, _ := f.repoRows.ListByTask("01ALPHA")
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want the row dropped", rows)
	}
}

// AC: removing a repo's last slot is how a repo leaves a task. It is
// allowed, and it goes through the same gates.
func TestWorkspaceRemoveLastSlotDropsTheRepoFromTheTask(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo")
	f.add("alpha-repo", "")
	f.add("beta-repo", "")

	resp := f.run(removeRequest("beta-repo", false))
	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("remove: %+v", resp)
	}
	if resp.Data["repo_dropped"] != "true" {
		t.Errorf("repo_dropped = %q, want true for the last slot of a repo", resp.Data["repo_dropped"])
	}

	repos := f.run(hooks.NewWorkspaceRequest(hooks.WorkspaceOpRepos, "alpha"))
	for _, repo := range repos.Repos {
		if repo.Name == "beta-repo" && repo.InTask {
			t.Error("beta-repo still reads as in_task after its last slot went")
		}
	}
}

// The task's cwd is the workspace *container* in multi_repo, so no slot
// removal can ever take it out from under the task. Verify that, since
// the guard below only fires in the other strategy.
func TestMultiRepoTaskCwdIsTheContainerNotASlot(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	slot := f.add("alpha-repo", "")

	tasks, _ := f.taskStore.List()
	if tasks[0].Cwd != f.workspaceDir {
		t.Fatalf("task cwd = %q, want the workspace container %q", tasks[0].Cwd, f.workspaceDir)
	}

	slotPath := workspace.SlotPath(f.repoSets, f.workspaceDir, slot.Dir, slot.Slot)
	if slotPath == f.workspaceDir {
		t.Fatal("a multi_repo slot resolved to the workspace root")
	}

	if resp := f.run(removeRequest("alpha-repo", false)); resp.Status != hooks.WorkspaceStatusOK {
		t.Errorf("removing the only slot of a multi_repo task: %+v", resp)
	}
}

// In single_repo the workspace directory IS the initial checkout and IS
// the task's cwd, so "remove that slot" means "delete the task's whole
// working directory". Refused outright, force or not.
func TestWorkspaceRemoveSlotRefusesTheSingleRepoWorkspaceRoot(t *testing.T) {
	f := newWSFixtureWith(t, workspace.StrategySingleRepo, "git", "alpha-repo")
	f.add("alpha-repo", "")

	for _, force := range []bool{false, true} {
		resp := f.run(removeRequest("alpha-repo", force))
		if resp.Reason != hooks.ReasonWorkspaceRoot {
			t.Fatalf("force=%t: reason = %q, want %q: %+v", force, resp.Reason, hooks.ReasonWorkspaceRoot, resp)
		}
		if _, err := os.Stat(f.workspaceDir); err != nil {
			t.Fatalf("force=%t: the workspace directory was removed: %v", force, err)
		}
	}
}

// A recorded slot with nothing on disk is refused until forced, because
// the mismatch is worth surfacing before it is papered over.
func TestWorkspaceRemoveSlotRefusesAMissingDirectoryUntilForced(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	if err := os.RemoveAll(filepath.Join(f.workspaceDir, "alpha-repo")); err != nil {
		t.Fatalf("removing the slot directory: %v", err)
	}

	resp := f.run(removeRequest("alpha-repo", false))
	if resp.Reason != hooks.ReasonSlotMissing {
		t.Fatalf("reason = %q, want %q: %+v", resp.Reason, hooks.ReasonSlotMissing, resp)
	}
	if !strings.Contains(resp.Message, "force") {
		t.Errorf("message %q does not say how to proceed", resp.Message)
	}

	if forced := f.run(removeRequest("alpha-repo", true)); forced.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("forced remove of a missing slot: %+v", forced)
	}
	rows, _ := f.repoRows.ListByTask("01ALPHA")
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want the stale row dropped", rows)
	}
}

func TestWorkspaceRemoveSlotRejectsAnUnknownSlot(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")

	resp := f.run(removeRequest("no-such-dir", false))

	if resp.Reason != hooks.ReasonUnknownSlot {
		t.Fatalf("reason = %q, want %q: %+v", resp.Reason, hooks.ReasonUnknownSlot, resp)
	}
	if !strings.Contains(resp.Message, "alpha-repo") {
		t.Errorf("message %q does not list the slots that do exist", resp.Message)
	}
}

// Naming a slot by repo and label is the friendlier form; it must
// resolve to exactly one directory.
func TestWorkspaceRemoveSlotAcceptsRepoAndLabel(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	f.add("alpha-repo", "tests")

	req := hooks.NewWorkspaceRequest(hooks.WorkspaceOpRemoveSlot, "alpha")
	req.Repo = "alpha-repo"
	req.Label = "tests"

	resp := f.run(req)
	if resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("remove by repo+label: %+v", resp)
	}
	if resp.Slot.Dir != "alpha-repo--tests" {
		t.Errorf("removed %q, want alpha-repo--tests", resp.Slot.Dir)
	}
}

// Every operation that resolved a task leaves an events row behind,
// written before the caller is answered.
func TestWorkspaceOperationsRecordEventRows(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	f.run(listRequest())
	f.run(removeRequest("alpha-repo", false))

	for _, eventType := range []string{"workspace_add", "workspace_list", "workspace_remove_slot"} {
		if got := countEvents(t, f.database, eventType); got != 1 {
			t.Errorf("%s events = %d, want 1", eventType, got)
		}
	}

	var payload string
	if err := f.database.QueryRow(
		"SELECT payload FROM events WHERE event_type = 'workspace_add'",
	).Scan(&payload); err != nil {
		t.Fatalf("reading the add payload: %v", err)
	}
	for _, want := range []string{`"op":"add"`, `"task":"alpha"`, `"repo":"alpha-repo"`, `"status":"ok"`} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload %s missing %s", payload, want)
		}
	}
}

// A task with no workspace has nothing to enumerate or add to, and says
// so rather than answering with an empty list.
func TestWorkspaceOperationsRefuseATaskWithNoWorkspace(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	if err := f.taskStore.Create(&db.Task{
		ID: "01PLAIN", Name: "plain", State: db.StateActive, Attention: db.AttentionOK, Cwd: "/tmp",
	}); err != nil {
		t.Fatalf("creating the plain task: %v", err)
	}

	for _, op := range []hooks.WorkspaceOp{hooks.WorkspaceOpList, hooks.WorkspaceOpAdd, hooks.WorkspaceOpRemoveSlot} {
		req := hooks.NewWorkspaceRequest(op, "plain")
		req.Repo = "alpha-repo"
		req.Dir = "alpha-repo"

		if resp := f.run(req); resp.Reason != hooks.ReasonNoWorkspace {
			t.Errorf("op %q: reason = %q, want %q", op, resp.Reason, hooks.ReasonNoWorkspace)
		}
	}
}

var _ tea.Msg = workspaceRequestDoneMsg{}
