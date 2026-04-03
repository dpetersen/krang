package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitWorktreeDestroyWithUncommittedChanges(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initGitRepo(t, repoDir)

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "dirty-destroy", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Add an uncommitted file and modify a tracked file.
	if err := os.WriteFile(filepath.Join(result.WorkspaceDir, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create and stage a file (staged but uncommitted).
	if err := os.WriteFile(filepath.Join(result.WorkspaceDir, "staged.txt"), []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, result.WorkspaceDir, "git", "add", "staged.txt")

	// Destroy should succeed despite dirty state (--force).
	if err := Destroy(rs, result.WorkspaceDir); err != nil {
		t.Fatalf("Destroy with uncommitted changes: %v", err)
	}

	if _, err := os.Stat(result.WorkspaceDir); !os.IsNotExist(err) {
		t.Error("workspace dir should be removed after Destroy")
	}

	// Branch should be deleted (no actual commits on it).
	checkCmd := exec.Command("git", "rev-parse", "--verify", "krang/dirty-destroy")
	checkCmd.Dir = repoDir
	if checkCmd.Run() == nil {
		t.Error("branch krang/dirty-destroy should be deleted after destroy")
	}
}

func TestGitWorktreeDestroyKeepsUnpushedBranch(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initGitRepo(t, repoDir)

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "unpushed-destroy", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Make a commit on the worktree's branch.
	if err := os.WriteFile(filepath.Join(result.WorkspaceDir, "feature.txt"), []byte("feature"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, result.WorkspaceDir, "git", "add", "feature.txt")
	run(t, result.WorkspaceDir, "git", "commit", "-m", "add feature")

	if err := Destroy(rs, result.WorkspaceDir); err != nil {
		t.Fatalf("Destroy with unpushed commits: %v", err)
	}

	// Workspace dir should be gone.
	if _, err := os.Stat(result.WorkspaceDir); !os.IsNotExist(err) {
		t.Error("workspace dir should be removed after Destroy")
	}

	// Branch should be KEPT because git branch -d refuses to delete
	// branches with commits not reachable from HEAD/upstream.
	checkCmd := exec.Command("git", "rev-parse", "--verify", "krang/unpushed-destroy")
	checkCmd.Dir = repoDir
	if err := checkCmd.Run(); err != nil {
		t.Error("branch krang/unpushed-destroy should be kept (has unpushed commits)")
	}
}

func TestGitWorktreeCreateWithStaleBranch(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initGitRepo(t, repoDir)

	// Manually create a stale branch as if a previous task crashed.
	run(t, repoDir, "git", "branch", "krang/stale-task")

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	// Creating a workspace with the same task name should succeed
	// because addGitWorktree cleans up stale branches.
	result, err := Create(rs, "stale-task", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create with stale branch: %v", err)
	}

	// Verify the worktree was created.
	gitPath := filepath.Join(result.WorkspaceDir, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		t.Fatalf("expected .git at workspace: %v", err)
	}
	if info.IsDir() {
		t.Error(".git should be a file (worktree), not a directory")
	}
}

func TestGitWorktreeForkWithCommits(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initGitRepo(t, repoDir)

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	// Create source workspace and make commits on it.
	srcResult, err := Create(rs, "src-with-commits", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create source: %v", err)
	}

	if err := os.WriteFile(filepath.Join(srcResult.WorkspaceDir, "a.txt"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, srcResult.WorkspaceDir, "git", "add", "a.txt")
	run(t, srcResult.WorkspaceDir, "git", "commit", "-m", "first commit")

	if err := os.WriteFile(filepath.Join(srcResult.WorkspaceDir, "b.txt"), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, srcResult.WorkspaceDir, "git", "add", "b.txt")
	run(t, srcResult.WorkspaceDir, "git", "commit", "-m", "second commit")

	// Fork from the source.
	forkDir := filepath.Join(workspacesDir, "fork-with-commits")
	forkResult := ForkRepo(rs, srcResult.WorkspaceDir, forkDir, "myrepo", "fork-with-commits")
	if forkResult.Err != nil {
		t.Fatalf("ForkRepo: %v", forkResult.Err)
	}

	// Fork should have both committed files.
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(forkDir, name)); err != nil {
			t.Errorf("fork should have %s: %v", name, err)
		}
	}

	// Fork and source should be on different branches.
	srcBranch := gitCurrentBranch(t, srcResult.WorkspaceDir)
	forkBranch := gitCurrentBranch(t, forkDir)
	if srcBranch == forkBranch {
		t.Errorf("source and fork should be on different branches, both on %s", srcBranch)
	}

	// Committing on the fork should not affect the source.
	if err := os.WriteFile(filepath.Join(forkDir, "fork-only.txt"), []byte("fork"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, forkDir, "git", "add", "fork-only.txt")
	run(t, forkDir, "git", "commit", "-m", "fork commit")

	if _, err := os.Stat(filepath.Join(srcResult.WorkspaceDir, "fork-only.txt")); !os.IsNotExist(err) {
		t.Error("commit on fork should not appear in source")
	}
}

func TestGitWorktreeForkFromFork(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initGitRepo(t, repoDir)

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	// Create A.
	resultA, err := Create(rs, "task-a", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}

	// Fork A -> B.
	dirB := filepath.Join(workspacesDir, "task-b")
	forkB := ForkRepo(rs, resultA.WorkspaceDir, dirB, "myrepo", "task-b")
	if forkB.Err != nil {
		t.Fatalf("Fork A->B: %v", forkB.Err)
	}

	// Fork B -> C (fork from a fork).
	dirC := filepath.Join(workspacesDir, "task-c")
	forkC := ForkRepo(rs, dirB, dirC, "myrepo", "task-c")
	if forkC.Err != nil {
		t.Fatalf("Fork B->C: %v", forkC.Err)
	}

	// All three should resolve back to the same source repo.
	// Use EvalSymlinks because macOS /var -> /private/var.
	wantRepo, _ := filepath.EvalSymlinks(repoDir)
	for _, wtDir := range []string{resultA.WorkspaceDir, dirB, dirC} {
		resolved, err := resolveGitWorktreeSource(wtDir)
		if err != nil {
			t.Fatalf("resolveGitWorktreeSource(%s): %v", wtDir, err)
		}
		got, _ := filepath.EvalSymlinks(resolved)
		if got != wantRepo {
			t.Errorf("resolveGitWorktreeSource(%s) = %q, want %q", wtDir, got, wantRepo)
		}
	}

	// All three should be on different branches.
	branches := map[string]bool{}
	for _, wtDir := range []string{resultA.WorkspaceDir, dirB, dirC} {
		b := gitCurrentBranch(t, wtDir)
		if branches[b] {
			t.Errorf("duplicate branch %s", b)
		}
		branches[b] = true
	}
}

func TestHasUncommittedChangesClean(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	if HasUncommittedChanges(dir) {
		t.Error("clean repo should not report uncommitted changes")
	}
}

func TestHasUncommittedChangesModifiedFile(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create a tracked file.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", "file.txt")
	run(t, dir, "git", "commit", "-m", "add file")

	// Modify it.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !HasUncommittedChanges(dir) {
		t.Error("modified tracked file should report uncommitted changes")
	}
}

func TestHasUncommittedChangesStagedFile(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", "staged.txt")

	if !HasUncommittedChanges(dir) {
		t.Error("staged but uncommitted file should report uncommitted changes")
	}
}

func TestHasUncommittedChangesUntrackedFile(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !HasUncommittedChanges(dir) {
		t.Error("untracked file should report uncommitted changes")
	}
}

func TestHasUnpushedCommitsNoRemote(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// A repo with no remote has all commits "unpushed".
	if !HasUnpushedCommits(dir) {
		t.Error("repo with no remote should report unpushed commits")
	}
}

func TestHasUnpushedCommitsWithRemote(t *testing.T) {
	dir := t.TempDir()

	// Set up a bare repo as the "remote".
	bareDir := filepath.Join(dir, "bare.git")
	run(t, dir, "git", "init", "--bare", bareDir)

	// Clone it.
	workDir := filepath.Join(dir, "work")
	run(t, dir, "git", "clone", bareDir, workDir)

	// Make a commit and push it.
	if err := os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, workDir, "git", "add", "file.txt")
	run(t, workDir, "git", "commit", "-m", "initial")
	run(t, workDir, "git", "push", "origin", "HEAD")

	// All commits are pushed.
	if HasUnpushedCommits(workDir) {
		t.Error("all commits are pushed, should not report unpushed")
	}

	// Make another commit without pushing.
	if err := os.WriteFile(filepath.Join(workDir, "file2.txt"), []byte("more"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, workDir, "git", "add", "file2.txt")
	run(t, workDir, "git", "commit", "-m", "unpushed")

	if !HasUnpushedCommits(workDir) {
		t.Error("should report unpushed commits after new local commit")
	}
}

func gitCurrentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse --abbrev-ref HEAD in %s: %v: %s", dir, err, out)
	}
	return strings.TrimSpace(string(out))
}
