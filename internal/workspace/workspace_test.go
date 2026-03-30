package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
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
		Repos:             map[string]RepoConfig{},
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

	initGitRepo(t, filepath.Join(reposDir, "gonfalon"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Repos:             map[string]RepoConfig{},
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "fix-auth", []string{"gonfalon"})
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
	nestedDir := filepath.Join(expectedDir, "gonfalon")
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
				Repos:             map[string]RepoConfig{},
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
				Repos:             map[string]RepoConfig{},
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
		Repos:         map[string]RepoConfig{},
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
		Repos:             map[string]RepoConfig{},
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
		Repos:             map[string]RepoConfig{},
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
		Repos:             map[string]RepoConfig{},
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
		Repos:             map[string]RepoConfig{},
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

	initGitRepo(t, filepath.Join(reposDir, "gonfalon"))

	rs := &RepoSets{
		MetarepoDir:       dir,
		WorkspaceStrategy: StrategySingleRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Repos:             map[string]RepoConfig{},
		Sets:              map[string][]string{},
	}

	result, err := Create(rs, "single-destroy", []string{"gonfalon"})
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

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run(t, dir, "git", "init")
	run(t, dir, "git", "commit", "--allow-empty", "-m", "init")
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
