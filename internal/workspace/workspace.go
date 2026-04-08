package workspace

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
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
		err = createGitWorktree(repoSrc, workspaceDir, taskName)
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
			err = createGitWorktree(repoSrc, repoDst, taskName)
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
			err = createGitWorktree(repoSrc, repoDst, taskName)
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

// ForgetRepo cleans up a single repo's workspace. For jj repos, runs
// jj workspace forget. For git repos, removes the worktree and branch.
func ForgetRepo(rs *RepoSets, workspaceDir, repoName string) DestroyRepoResult {
	vcs := rs.DetectVCS(repoName)
	repoSrc := filepath.Join(rs.ReposDir, repoName)
	workspaceName := filepath.Base(workspaceDir)

	switch vcs {
	case "jj":
		output, err := forgetJJWorkspaceOutput(repoSrc, workspaceName)
		return DestroyRepoResult{Repo: repoName, VCS: vcs, Output: output, Err: err}
	default:
		worktreePath := filepath.Join(workspaceDir, repoName)
		if rs.WorkspaceStrategy == StrategySingleRepo {
			worktreePath = workspaceDir
		}
		output, err := removeGitWorktree(repoSrc, worktreePath, workspaceName)
		return DestroyRepoResult{Repo: repoName, VCS: vcs, Output: output, Err: err}
	}
}

