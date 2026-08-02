package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordedCommand struct {
	Dir  string
	Args []string
}

func (c recordedCommand) String() string {
	return c.Dir + ": " + strings.Join(c.Args, " ")
}

// captureVCSCommands swaps the VCS exec seam for a recorder so cleanup
// tests can assert on the commands krang builds without standing up
// real repositories.
func captureVCSCommands(t *testing.T) *[]recordedCommand {
	t.Helper()

	original := runVCSCommand
	t.Cleanup(func() { runVCSCommand = original })

	var recorded []recordedCommand
	runVCSCommand = func(dir, name string, args ...string) (string, error) {
		recorded = append(recorded, recordedCommand{
			Dir:  dir,
			Args: append([]string{name}, args...),
		})
		return "", nil
	}
	return &recorded
}

// markRepoDir makes dir look like a checkout of the given VCS so
// isRepoDir and DetectVCS treat it as one.
func markRepoDir(t *testing.T, dir, vcs string) {
	t.Helper()
	marker := ".git"
	if vcs == "jj" {
		marker = ".jj"
	}
	if err := os.MkdirAll(filepath.Join(dir, marker), 0o755); err != nil {
		t.Fatalf("marking %s as %s: %v", dir, vcs, err)
	}
}

func multiRepoSets(t *testing.T) (rs *RepoSets, reposDir, workspacesDir string) {
	t.Helper()
	dir := t.TempDir()
	reposDir = filepath.Join(dir, "repos")
	workspacesDir = filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)
	mkdirs(t, workspacesDir)
	return &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}, reposDir, workspacesDir
}

func TestForgetRepoUsesRecordedProvenance(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	// The slot's directory name no longer names a repo, and its jj
	// workspace name no longer matches the workspace directory.
	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	markRepoDir(t, filepath.Join(workspaceDir, "krang-tests"), "jj")

	recorded := []RepoProvenance{{
		DirName:  "krang-tests",
		RepoName: "krang",
		VCS:      "jj",
		VCSName:  "slots--krang--tests",
	}}

	targets := DestroyRepoList(rs, workspaceDir, recorded)
	if len(targets) != 1 {
		t.Fatalf("DestroyRepoList = %+v, want the single recorded row", targets)
	}

	commands := captureVCSCommands(t)
	result := ForgetRepo(rs, workspaceDir, targets[0])
	if result.Err != nil {
		t.Fatalf("ForgetRepo: %v", result.Err)
	}
	if result.Repo != "krang" {
		t.Errorf("result repo = %q, want krang", result.Repo)
	}

	want := []recordedCommand{{
		Dir:  filepath.Join(reposDir, "krang"),
		Args: []string{"jj", "workspace", "forget", "slots--krang--tests"},
	}}
	if !reflect.DeepEqual(*commands, want) {
		t.Errorf("commands = %v, want %v", *commands, want)
	}
}

func TestForgetRepoUsesRecordedProvenanceForGit(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	markRepoDir(t, filepath.Join(reposDir, "krang"), "git")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	slotDir := filepath.Join(workspaceDir, "krang-tests")
	markRepoDir(t, slotDir, "git")

	commands := captureVCSCommands(t)
	result := ForgetRepo(rs, workspaceDir, RepoProvenance{
		DirName:  "krang-tests",
		RepoName: "krang",
		VCS:      "git",
		VCSName:  "slots--krang--tests",
		Recorded: true,
	})
	if result.Err != nil {
		t.Fatalf("ForgetRepo: %v", result.Err)
	}

	want := []recordedCommand{
		{
			Dir:  filepath.Join(reposDir, "krang"),
			Args: []string{"git", "worktree", "remove", "--force", slotDir},
		},
		{
			Dir:  filepath.Join(reposDir, "krang"),
			Args: []string{"git", "branch", "-d", "krang/slots--krang--tests"},
		},
	}
	if !reflect.DeepEqual(*commands, want) {
		t.Errorf("commands = %v, want %v", *commands, want)
	}
}

