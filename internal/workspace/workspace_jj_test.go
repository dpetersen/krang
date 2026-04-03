package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func jjAvailable() bool {
	_, err := exec.LookPath("jj")
	return err == nil
}

func initJJRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run(t, dir, "jj", "git", "init")
	// Create a file and snapshot so the repo has content.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("writing README: %v", err)
	}
	// jj auto-snapshots on next command, so just describe the commit.
	run(t, dir, "jj", "describe", "-m", "init")
	// Create a new empty change on top so the working copy is clean.
	run(t, dir, "jj", "new")
}

func TestJJWorkspaceCreate(t *testing.T) {
	if !jjAvailable() {
		t.Skip("jj not installed")
	}

	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initJJRepo(t, repoDir)

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "jj-task", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should have .jj directory (workspace).
	jjDir := filepath.Join(result.WorkspaceDir, ".jj")
	if _, err := os.Stat(jjDir); err != nil {
		t.Errorf("expected .jj dir at workspace: %v", err)
	}

	// README should be present from the source repo.
	if _, err := os.Stat(filepath.Join(result.WorkspaceDir, "README.md")); err != nil {
		t.Error("README.md should be present in jj workspace")
	}

	// Workspace should be listed in jj workspace list from source.
	listCmd := exec.Command("jj", "workspace", "list")
	listCmd.Dir = repoDir
	out, err := listCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jj workspace list: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "jj-task") {
		t.Errorf("workspace 'jj-task' not found in jj workspace list:\n%s", out)
	}
}

func TestJJWorkspaceDestroy(t *testing.T) {
	if !jjAvailable() {
		t.Skip("jj not installed")
	}

	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initJJRepo(t, repoDir)

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "jj-destroy", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := Destroy(rs, result.WorkspaceDir); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Workspace dir should be gone.
	if _, err := os.Stat(result.WorkspaceDir); !os.IsNotExist(err) {
		t.Error("workspace dir should be removed after Destroy")
	}

	// Workspace should be forgotten in jj.
	listCmd := exec.Command("jj", "workspace", "list")
	listCmd.Dir = repoDir
	out, _ := listCmd.CombinedOutput()
	if strings.Contains(string(out), "jj-destroy") {
		t.Error("workspace should be forgotten after Destroy")
	}
}

func TestJJWorkspaceCreateMultiRepo(t *testing.T) {
	if !jjAvailable() {
		t.Skip("jj not installed")
	}

	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initJJRepo(t, filepath.Join(reposDir, "alpha"))
	initJJRepo(t, filepath.Join(reposDir, "beta"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "jj-multi", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(result.Created) != 2 {
		t.Errorf("Created %d repos, want 2", len(result.Created))
	}

	for _, repo := range []string{"alpha", "beta"} {
		jjDir := filepath.Join(result.WorkspaceDir, repo, ".jj")
		if _, err := os.Stat(jjDir); err != nil {
			t.Errorf("expected .jj dir for %s: %v", repo, err)
		}
	}
}

func TestJJWorkspaceForkIndependent(t *testing.T) {
	if !jjAvailable() {
		t.Skip("jj not installed")
	}

	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initJJRepo(t, repoDir)

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	// Create source workspace.
	srcResult, err := Create(rs, "jj-src", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create source: %v", err)
	}

	// Add a file in the source workspace.
	if err := os.WriteFile(filepath.Join(srcResult.WorkspaceDir, "work.txt"), []byte("in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Snapshot by running a jj command.
	run(t, srcResult.WorkspaceDir, "jj", "describe", "-m", "wip")

	// Fork it.
	forkDir := filepath.Join(workspacesDir, "jj-fork")
	forkResult := ForkRepo(rs, srcResult.WorkspaceDir, forkDir, "myrepo", "jj-fork")
	if forkResult.Err != nil {
		t.Fatalf("ForkRepo: %v", forkResult.Err)
	}

	// Fork should have the work file.
	data, err := os.ReadFile(filepath.Join(forkDir, "work.txt"))
	if err != nil {
		t.Fatalf("reading work.txt in fork: %v", err)
	}
	if string(data) != "in progress" {
		t.Errorf("fork work.txt = %q, want %q", string(data), "in progress")
	}

	// Both workspaces should be listed.
	listCmd := exec.Command("jj", "workspace", "list")
	listCmd.Dir = repoDir
	out, _ := listCmd.CombinedOutput()
	for _, name := range []string{"jj-src", "jj-fork"} {
		if !strings.Contains(string(out), name) {
			t.Errorf("workspace %q not in jj workspace list:\n%s", name, out)
		}
	}

	// Editing in the fork should not affect the source.
	if err := os.WriteFile(filepath.Join(forkDir, "fork-only.txt"), []byte("fork"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, forkDir, "jj", "describe", "-m", "fork work")

	// Source should not have fork-only.txt.
	run(t, srcResult.WorkspaceDir, "jj", "workspace", "update-stale")
	if _, err := os.Stat(filepath.Join(srcResult.WorkspaceDir, "fork-only.txt")); !os.IsNotExist(err) {
		t.Error("editing fork should not affect source workspace")
	}
}
