package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type CreateResult struct {
	WorkspaceDir string
	Created      map[string]string // repo name → VCS used
	Errors       []string
}

// Create makes a workspace for the given task. For single_repo mode,
// the workspace dir IS the repo clone directly. For multi_repo mode,
// the workspace dir contains subdirectories for each repo.
func Create(rs *RepoSets, taskName string, repos []string) (*CreateResult, error) {
	workspaceDir := filepath.Join(rs.WorkspacesDir, taskName)

	if _, err := os.Stat(workspaceDir); err == nil {
		return nil, fmt.Errorf("workspace directory already exists: %s", workspaceDir)
	}

	if len(repos) == 0 {
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating empty workspace: %w", err)
		}
		return &CreateResult{
			WorkspaceDir: workspaceDir,
			Created:      map[string]string{},
		}, nil
	}

	if rs.WorkspaceStrategy == StrategySingleRepo {
		return createSingleRepo(rs, taskName, workspaceDir, repos[0])
	}
	return createMultiRepo(rs, taskName, workspaceDir, repos)
}

func createSingleRepo(rs *RepoSets, taskName, workspaceDir, repo string) (*CreateResult, error) {
	// Ensure the parent (workspaces/) dir exists.
	if err := os.MkdirAll(filepath.Dir(workspaceDir), 0o755); err != nil {
		return nil, fmt.Errorf("creating workspaces directory: %w", err)
	}

	vcs := rs.DetectVCS(repo)
	repoSrc := filepath.Join(rs.ReposDir, repo)

	var err error
	switch vcs {
	case "jj":
		err = createJJWorkspace(repoSrc, workspaceDir, taskName)
	default:
		err = createGitClone(repoSrc, workspaceDir)
	}
	if err != nil {
		return nil, fmt.Errorf("%s (%s): %w", repo, vcs, err)
	}

	return &CreateResult{
		WorkspaceDir: workspaceDir,
		Created:      map[string]string{repo: vcs},
	}, nil
}

func createMultiRepo(rs *RepoSets, taskName, workspaceDir string, repos []string) (*CreateResult, error) {
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating workspace directory: %w", err)
	}

	result := &CreateResult{
		WorkspaceDir: workspaceDir,
		Created:      make(map[string]string),
	}

	for _, repo := range repos {
		vcs := rs.DetectVCS(repo)
		repoSrc := filepath.Join(rs.ReposDir, repo)
		repoDst := filepath.Join(workspaceDir, repo)

		var err error
		switch vcs {
		case "jj":
			err = createJJWorkspace(repoSrc, repoDst, taskName)
		default:
			err = createGitClone(repoSrc, repoDst)
		}

		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s (%s): %v", repo, vcs, err))
			continue
		}
		result.Created[repo] = vcs
	}

	if len(result.Created) == 0 && len(result.Errors) > 0 {
		_ = os.RemoveAll(workspaceDir)
		return nil, fmt.Errorf("all repos failed: %s", strings.Join(result.Errors, "; "))
	}

	return result, nil
}

// AddRepos adds new repos to an existing multi_repo workspace.
func AddRepos(rs *RepoSets, workspaceDir, taskName string, repos []string) (*CreateResult, error) {
	result := &CreateResult{
		WorkspaceDir: workspaceDir,
		Created:      make(map[string]string),
	}

	for _, repo := range repos {
		vcs := rs.DetectVCS(repo)
		repoSrc := filepath.Join(rs.ReposDir, repo)
		repoDst := filepath.Join(workspaceDir, repo)

		var err error
		switch vcs {
		case "jj":
			err = createJJWorkspace(repoSrc, repoDst, taskName)
		default:
			err = createGitClone(repoSrc, repoDst)
		}

		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s (%s): %v", repo, vcs, err))
			continue
		}
		result.Created[repo] = vcs
	}

	return result, nil
}

// PresentRepos returns the names of repo subdirectories already
// present in a multi_repo workspace.
func PresentRepos(workspaceDir string) []string {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return nil
	}
	var repos []string
	for _, e := range entries {
		if e.IsDir() {
			repos = append(repos, e.Name())
		}
	}
	return repos
}