// ForgetSingleRepoWorkspace cleans up a single_repo workspace by
// trying all known repos until one succeeds.
func ForgetSingleRepoWorkspace(rs *RepoSets, workspaceDir string) DestroyRepoResult {
	repos, _ := rs.ListRepos()
	workspaceName := filepath.Base(workspaceDir)
	for _, repo := range repos {
		repoSrc := filepath.Join(rs.ReposDir, repo)
		switch rs.DetectVCS(repo) {
		case "jj":
			output, err := forgetJJWorkspaceOutput(repoSrc, workspaceName)
			if err == nil {
				return DestroyRepoResult{Repo: repo, VCS: "jj", Output: output}
			}
		default:
			output, err := removeGitWorktree(repoSrc, workspaceDir, workspaceName)
			if err == nil {
				return DestroyRepoResult{Repo: repo, VCS: "git", Output: output}
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
// the workspace first. For git repos, it removes the worktree and
// branch. The RepoSets parameter is needed to find source repos;
// pass nil to skip VCS cleanup.
func Destroy(rs *RepoSets, workspaceDir string) error {
	if rs != nil {
		workspaceName := filepath.Base(workspaceDir)

		// Multi-repo: clean up each repo subdirectory.
		entries, err := os.ReadDir(workspaceDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				repoName := entry.Name()
				repoSrc := filepath.Join(rs.ReposDir, repoName)
				switch rs.DetectVCS(repoName) {
				case "jj":
					_ = forgetJJWorkspace(repoSrc, workspaceName)
				default:
					worktreePath := filepath.Join(workspaceDir, repoName)
					_, _ = removeGitWorktree(repoSrc, worktreePath, workspaceName)
				}
			}
		}

		// For single_repo mode, the workspace dir itself is the repo.
		if rs.WorkspaceStrategy == StrategySingleRepo {
			repos, _ := rs.ListRepos()
			for _, repo := range repos {
				repoSrc := filepath.Join(rs.ReposDir, repo)
				switch rs.DetectVCS(repo) {
				case "jj":
					_ = forgetJJWorkspace(repoSrc, workspaceName)
				default:
					_, _ = removeGitWorktree(repoSrc, workspaceDir, workspaceName)
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
		output, err = addGitWorktree(repoSrc, dst, taskName)
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

	// Fetch latest from origin so the workspace isn't based on stale state.
	_ = fetchJJRemote(repoSrc)

	// jj workspace add must be run from the source repo.
	args := []string{"workspace", "add", repoDst, "--name", workspaceName}

	// Base the workspace on the remote default branch if we can detect it.
	if bookmark := detectJJDefaultBookmark(repoSrc); bookmark != "" {
		args = append(args, "-r", bookmark)
	}

	cmd := exec.Command("jj", args...)
	cmd.Dir = repoSrc
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("jj workspace add: %w: %s", err, output)
	}
	return string(output), nil
}

func createGitWorktree(repoSrc, repoDst, taskName string) error {
	_, err := addGitWorktree(repoSrc, repoDst, taskName)
	return err
}

// addGitWorktree creates a git worktree at repoDst branching from repoSrc.
// The branch is named krang/<taskName> to make it identifiable for cleanup.
// It fetches from origin first and bases the worktree on the remote default
// branch so that new workspaces start from up-to-date code.
func addGitWorktree(repoSrc, repoDst, taskName string) (string, error) {
	// Fetch latest from origin so the worktree isn't based on a stale
	// local HEAD. Non-fatal — proceed with whatever we have on failure.
	_ = fetchGitRemote(repoSrc)

	// Determine the remote default branch (e.g. "origin/main").
	// If found, delegate to addGitWorktreeAt so the worktree starts there.
	if base := detectGitDefaultBranch(repoSrc); base != "" {
		return addGitWorktreeAt(repoSrc, repoDst, taskName, base)
	}

	// Fallback: no remote or detection failed — branch from HEAD.
	branchName := "krang/" + taskName

	// Prune stale worktree entries that might block creation.
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = repoSrc
	_ = pruneCmd.Run()

	// Clean up stale branch from a previous crashed task with the same name.
	checkCmd := exec.Command("git", "rev-parse", "--verify", branchName)
	checkCmd.Dir = repoSrc
	if checkCmd.Run() == nil {
		delCmd := exec.Command("git", "branch", "-D", branchName)
		delCmd.Dir = repoSrc
		_ = delCmd.Run()
	}

	cmd := exec.Command("git", "worktree", "add", "-b", branchName, repoDst)
	cmd.Dir = repoSrc
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git worktree add: %w: %s", err, output)
	}

	result := string(output)

	// Copy files listed in .worktreeinclude (e.g., .env).
	if inclErr := processWorktreeInclude(repoSrc, repoDst); inclErr != nil {
		// Non-fatal: log but don't fail workspace creation.
		result += "\nworktreeinclude warning: " + inclErr.Error()
	}

	return result, nil
}

// addGitWorktreeAt creates a git worktree at a specific commit.
func addGitWorktreeAt(repoSrc, repoDst, taskName, commitish string) (string, error) {
	branchName := "krang/" + taskName

	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = repoSrc
	_ = pruneCmd.Run()

	checkCmd := exec.Command("git", "rev-parse", "--verify", branchName)
	checkCmd.Dir = repoSrc
	if checkCmd.Run() == nil {
		delCmd := exec.Command("git", "branch", "-D", branchName)
		delCmd.Dir = repoSrc
		_ = delCmd.Run()
	}

	cmd := exec.Command("git", "worktree", "add", "-b", branchName, repoDst, commitish)
	cmd.Dir = repoSrc
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git worktree add: %w: %s", err, output)
	}

	result := string(output)

	// Copy files listed in .worktreeinclude (e.g., .env).
	if inclErr := processWorktreeInclude(repoSrc, repoDst); inclErr != nil {
		result += "\nworktreeinclude warning: " + inclErr.Error()
	}

	return result, nil
}

// fetchGitRemote runs "git fetch origin" in repoDir.
// Non-fatal — callers should log the error and continue.
func fetchGitRemote(repoDir string) error {
	cmd := exec.Command("git", "fetch", "origin")
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch origin: %w: %s", err, output)
	}
	return nil
}

// detectGitDefaultBranch returns the remote tracking ref for the default
// branch (e.g. "origin/main"). Returns "" if detection fails entirely.
func detectGitDefaultBranch(repoDir string) string {
	// Try reading the locally cached default branch.
	if branch := gitSymbolicOriginHead(repoDir); branch != "" {
		return branch
	}

	// Auto-detect from the remote and cache locally.
	setHead := exec.Command("git", "remote", "set-head", "origin", "-a")
	setHead.Dir = repoDir
	if setHead.Run() == nil {
		if branch := gitSymbolicOriginHead(repoDir); branch != "" {
			return branch
		}
	}

	// Heuristic fallback: check for common branch names.
	for _, name := range []string{"origin/main", "origin/master"} {
		check := exec.Command("git", "rev-parse", "--verify", name)
		check.Dir = repoDir
		if check.Run() == nil {
			return name
		}
	}

	return ""
}

// gitSymbolicOriginHead reads refs/remotes/origin/HEAD and returns the
// remote tracking ref (e.g. "origin/main"), or "" if unset.
func gitSymbolicOriginHead(repoDir string) string {
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Output is "refs/remotes/origin/main\n" — strip prefix to get "origin/main".
	ref := strings.TrimSpace(string(out))
	return strings.TrimPrefix(ref, "refs/remotes/")
}

// fetchJJRemote runs "jj git fetch" in repoDir.
// Non-fatal — callers should ignore the error and continue.
func fetchJJRemote(repoDir string) error {
	cmd := exec.Command("jj", "git", "fetch")
	cmd.Dir = repoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("jj git fetch: %w: %s", err, output)
	}
	return nil
}

// detectJJDefaultBookmark returns the bookmark name to use as the
// workspace base (e.g. "main@origin"). Returns "" if neither main nor
// master bookmarks exist at origin.
func detectJJDefaultBookmark(repoDir string) string {
	for _, candidate := range []string{"main@origin", "master@origin"} {
		// Use jj log to check if the revset resolves.
		cmd := exec.Command("jj", "log", "-r", candidate, "--no-graph", "--limit", "1")
		cmd.Dir = repoDir
		if cmd.Run() == nil {
			return candidate
		}
	}
	return ""
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

	// Resolve the source repo from the worktree's .git file.
	repoSrc, err := resolveGitWorktreeSource(srcDir)
	if err != nil {
		return "", fmt.Errorf("resolving source repo: %w", err)
	}

	// Get the current HEAD of the source worktree.
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = srcDir
	headOut, err := headCmd.CombinedOutput()
	allOutput.WriteString(string(headOut))
	if err != nil {
		return allOutput.String(), fmt.Errorf("git rev-parse HEAD: %w: %s", err, headOut)
	}
	commitish := strings.TrimSpace(string(headOut))

	// Create a new worktree at the same commit.
	wtOut, err := addGitWorktreeAt(repoSrc, dstDir, forkTaskName, commitish)
	allOutput.WriteString(wtOut)
	if err != nil {
		return allOutput.String(), err
	}

	// Overlay working tree state from the source, preserving the
	// fork's .git pointer file.
	if err := copyTreeExcluding(srcDir, dstDir, []string{".git"}); err != nil {
		return allOutput.String(), fmt.Errorf("copying working tree: %w", err)
	}

	return allOutput.String(), nil
}

// resolveGitWorktreeSource finds the main repository directory from a
// worktree's .git file. Worktrees have a .git file (not directory)
// containing "gitdir: /path/to/.git/worktrees/<name>". We walk up
// from there to find the repo root. Falls back to srcDir itself if
// .git is a directory (regular repo, not a worktree).
func resolveGitWorktreeSource(worktreeDir string) (string, error) {
	gitPath := filepath.Join(worktreeDir, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		return "", fmt.Errorf("stat .git: %w", err)
	}

	// Regular git repo (not a worktree) — .git is a directory.
	if info.IsDir() {
		return worktreeDir, nil
	}

	// Worktree — .git is a file with "gitdir: <path>".
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", fmt.Errorf("reading .git file: %w", err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return "", fmt.Errorf("unexpected .git file content: %s", line)
	}
	gitdir := strings.TrimPrefix(line, "gitdir: ")
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktreeDir, gitdir)
	}

	// gitdir points to .git/worktrees/<name>. The repo root's .git
	// is two levels up.
	dotGit := filepath.Dir(filepath.Dir(gitdir))
	return filepath.Dir(dotGit), nil
}