func TestForgetRepoFallsBackForUnrecordedDir(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	// A repo cloned into the workspace by hand: no recorded row, so
	// cleanup keeps guessing from the directory name as it always has.
	markRepoDir(t, filepath.Join(reposDir, "oneoff"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	markRepoDir(t, filepath.Join(workspaceDir, "oneoff"), "jj")

	targets := DestroyRepoList(rs, workspaceDir, nil)
	if len(targets) != 1 || targets[0].Recorded {
		t.Fatalf("DestroyRepoList = %+v, want one derived entry", targets)
	}

	commands := captureVCSCommands(t)
	if result := ForgetRepo(rs, workspaceDir, targets[0]); result.Err != nil {
		t.Fatalf("ForgetRepo: %v", result.Err)
	}

	want := []recordedCommand{{
		Dir:  filepath.Join(reposDir, "oneoff"),
		Args: []string{"jj", "workspace", "forget", "slots"},
	}}
	if !reflect.DeepEqual(*commands, want) {
		t.Errorf("commands = %v, want %v", *commands, want)
	}
}

func TestDestroyRepoListMergesRecordedAndScannedDirs(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	markRepoDir(t, filepath.Join(reposDir, "krang"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "slots")
	markRepoDir(t, filepath.Join(workspaceDir, "krang"), "jj")
	markRepoDir(t, filepath.Join(workspaceDir, "krang-tests"), "jj")
	markRepoDir(t, filepath.Join(workspaceDir, "oneoff"), "jj")
	mkdirs(t, workspaceDir, "notes")

	recorded := []RepoProvenance{
		{DirName: "krang", RepoName: "krang", VCS: "jj", VCSName: "slots"},
		{DirName: "krang-tests", RepoName: "krang", VCS: "jj", VCSName: "slots--krang--tests"},
	}

	targets := DestroyRepoList(rs, workspaceDir, recorded)
	if len(targets) != 3 {
		t.Fatalf("DestroyRepoList returned %d targets, want 3: %+v", len(targets), targets)
	}

	for i, target := range targets[:2] {
		if !target.Recorded {
			t.Errorf("target %d (%s) should be marked recorded", i, target.DirName)
		}
		if target.RepoName != "krang" {
			t.Errorf("target %d repo = %q, want krang", i, target.RepoName)
		}
	}

	fallback := targets[2]
	if fallback.DirName != "oneoff" || fallback.RepoName != "oneoff" {
		t.Errorf("fallback target = %+v, want the unrecorded oneoff dir", fallback)
	}
	if fallback.Recorded {
		t.Error("fallback target should not be marked recorded")
	}
	if fallback.VCSName != "slots" {
		t.Errorf("fallback vcs name = %q, want the workspace dir name", fallback.VCSName)
	}
}

// AC: completing a task forgets EVERY recorded VCS identity it holds,
// not one per repo. Two checkouts of one repo are two jj workspaces, and
// the second is exactly the one a per-repo loop would drop.
func TestDestroyForgetsEveryRecordedSlotIdentity(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	markRepoDir(t, filepath.Join(reposDir, "alpha"), "jj")
	markRepoDir(t, filepath.Join(reposDir, "beta"), "git")
	workspaceDir := filepath.Join(workspacesDir, "slotty")
	for _, dir := range []string{"alpha", "alpha--tests", "alpha--docs", "beta"} {
		markRepoDir(t, filepath.Join(workspaceDir, dir), "jj")
	}

	recorded := []RepoProvenance{
		{DirName: "alpha", RepoName: "alpha", VCS: "jj", VCSName: "slotty"},
		{DirName: "alpha--tests", RepoName: "alpha", VCS: "jj", VCSName: "slotty--alpha--tests"},
		{DirName: "alpha--docs", RepoName: "alpha", VCS: "jj", VCSName: "slotty--alpha--docs"},
		{DirName: "beta", RepoName: "beta", VCS: "git", VCSName: "slotty"},
	}

	commands := captureVCSCommands(t)
	if err := Destroy(rs, workspaceDir, recorded); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	alphaSrc := filepath.Join(reposDir, "alpha")
	betaSrc := filepath.Join(reposDir, "beta")
	want := []recordedCommand{
		{Dir: alphaSrc, Args: []string{"jj", "workspace", "forget", "slotty"}},
		{Dir: alphaSrc, Args: []string{"jj", "workspace", "forget", "slotty--alpha--tests"}},
		{Dir: alphaSrc, Args: []string{"jj", "workspace", "forget", "slotty--alpha--docs"}},
		{Dir: betaSrc, Args: []string{"git", "worktree", "remove", "--force", filepath.Join(workspaceDir, "beta")}},
		{Dir: betaSrc, Args: []string{"git", "branch", "-d", "krang/slotty"}},
	}
	if !reflect.DeepEqual(*commands, want) {
		t.Errorf("commands = %v, want %v", *commands, want)
	}

	if _, err := os.Stat(workspaceDir); !os.IsNotExist(err) {
		t.Errorf("workspace directory survived Destroy: %v", err)
	}
}

// A recorded slot whose directory somebody already deleted still owns a
// jj workspace name in the source repo, so cleanup still has to forget
// it. Dropping it because the scan can't see it would leak the identity
// and block a task of the same name from ever being created again.
func TestDestroyRepoListKeepsRecordedSlotsWithNoDirectory(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	markRepoDir(t, filepath.Join(reposDir, "alpha"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "slotty")
	markRepoDir(t, filepath.Join(workspaceDir, "alpha"), "jj")

	recorded := []RepoProvenance{
		{DirName: "alpha", RepoName: "alpha", VCS: "jj", VCSName: "slotty"},
		{DirName: "alpha--gone", RepoName: "alpha", VCS: "jj", VCSName: "slotty--alpha--gone"},
	}

	targets := DestroyRepoList(rs, workspaceDir, recorded)
	if len(targets) != 2 || targets[1].VCSName != "slotty--alpha--gone" {
		t.Fatalf("DestroyRepoList = %+v, want both recorded rows", targets)
	}
}

// In single_repo the workspace directory IS the checkout, so its
// subdirectories are that repo's own contents. Scanning them the way a
// multi_repo container is scanned would invent slots out of vendored
// checkouts and aim cleanup at repos nobody asked it to touch.
func TestDestroyRepoListDoesNotScanInsideASingleRepoWorkspace(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)
	rs.WorkspaceStrategy = StrategySingleRepo

	markRepoDir(t, filepath.Join(reposDir, "alpha"), "jj")
	markRepoDir(t, filepath.Join(reposDir, "vendored"), "jj")
	workspaceDir := filepath.Join(workspacesDir, "solo")
	markRepoDir(t, workspaceDir, "jj")
	markRepoDir(t, filepath.Join(workspaceDir, "vendored"), "git")

	recorded := []RepoProvenance{
		{DirName: "alpha", RepoName: "alpha", VCS: "jj", VCSName: "solo"},
	}

	targets := DestroyRepoList(rs, workspaceDir, recorded)
	if len(targets) != 1 || targets[0].RepoName != "alpha" {
		t.Errorf("DestroyRepoList = %+v, want only the recorded checkout", targets)
	}
}

// The branch a warning names has to be the branch the removal will try
// to delete — which for a slot is not krang/<task>.
func TestGitBranchForNamesEachSlotsOwnBranch(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)
	markRepoDir(t, filepath.Join(reposDir, "alpha"), "git")
	workspaceDir := filepath.Join(workspacesDir, "slotty")

	cases := []struct {
		target RepoProvenance
		want   string
	}{
		{RepoProvenance{DirName: "alpha", RepoName: "alpha", VCS: "git", VCSName: "slotty"}, "krang/slotty"},
		{
			RepoProvenance{DirName: "alpha--tests", RepoName: "alpha", VCS: "git", VCSName: "slotty--alpha--tests"},
			"krang/slotty--alpha--tests",
		},
		// An unrecorded directory falls back to the pre-slot derivation,
		// which is also what ForgetRepo will use on it.
		{RepoProvenance{DirName: "oneoff"}, "krang/slotty"},
	}
	for _, tc := range cases {
		if got := GitBranchFor(rs, workspaceDir, tc.target); got != tc.want {
			t.Errorf("GitBranchFor(%+v) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestDeriveProvenanceUsesPreSlotDerivation(t *testing.T) {
	rs, reposDir, workspacesDir := multiRepoSets(t)

	markRepoDir(t, filepath.Join(reposDir, "alpha"), "jj")
	markRepoDir(t, filepath.Join(reposDir, "beta"), "git")
	workspaceDir := filepath.Join(workspacesDir, "legacy")
	markRepoDir(t, filepath.Join(workspaceDir, "alpha"), "jj")
	markRepoDir(t, filepath.Join(workspaceDir, "beta"), "git")
	mkdirs(t, workspaceDir, "scratch")

	derived := DeriveProvenance(rs, workspaceDir)

	want := []RepoProvenance{
		{DirName: "alpha", RepoName: "alpha", VCS: "jj", VCSName: "legacy"},
		{DirName: "beta", RepoName: "beta", VCS: "git", VCSName: "legacy"},
	}
	if !reflect.DeepEqual(derived, want) {
		t.Errorf("DeriveProvenance = %+v, want %+v", derived, want)
	}
}