// DestroyRepoResult holds the outcome of forgetting a single repo's workspace.
type DestroyRepoResult struct {
	Repo   string
	VCS    string
	Output string
	Err    error
}

// ForgetRepo runs jj workspace forget for a single repo. Returns
// immediately with a no-op result for git repos.
func ForgetRepo(rs *RepoSets, workspaceDir, repoName string) DestroyRepoResult {
	vcs := rs.DetectVCS(repoName)
	if vcs != "jj" {
		return DestroyRepoResult{Repo: repoName, VCS: vcs}
	}
	repoSrc := filepath.Join(rs.ReposDir, repoName)
	workspaceName := filepath.Base(workspaceDir)
	output, err := forgetJJWorkspaceOutput(repoSrc, workspaceName)
	return DestroyRepoResult{Repo: repoName, VCS: vcs, Output: output, Err: err}
}

// ForgetSingleRepoWorkspace tries to forget jj workspaces for a
// single_repo workspace by checking all known repos.
func ForgetSingleRepoWorkspace(rs *RepoSets, workspaceDir string) DestroyRepoResult {
	repos, _ := rs.ListRepos()
	workspaceName := filepath.Base(workspaceDir)
	for _, repo := range repos {
		if rs.DetectVCS(repo) == "jj" {
			repoSrc := filepath.Join(rs.ReposDir, repo)
			output, err := forgetJJWorkspaceOutput(repoSrc, workspaceName)
			if err == nil {
				return DestroyRepoResult{Repo: repo, VCS: "jj", Output: output}
			}
		}
	}
	return DestroyRepoResult{Repo: filepath.Base(workspaceDir), VCS: "unknown"}
}

// RemoveWorkspaceDir removes the workspace directory after repos have
// been forgotten.
func RemoveWorkspaceDir(workspaceDir string) error {
	return os.RemoveAll(workspaceDir)
}

// DestroyRepoList returns the list of repo subdirectories in a
// multi_repo workspace that need cleanup. Only directories that
// look like repo clones (contain .git or .jj) are included.
func DestroyRepoList(workspaceDir string) []string {
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return nil
	}
	var repos []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(workspaceDir, e.Name())
		if isRepoDir(sub) {
			repos = append(repos, e.Name())
		}
	}
	return repos
}

func isRepoDir(dir string) bool {
	for _, marker := range []string{".jj", ".git"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// Destroy removes a workspace directory. For jj repos, it forgets
// the workspace first. The RepoSets parameter is needed to find the
// source repos for jj workspace forget; pass nil to skip jj cleanup.
func Destroy(rs *RepoSets, workspaceDir string) error {
	if rs != nil {
		// Try to forget jj workspaces for any repos that were jj-linked.
		entries, err := os.ReadDir(workspaceDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				repoName := entry.Name()
				if rs.DetectVCS(repoName) == "jj" {
					repoSrc := filepath.Join(rs.ReposDir, repoName)
					workspaceName := filepath.Base(workspaceDir)
					_ = forgetJJWorkspace(repoSrc, workspaceName)
				}
			}
		}

		// For single_repo mode, the workspace dir itself is the repo.
		if rs.WorkspaceStrategy == StrategySingleRepo {
			// Try to detect which repo this was by checking all known repos.
			repos, _ := rs.ListRepos()
			workspaceName := filepath.Base(workspaceDir)
			for _, repo := range repos {
				if rs.DetectVCS(repo) == "jj" {
					repoSrc := filepath.Join(rs.ReposDir, repo)
					_ = forgetJJWorkspace(repoSrc, workspaceName)
				}
			}
		}
	}

	return os.RemoveAll(workspaceDir)
}

func forgetJJWorkspace(repoSrc, workspaceName string) error {
	_, err := forgetJJWorkspaceOutput(repoSrc, workspaceName)
	return err
}

func forgetJJWorkspaceOutput(repoSrc, workspaceName string) (string, error) {
	cmd := exec.Command("jj", "workspace", "forget", workspaceName)
	cmd.Dir = repoSrc
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("jj workspace forget: %w: %s", err, output)
	}
	return string(output), nil
}