// removeGitWorktree removes a git worktree and attempts to delete its
// branch. Uses git branch -d (not -D) so unpushed branches are kept.
func removeGitWorktree(repoSrc, worktreePath, taskName string) (string, error) {
	var allOutput strings.Builder
	branchName := "krang/" + taskName

	// If the worktree directory is already gone, prune stale entries.
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		pruneCmd := exec.Command("git", "worktree", "prune")
		pruneCmd.Dir = repoSrc
		_ = pruneCmd.Run()
	} else {
		rmCmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
		rmCmd.Dir = repoSrc
		rmOut, err := rmCmd.CombinedOutput()
		allOutput.WriteString(string(rmOut))
		if err != nil {
			// If removal fails, try pruning then force remove.
			pruneCmd := exec.Command("git", "worktree", "prune")
			pruneCmd.Dir = repoSrc
			_ = pruneCmd.Run()
		}
	}

	// Try to delete the branch. Use -d (not -D) so git refuses to
	// delete branches with unpushed commits.
	delCmd := exec.Command("git", "branch", "-d", branchName)
	delCmd.Dir = repoSrc
	delOut, err := delCmd.CombinedOutput()
	allOutput.WriteString(string(delOut))
	if err != nil {
		// Branch has unpushed commits or doesn't exist — not fatal.
		allOutput.WriteString(fmt.Sprintf("(branch %s kept: %s)", branchName, strings.TrimSpace(string(delOut))))
	}

	return allOutput.String(), nil
}

