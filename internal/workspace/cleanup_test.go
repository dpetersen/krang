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