// CreateWorkspaceDir creates the workspace directory structure. For
// single_repo mode it creates the parent; for multi_repo it creates
// the workspace dir itself.
func CreateWorkspaceDir(rs *RepoSets, taskName string) (string, error) {
	workspaceDir := filepath.Join(rs.WorkspacesDir, taskName)

	if _, err := os.Stat(workspaceDir); err == nil {
		return "", fmt.Errorf("workspace directory already exists: %s", workspaceDir)
	}

	if rs.WorkspaceStrategy == StrategySingleRepo {
		if err := os.MkdirAll(filepath.Dir(workspaceDir), 0o755); err != nil {
			return "", fmt.Errorf("creating workspaces directory: %w", err)
		}
	} else {
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			return "", fmt.Errorf("creating workspace directory: %w", err)
		}
	}

	return workspaceDir, nil
}

// CloneRepoResult holds the outcome of a single repo clone operation.
type CloneRepoResult struct {
	Repo   string
	VCS    string
	Output string // combined stdout+stderr from the clone command
	Err    error
}

// CloneRepo clones or creates a workspace for a single repo. For
// single_repo mode, dst should be the workspace dir itself. For
// multi_repo mode, dst should be workspaceDir/repo.
func CloneRepo(rs *RepoSets, taskName, dst, repo string) CloneRepoResult {
	vcs := rs.DetectVCS(repo)
	repoSrc := filepath.Join(rs.ReposDir, repo)

	var output string
	var err error
	switch vcs {
	case "jj":
		output, err = cloneJJWorkspace(repoSrc, dst, taskName)
	default:
		output, err = cloneGitRepo(repoSrc, dst)
	}

	return CloneRepoResult{
		Repo:   repo,
		VCS:    vcs,
		Output: output,
		Err:    err,
	}
}

// RepoDst returns the destination path for a repo within a workspace.
func RepoDst(rs *RepoSets, workspaceDir, repo string) string {
	if rs.WorkspaceStrategy == StrategySingleRepo {
		return workspaceDir
	}
	return filepath.Join(workspaceDir, repo)
}

func createJJWorkspace(repoSrc, repoDst, workspaceName string) error {
	_, err := cloneJJWorkspace(repoSrc, repoDst, workspaceName)
	return err
}

func cloneJJWorkspace(repoSrc, repoDst, workspaceName string) (string, error) {
	// Ensure the source repo's working copy is up to date — a stale
	// working copy causes "jj workspace add" to fail.
	updateCmd := exec.Command("jj", "workspace", "update-stale")
	updateCmd.Dir = repoSrc
	_ = updateCmd.Run() // safe no-op if not stale

	// jj workspace add must be run from the source repo.
	cmd := exec.Command("jj", "workspace", "add", repoDst, "--name", workspaceName)
	cmd.Dir = repoSrc
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("jj workspace add: %w: %s", err, output)
	}
	return string(output), nil
}

func createGitClone(repoSrc, repoDst string) error {
	_, err := cloneGitRepo(repoSrc, repoDst)
	return err
}

func cloneGitRepo(repoSrc, repoDst string) (string, error) {
	// Local clone uses hardlinks for objects — fast and space-efficient.
	cmd := exec.Command("git", "clone", repoSrc, repoDst)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git clone: %w: %s", err, output)
	}
	return string(output), nil
}

// ForkRepoResult holds the outcome of forking a single repo.
type ForkRepoResult struct {
	Repo   string
	VCS    string
	Output string
	Err    error
}

// ForkRepo forks a single repo from srcWorkspaceDir into dstPath.
// For jj repos, creates an independent duplicate (sibling commits).
// For git repos, creates a physical copy with a new branch.
func ForkRepo(rs *RepoSets, srcWorkspaceDir, dstPath, repo, forkTaskName string) ForkRepoResult {
	vcs := rs.DetectVCS(repo)

	// Resolve source path: single_repo = workspace dir, multi_repo = subdir.
	srcPath := srcWorkspaceDir
	if rs.WorkspaceStrategy == StrategyMultiRepo {
		srcPath = filepath.Join(srcWorkspaceDir, repo)
	}

	var output string
	var err error
	switch vcs {
	case "jj":
		repoSrc := filepath.Join(rs.ReposDir, repo)
		output, err = forkJJRepoIndependent(repoSrc, srcPath, dstPath, forkTaskName)
	default:
		output, err = forkGitRepo(srcPath, dstPath, forkTaskName)
	}

	return ForkRepoResult{Repo: repo, VCS: vcs, Output: output, Err: err}
}