// HasUncommittedChanges checks whether a git worktree has modified,
// staged, or untracked files.
func HasUncommittedChanges(worktreeDir string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktreeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// HasUnpushedCommits checks whether a git worktree has commits that
// don't exist on any remote-tracking branch.
func HasUnpushedCommits(worktreeDir string) bool {
	// Show commits on HEAD that aren't reachable from any remote ref.
	// This works regardless of whether the branch has an upstream.
	cmd := exec.Command("git", "log", "--oneline", "HEAD", "--not", "--remotes")
	cmd.Dir = worktreeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// copyTreeExcluding copies all files and directories from src to dst,
// skipping any top-level entries whose names appear in the exclude list.
// Existing files in dst are overwritten.
func copyTreeExcluding(src, dst string, exclude []string) error {
	excludeSet := make(map[string]bool, len(exclude))
	for _, name := range exclude {
		excludeSet[name] = true
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		// Skip excluded top-level entries.
		topLevel := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if excludeSet[topLevel] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		dstPath := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		// Handle symlinks.
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(dstPath)
			return os.Symlink(target, dstPath)
		}

		return copyFile(path, dstPath)
	})
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// processWorktreeInclude reads a .worktreeinclude file from the source
// repo and copies matching gitignored files into the worktree. The file
// uses gitignore-style glob patterns (one per line).
func processWorktreeInclude(repoSrc, worktreeDst string) error {
	includePath := filepath.Join(repoSrc, ".worktreeinclude")
	f, err := os.Open(includePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		pattern := strings.TrimSpace(scanner.Text())
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}

		matches, err := filepath.Glob(filepath.Join(repoSrc, pattern))
		if err != nil {
			continue
		}

		for _, match := range matches {
			rel, err := filepath.Rel(repoSrc, match)
			if err != nil {
				continue
			}
			dst := filepath.Join(worktreeDst, rel)

			// Skip if already exists in the worktree (tracked file).
			if _, err := os.Stat(dst); err == nil {
				continue
			}

			info, err := os.Stat(match)
			if err != nil {
				continue
			}

			if info.IsDir() {
				if err := copyTreeExcluding(match, dst, nil); err != nil {
					continue
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					continue
				}
				if err := copyFile(match, dst); err != nil {
					continue
				}
			}
		}
	}

	return scanner.Err()
}
