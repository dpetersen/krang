package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateGitWorkspace(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initGitRepo(t, filepath.Join(reposDir, "alpha"))
	initGitRepo(t, filepath.Join(reposDir, "beta"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "my-task", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(result.Errors) > 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if len(result.Created) != 2 {
		t.Errorf("Created %d repos, want 2", len(result.Created))
	}

	expectedDir := filepath.Join(workspacesDir, "my-task")
	if result.WorkspaceDir != expectedDir {
		t.Errorf("WorkspaceDir = %q, want %q", result.WorkspaceDir, expectedDir)
	}

	// Verify cloned repos exist and are git repos.
	for _, repo := range []string{"alpha", "beta"} {
		gitDir := filepath.Join(expectedDir, repo, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			t.Errorf("expected git dir at %s: %v", gitDir, err)
		}
	}
}

func TestCreateSingleRepoWorkspace(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initGitRepo(t, filepath.Join(reposDir, "api-server"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "fix-auth", []string{"api-server"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	expectedDir := filepath.Join(workspacesDir, "fix-auth")
	if result.WorkspaceDir != expectedDir {
		t.Errorf("WorkspaceDir = %q, want %q", result.WorkspaceDir, expectedDir)
	}

	// In single_repo mode, the workspace dir IS the repo clone directly.
	gitDir := filepath.Join(expectedDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		t.Errorf("expected .git at workspace root %s: %v", gitDir, err)
	}

	// There should NOT be a subdirectory named after the repo.
	nestedDir := filepath.Join(expectedDir, "api-server")
	if _, err := os.Stat(nestedDir); err == nil {
		t.Error("single_repo mode should not create a nested repo directory")
	}
}

func TestCreateEmptyWorkspace(t *testing.T) {
	for _, strategy := range []WorkspaceStrategy{StrategySingleRepo, StrategyMultiRepo} {
		t.Run(string(strategy), func(t *testing.T) {
			dir := t.TempDir()

			workspacesDir := filepath.Join(dir, "workspaces")

			rs := &RepoSets{
				MetarepoDir:       dir,
				WorkspaceStrategy: strategy,
				ReposDir:          filepath.Join(dir, "repos"),
				WorkspacesDir:     workspacesDir,
				Sets:              map[string][]string{},
			}

			result, err := Create(rs, "empty-task", nil)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			expectedDir := filepath.Join(workspacesDir, "empty-task")
			if result.WorkspaceDir != expectedDir {
				t.Errorf("WorkspaceDir = %q, want %q", result.WorkspaceDir, expectedDir)
			}
			if len(result.Created) != 0 {
				t.Errorf("Created = %v, want empty", result.Created)
			}
			if len(result.Errors) != 0 {
				t.Errorf("Errors = %v, want empty", result.Errors)
			}

			info, err := os.Stat(expectedDir)
			if err != nil {
				t.Fatalf("workspace dir should exist: %v", err)
			}
			if !info.IsDir() {
				t.Error("workspace path should be a directory")
			}
		})
	}
}

func TestDestroyEmptyWorkspace(t *testing.T) {
	for _, strategy := range []WorkspaceStrategy{StrategySingleRepo, StrategyMultiRepo} {
		t.Run(string(strategy), func(t *testing.T) {
			dir := t.TempDir()

			workspacesDir := filepath.Join(dir, "workspaces")

			rs := &RepoSets{
				MetarepoDir:       dir,
				WorkspaceStrategy: strategy,
				ReposDir:          filepath.Join(dir, "repos"),
				WorkspacesDir:     workspacesDir,
				Sets:              map[string][]string{},
			}

			result, err := Create(rs, "empty-destroy", nil)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			if err := Destroy(rs, result.WorkspaceDir); err != nil {
				t.Fatalf("Destroy: %v", err)
			}

			if _, err := os.Stat(result.WorkspaceDir); !os.IsNotExist(err) {
				t.Error("workspace dir should be removed after Destroy")
			}
		})
	}
}

func TestCreateAlreadyExists(t *testing.T) {
	dir := t.TempDir()

	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, workspacesDir, "existing-task")

	rs := &RepoSets{
		MetarepoDir:   dir,
		ReposDir:      filepath.Join(dir, "repos"),
		WorkspacesDir: workspacesDir,
		Sets:          map[string][]string{},
	}

	_, err := Create(rs, "existing-task", []string{"alpha"})
	if err == nil {
		t.Fatal("expected error for existing workspace dir")
	}
}

func TestCreatePartialFailure(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initGitRepo(t, filepath.Join(reposDir, "good"))
	// "bad" exists as a directory but is not a git repo.
	mkdirs(t, reposDir, "bad")

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "partial", []string{"good", "bad"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(result.Created) != 1 {
		t.Errorf("Created %d repos, want 1", len(result.Created))
	}
	if len(result.Errors) != 1 {
		t.Errorf("Errors = %v, want 1 error", result.Errors)
	}
}

func TestCreateAllFail(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	// "bad" is not a git repo.
	mkdirs(t, reposDir, "bad")

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	_, err := Create(rs, "doomed", []string{"bad"})
	if err == nil {
		t.Fatal("expected error when all repos fail")
	}

	// Workspace dir should be cleaned up.
	if _, statErr := os.Stat(filepath.Join(workspacesDir, "doomed")); !os.IsNotExist(statErr) {
		t.Error("expected workspace dir to be removed after total failure")
	}
}

func TestAddReposToWorkspace(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initGitRepo(t, filepath.Join(reposDir, "alpha"))
	initGitRepo(t, filepath.Join(reposDir, "beta"))
	initGitRepo(t, filepath.Join(reposDir, "gamma"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	// Create with 2 repos.
	createResult, err := Create(rs, "add-test", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	present := PresentRepos(createResult.WorkspaceDir)
	if len(present) != 2 {
		t.Fatalf("expected 2 present repos, got %v", present)
	}

	// Add a third repo.
	addResult, err := AddRepos(rs, createResult.WorkspaceDir, "add-test", []string{"gamma"})
	if err != nil {
		t.Fatalf("AddRepos: %v", err)
	}
	if len(addResult.Created) != 1 {
		t.Errorf("expected 1 added, got %d", len(addResult.Created))
	}

	present = PresentRepos(createResult.WorkspaceDir)
	if len(present) != 3 {
		t.Fatalf("expected 3 present repos after add, got %v", present)
	}
}

func TestDestroyGitWorkspace(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initGitRepo(t, filepath.Join(reposDir, "alpha"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "to-destroy", []string{"alpha"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify it exists.
	if _, err := os.Stat(result.WorkspaceDir); err != nil {
		t.Fatalf("workspace should exist: %v", err)
	}

	if err := Destroy(rs, result.WorkspaceDir); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if _, err := os.Stat(result.WorkspaceDir); !os.IsNotExist(err) {
		t.Error("workspace dir should be removed after Destroy")
	}
}

func TestDestroySingleRepoWorkspace(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initGitRepo(t, filepath.Join(reposDir, "api-server"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "single-destroy", []string{"api-server"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := Destroy(rs, result.WorkspaceDir); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if _, err := os.Stat(result.WorkspaceDir); !os.IsNotExist(err) {
		t.Error("workspace dir should be removed after Destroy")
	}
}

func TestParseDuplicateChangeID(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "standard output",
			output: "Duplicated 4489ac14cefe as ppznuquv b0849571 (no description set)\n",
			want:   "ppznuquv",
		},
		{
			name:   "with description",
			output: "Duplicated abc123 as xyzchange def456 Add feature X\n",
			want:   "xyzchange",
		},
		{
			name:   "with working copy snapshot prefix",
			output: "Working copy changes were snapshotted.\nDuplicated 4489ac14cefe as ppznuquv b0849571 (no description set)\n",
			want:   "ppznuquv",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
		{
			name:   "unexpected format",
			output: "something unexpected\n",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDuplicateChangeID(tt.output)
			if got != tt.want {
				t.Errorf("parseDuplicateChangeID(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestGitWorktreeCreation(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initGitRepo(t, filepath.Join(reposDir, "myrepo"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "test-task", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// .git should be a file (worktree pointer), not a directory.
	gitPath := filepath.Join(result.WorkspaceDir, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		t.Fatalf("expected .git at %s: %v", gitPath, err)
	}
	if info.IsDir() {
		t.Error(".git should be a file (worktree), not a directory (clone)")
	}

	// Branch krang/test-task should exist in the source repo.
	cmd := exec.Command("git", "rev-parse", "--verify", "krang/test-task")
	cmd.Dir = filepath.Join(reposDir, "myrepo")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("branch krang/test-task should exist in source repo: %v: %s", err, output)
	}
}

func TestGitWorktreeDestroy(t *testing.T) {
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

	result, err := Create(rs, "destroy-test", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := Destroy(rs, result.WorkspaceDir); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Workspace dir should be gone.
	if _, err := os.Stat(result.WorkspaceDir); !os.IsNotExist(err) {
		t.Error("workspace dir should be removed")
	}

	// Worktree should be deregistered.
	listCmd := exec.Command("git", "worktree", "list", "--porcelain")
	listCmd.Dir = repoDir
	listOut, _ := listCmd.CombinedOutput()
	if strings.Contains(string(listOut), "destroy-test") {
		t.Error("worktree should be deregistered from source repo")
	}

	// Branch should be deleted (no commits = safe to delete).
	checkCmd := exec.Command("git", "rev-parse", "--verify", "krang/destroy-test")
	checkCmd.Dir = repoDir
	if checkCmd.Run() == nil {
		t.Error("branch krang/destroy-test should be deleted after destroy")
	}
}

func TestGitWorktreeFork(t *testing.T) {
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

	// Create source workspace.
	srcResult, err := Create(rs, "source-task", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create source: %v", err)
	}

	// Add an uncommitted file to the source workspace.
	if err := os.WriteFile(filepath.Join(srcResult.WorkspaceDir, "dirty.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatalf("writing dirty file: %v", err)
	}

	// Fork it.
	forkDir := filepath.Join(workspacesDir, "fork-task")
	forkResult := ForkRepo(rs, srcResult.WorkspaceDir, forkDir, "myrepo", "fork-task")
	if forkResult.Err != nil {
		t.Fatalf("ForkRepo: %v", forkResult.Err)
	}

	// Fork should have the uncommitted file.
	data, err := os.ReadFile(filepath.Join(forkDir, "dirty.txt"))
	if err != nil {
		t.Fatalf("reading dirty file in fork: %v", err)
	}
	if string(data) != "uncommitted" {
		t.Errorf("fork dirty.txt = %q, want %q", string(data), "uncommitted")
	}

	// Fork should be a worktree with its own branch.
	forkGitPath := filepath.Join(forkDir, ".git")
	info, err := os.Lstat(forkGitPath)
	if err != nil {
		t.Fatalf("expected .git at fork: %v", err)
	}
	if info.IsDir() {
		t.Error("fork .git should be a file (worktree)")
	}

	checkCmd := exec.Command("git", "rev-parse", "--verify", "krang/fork-task")
	checkCmd.Dir = repoDir
	if output, err := checkCmd.CombinedOutput(); err != nil {
		t.Errorf("branch krang/fork-task should exist: %v: %s", err, output)
	}

	// Source should be unaffected.
	srcData, err := os.ReadFile(filepath.Join(srcResult.WorkspaceDir, "dirty.txt"))
	if err != nil {
		t.Fatalf("reading dirty file in source: %v", err)
	}
	if string(srcData) != "uncommitted" {
		t.Errorf("source dirty.txt = %q, want %q", string(srcData), "uncommitted")
	}
}

func TestWorktreeInclude(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	repoDir := filepath.Join(reposDir, "myrepo")
	initGitRepo(t, repoDir)

	// Create .worktreeinclude and a .env file in the source repo.
	if err := os.WriteFile(filepath.Join(repoDir, ".worktreeinclude"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".env"), []byte("SECRET=hunter2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "include-test", []string{"myrepo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// .env should be copied to the worktree.
	data, err := os.ReadFile(filepath.Join(result.WorkspaceDir, ".env"))
	if err != nil {
		t.Fatalf("expected .env in worktree: %v", err)
	}
	if string(data) != "SECRET=hunter2\n" {
		t.Errorf(".env content = %q, want %q", string(data), "SECRET=hunter2\n")
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	run(t, dir, "git", "commit", "--allow-empty", "-m", "init")
}

// initBareRemoteWithClone creates a bare git repo (the "remote"), clones it
// into cloneDir, and returns the bare repo path. The clone has origin pointing
// at the bare repo so fetch/push work locally.
func initBareRemoteWithClone(t *testing.T, bareDir, cloneDir, branchName string) {
	t.Helper()
	if err := os.MkdirAll(bareDir, 0o755); err != nil {
		t.Fatalf("mkdir bare: %v", err)
	}
	run(t, bareDir, "git", "init", "--bare")

	// Set the bare repo's HEAD to the desired branch so clones check it out.
	run(t, bareDir, "git", "symbolic-ref", "HEAD", "refs/heads/"+branchName)

	// Create a temporary working copy to push the initial commit.
	tmpClone := t.TempDir()
	run(t, tmpClone, "git", "clone", bareDir, ".")
	run(t, tmpClone, "git", "config", "user.email", "test@example.com")
	run(t, tmpClone, "git", "config", "user.name", "Test User")
	// Ensure the branch has the desired name.
	run(t, tmpClone, "git", "checkout", "-b", branchName)
	run(t, tmpClone, "git", "commit", "--allow-empty", "-m", "init")
	run(t, tmpClone, "git", "push", "origin", branchName)

	// Now clone into the actual source repo dir.
	if err := os.MkdirAll(filepath.Dir(cloneDir), 0o755); err != nil {
		t.Fatalf("mkdir clone parent: %v", err)
	}
	run(t, filepath.Dir(cloneDir), "git", "clone", bareDir, filepath.Base(cloneDir))
}

// gitHeadCommit returns the HEAD commit hash in the given repo dir.
func gitHeadCommit(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD in %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

func TestGitWorktreeBasedOnRemoteMain(t *testing.T) {
	dir := t.TempDir()

	bareDir := filepath.Join(dir, "bare")
	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	repoDir := filepath.Join(reposDir, "myrepo")

	initBareRemoteWithClone(t, bareDir, repoDir, "main")

	staleCommit := gitHeadCommit(t, repoDir)

	// Push a new commit from a separate clone (simulates other developers).
	pusher := t.TempDir()
	run(t, pusher, "git", "clone", bareDir, ".")
	run(t, pusher, "git", "config", "user.email", "test@example.com")
	run(t, pusher, "git", "config", "user.name", "Test User")
	run(t, pusher, "git", "commit", "--allow-empty", "-m", "new work")
	run(t, pusher, "git", "push", "origin", "main")

	latestCommit := gitHeadCommit(t, pusher)
	if staleCommit == latestCommit {
		t.Fatal("test setup: stale and latest commits should differ")
	}

	// Create workspace — should fetch and base on origin/main.
	dst := filepath.Join(workspacesDir, "test-task")
	_, err := addGitWorktree(repoDir, dst, "test-task")
	if err != nil {
		t.Fatalf("addGitWorktree: %v", err)
	}

	worktreeHead := gitHeadCommit(t, dst)
	if worktreeHead != latestCommit {
		t.Errorf("worktree HEAD = %s (stale), want %s (latest remote main)", worktreeHead, latestCommit)
	}
}

func TestGitWorktreeBasedOnRemoteMaster(t *testing.T) {
	dir := t.TempDir()

	bareDir := filepath.Join(dir, "bare")
	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	repoDir := filepath.Join(reposDir, "myrepo")

	initBareRemoteWithClone(t, bareDir, repoDir, "master")

	// Push a new commit via a separate clone.
	pusher := t.TempDir()
	run(t, pusher, "git", "clone", bareDir, ".")
	run(t, pusher, "git", "config", "user.email", "test@example.com")
	run(t, pusher, "git", "config", "user.name", "Test User")
	run(t, pusher, "git", "commit", "--allow-empty", "-m", "new work")
	run(t, pusher, "git", "push", "origin", "master")

	latestCommit := gitHeadCommit(t, pusher)

	dst := filepath.Join(workspacesDir, "test-task")
	_, err := addGitWorktree(repoDir, dst, "test-task")
	if err != nil {
		t.Fatalf("addGitWorktree: %v", err)
	}

	worktreeHead := gitHeadCommit(t, dst)
	if worktreeHead != latestCommit {
		t.Errorf("worktree HEAD = %s, want %s (latest remote master)", worktreeHead, latestCommit)
	}
}

func TestDetectGitDefaultBranchMain(t *testing.T) {
	dir := t.TempDir()

	bareDir := filepath.Join(dir, "bare")
	cloneDir := filepath.Join(dir, "clone")

	initBareRemoteWithClone(t, bareDir, cloneDir, "main")

	branch := detectGitDefaultBranch(cloneDir)
	if branch != "origin/main" {
		t.Errorf("detectGitDefaultBranch = %q, want %q", branch, "origin/main")
	}
}

func TestDetectGitDefaultBranchMaster(t *testing.T) {
	dir := t.TempDir()

	bareDir := filepath.Join(dir, "bare")
	cloneDir := filepath.Join(dir, "clone")

	initBareRemoteWithClone(t, bareDir, cloneDir, "master")

	branch := detectGitDefaultBranch(cloneDir)
	if branch != "origin/master" {
		t.Errorf("detectGitDefaultBranch = %q, want %q", branch, "origin/master")
	}
}

func TestDetectGitDefaultBranchNoRemote(t *testing.T) {
	dir := t.TempDir()

	// A local-only git repo with no remote.
	initGitRepo(t, dir)

	branch := detectGitDefaultBranch(dir)
	if branch != "" {
		t.Errorf("detectGitDefaultBranch = %q, want empty string", branch)
	}
}

func TestFetchGitRemoteNoNetwork(t *testing.T) {
	dir := t.TempDir()

	initGitRepo(t, dir)
	// Add a bogus remote.
	run(t, dir, "git", "remote", "add", "origin", "https://invalid.example.com/no-such-repo.git")

	err := fetchGitRemote(dir)
	if err == nil {
		t.Error("fetchGitRemote should return error for unreachable remote")
	}
}

func TestGitWorktreeFallbackOnNoRemote(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")

	repoDir := filepath.Join(reposDir, "myrepo")
	initGitRepo(t, repoDir)

	localHead := gitHeadCommit(t, repoDir)

	// No remote — should still create a workspace from HEAD.
	dst := filepath.Join(workspacesDir, "fallback-task")
	_, err := addGitWorktree(repoDir, dst, "fallback-task")
	if err != nil {
		t.Fatalf("addGitWorktree: %v", err)
	}

	worktreeHead := gitHeadCommit(t, dst)
	if worktreeHead != localHead {
		t.Errorf("worktree HEAD = %s, want %s (local HEAD fallback)", worktreeHead, localHead)
	}
}

func TestPresentReposFiltersNonRepos(t *testing.T) {
	dir := t.TempDir()
	wsDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a real repo subdir.
	repoDir := filepath.Join(wsDir, "alpha")
	initGitRepo(t, repoDir)

	// Create a plain directory (not a repo).
	if err := os.MkdirAll(filepath.Join(wsDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a root-level file.
	if err := os.WriteFile(filepath.Join(wsDir, "CLAUDE.md"), []byte("# Instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos := PresentRepos(wsDir)
	if len(repos) != 1 || repos[0] != "alpha" {
		t.Errorf("PresentRepos = %v, want [alpha]", repos)
	}
}

func TestNonRepoItems(t *testing.T) {
	dir := t.TempDir()
	wsDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a managed repo.
	initGitRepo(t, filepath.Join(wsDir, "alpha"))

	// Create non-repo items.
	if err := os.MkdirAll(filepath.Join(wsDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "CLAUDE.md"), []byte("# Instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "deploy.sh"), []byte("#!/bin/bash\necho hi"), 0o755); err != nil {
		t.Fatal(err)
	}

	items := NonRepoItems(wsDir, []string{"alpha"})

	// Should contain docs (dir), CLAUDE.md (file), deploy.sh (file) but not alpha.
	byName := make(map[string]string)
	for _, item := range items {
		byName[item.Name] = item.Kind
	}
	if byName["alpha"] != "" {
		t.Error("NonRepoItems should not include managed repo 'alpha'")
	}
	if byName["docs"] != "dir" {
		t.Errorf("docs Kind = %q, want %q", byName["docs"], "dir")
	}
	if byName["CLAUDE.md"] != "file" {
		t.Errorf("CLAUDE.md Kind = %q, want %q", byName["CLAUDE.md"], "file")
	}
	if byName["deploy.sh"] != "file" {
		t.Errorf("deploy.sh Kind = %q, want %q", byName["deploy.sh"], "file")
	}
}

func TestCopyNonRepoItems(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "source")
	dstDir := filepath.Join(dir, "dest")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Managed repo — should be skipped.
	initGitRepo(t, filepath.Join(srcDir, "alpha"))

	// Plain directory with a nested file.
	if err := os.MkdirAll(filepath.Join(srcDir, "docs", "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "docs", "design", "plan.md"), []byte("the plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Root-level file.
	if err := os.WriteFile(filepath.Join(srcDir, "CLAUDE.md"), []byte("# Instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Symlink.
	if err := os.Symlink("CLAUDE.md", filepath.Join(srcDir, "link.md")); err != nil {
		t.Fatal(err)
	}

	err := CopyNonRepoItems(srcDir, dstDir, []string{"alpha"})
	if err != nil {
		t.Fatalf("CopyNonRepoItems: %v", err)
	}

	// Managed repo should NOT be copied.
	if _, err := os.Stat(filepath.Join(dstDir, "alpha")); !os.IsNotExist(err) {
		t.Error("managed repo 'alpha' should not be copied")
	}

	// Plain directory should be copied.
	data, err := os.ReadFile(filepath.Join(dstDir, "docs", "design", "plan.md"))
	if err != nil {
		t.Fatalf("reading copied docs: %v", err)
	}
	if string(data) != "the plan" {
		t.Errorf("plan.md = %q, want %q", string(data), "the plan")
	}

	// Root-level file should be copied.
	data, err = os.ReadFile(filepath.Join(dstDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading copied CLAUDE.md: %v", err)
	}
	if string(data) != "# Instructions" {
		t.Errorf("CLAUDE.md = %q, want %q", string(data), "# Instructions")
	}

	// Symlink should be preserved.
	target, err := os.Readlink(filepath.Join(dstDir, "link.md"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "CLAUDE.md" {
		t.Errorf("symlink target = %q, want %q", target, "CLAUDE.md")
	}
}

func TestCopyNonRepoItemsWithOneOffClone(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "source")
	dstDir := filepath.Join(dir, "dest")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Managed repo — will be in the managed list.
	initGitRepo(t, filepath.Join(srcDir, "alpha"))

	// One-off clone — has .git but NOT in the managed list.
	initGitRepo(t, filepath.Join(srcDir, "random-clone"))
	if err := os.WriteFile(filepath.Join(srcDir, "random-clone", "readme.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CopyNonRepoItems(srcDir, dstDir, []string{"alpha"})
	if err != nil {
		t.Fatalf("CopyNonRepoItems: %v", err)
	}

	// One-off clone should be copied (including its .git).
	data, err := os.ReadFile(filepath.Join(dstDir, "random-clone", "readme.md"))
	if err != nil {
		t.Fatalf("reading copied one-off clone: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("readme.md = %q, want %q", string(data), "hello")
	}

	// Its .git should also be copied.
	if _, err := os.Stat(filepath.Join(dstDir, "random-clone", ".git")); err != nil {
		t.Errorf("one-off clone .git should be copied: %v", err)
	}

	// Managed repo should NOT be copied.
	if _, err := os.Stat(filepath.Join(dstDir, "alpha")); !os.IsNotExist(err) {
		t.Error("managed repo 'alpha' should not be copied")
	}
}

func TestMultiRepoForkWithNonRepoItems(t *testing.T) {
	dir := t.TempDir()

	reposDir := filepath.Join(dir, "repos")
	workspacesDir := filepath.Join(dir, "workspaces")
	mkdirs(t, reposDir)

	initGitRepo(t, filepath.Join(reposDir, "alpha"))
	initGitRepo(t, filepath.Join(reposDir, "beta"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	// Create source workspace with two repos.
	srcResult, err := Create(rs, "source-task", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Create source: %v", err)
	}

	// Add non-repo items to the source workspace.
	if err := os.MkdirAll(filepath.Join(srcResult.WorkspaceDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcResult.WorkspaceDir, "docs", "design.md"), []byte("design doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcResult.WorkspaceDir, "CLAUDE.md"), []byte("# Instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fork each repo.
	forkDir := filepath.Join(workspacesDir, "fork-task")
	if err := os.MkdirAll(forkDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, repo := range []string{"alpha", "beta"} {
		dstPath := filepath.Join(forkDir, repo)
		result := ForkRepo(rs, srcResult.WorkspaceDir, dstPath, repo, "fork-task")
		if result.Err != nil {
			t.Fatalf("ForkRepo %s: %v", repo, result.Err)
		}
	}

	// Copy non-repo items.
	managedRepos := []string{"alpha", "beta"}
	if err := CopyNonRepoItems(srcResult.WorkspaceDir, forkDir, managedRepos); err != nil {
		t.Fatalf("CopyNonRepoItems: %v", err)
	}

	// Verify repos are worktrees.
	for _, repo := range []string{"alpha", "beta"} {
		gitPath := filepath.Join(forkDir, repo, ".git")
		info, err := os.Lstat(gitPath)
		if err != nil {
			t.Fatalf("%s: expected .git: %v", repo, err)
		}
		if info.IsDir() {
			t.Errorf("%s: .git should be a file (worktree), not a directory", repo)
		}
	}

	// Verify non-repo items are copied.
	data, err := os.ReadFile(filepath.Join(forkDir, "docs", "design.md"))
	if err != nil {
		t.Fatalf("reading copied docs: %v", err)
	}
	if string(data) != "design doc" {
		t.Errorf("design.md = %q, want %q", string(data), "design doc")
	}

	data, err = os.ReadFile(filepath.Join(forkDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading copied CLAUDE.md: %v", err)
	}
	if string(data) != "# Instructions" {
		t.Errorf("CLAUDE.md = %q, want %q", string(data), "# Instructions")
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, output)
	}
}