// AllReposJJ returns true if every repo in the workspace uses jj.
func AllReposJJ(rs *RepoSets, workspaceDir string) bool {
	if rs.WorkspaceStrategy == StrategySingleRepo {
		// Single repo: workspace dir IS the repo. Check for .jj directly.
		_, err := os.Stat(filepath.Join(workspaceDir, ".jj"))
		return err == nil
	}
	repos := PresentRepos(workspaceDir)
	for _, repo := range repos {
		if rs.DetectVCS(repo) != "jj" {
			return false
		}
	}
	return len(repos) > 0
}

func forkJJRepoIndependent(repoSrc, srcWorkspace, dstPath, forkTaskName string) (string, error) {
	var allOutput strings.Builder

	// Ensure source working copy is fresh.
	updateCmd := exec.Command("jj", "workspace", "update-stale")
	updateCmd.Dir = srcWorkspace
	_ = updateCmd.Run()

	// Duplicate the current working-copy commit to create an independent copy.
	// Output format: "Duplicated <old> as <change_id> <commit_id> <desc>"
	dupCmd := exec.Command("jj", "duplicate", "@")
	dupCmd.Dir = srcWorkspace
	dupOut, err := dupCmd.CombinedOutput()
	allOutput.WriteString(string(dupOut))
	if err != nil {
		return allOutput.String(), fmt.Errorf("jj duplicate: %w: %s", err, dupOut)
	}
	dupChangeID := parseDuplicateChangeID(string(dupOut))
	if dupChangeID == "" {
		return allOutput.String(), fmt.Errorf("could not parse change ID from jj duplicate output: %s", dupOut)
	}

	// Create workspace from the source repo (not the workspace).
	wsCmd := exec.Command("jj", "workspace", "add", dstPath, "--name", forkTaskName)
	wsCmd.Dir = repoSrc
	wsOut, err := wsCmd.CombinedOutput()
	allOutput.WriteString(string(wsOut))
	if err != nil {
		return allOutput.String(), fmt.Errorf("jj workspace add: %w: %s", err, wsOut)
	}

	// Switch the new workspace to edit the duplicated commit.
	editCmd := exec.Command("jj", "edit", dupChangeID)
	editCmd.Dir = dstPath
	editOut, err := editCmd.CombinedOutput()
	allOutput.WriteString(string(editOut))
	if err != nil {
		return allOutput.String(), fmt.Errorf("jj edit: %w: %s", err, editOut)
	}

	return allOutput.String(), nil
}

// parseDuplicateChangeID extracts the change ID from jj duplicate output.
// Expected format: "Duplicated <old> as <change_id> <commit_id> <desc>"
func parseDuplicateChangeID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "Duplicated" && fields[2] == "as" {
			return fields[3]
		}
	}
	return ""
}

func forkGitRepo(srcDir, dstDir, forkTaskName string) (string, error) {
	var allOutput strings.Builder

	// Physical copy preserves everything: committed, staged, unstaged, untracked.
	cpCmd := exec.Command("cp", "-a", srcDir, dstDir)
	cpOut, err := cpCmd.CombinedOutput()
	allOutput.WriteString(string(cpOut))
	if err != nil {
		return allOutput.String(), fmt.Errorf("cp -a: %w: %s", err, cpOut)
	}

	// Create a new branch so pushes don't collide with the original.
	branchName := forkTaskName
	gitCmd := exec.Command("git", "checkout", "-b", branchName)
	gitCmd.Dir = dstDir
	gitOut, err := gitCmd.CombinedOutput()
	allOutput.WriteString(string(gitOut))
	if err != nil {
		return allOutput.String(), fmt.Errorf("git checkout -b: %w: %s", err, gitOut)
	}

	return allOutput.String(), nil
}
