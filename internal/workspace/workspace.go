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
	Slots        []RepoProvenance  // provenance of each working copy created
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

	identity := SlotIdentity{TaskName: taskName, RepoName: repo}
	clone := CloneRepoAs(rs, identity, SlotDst(rs, workspaceDir, identity))
	if clone.Err != nil {
		return nil, fmt.Errorf("%s (%s): %w", repo, clone.VCS, clone.Err)
	}

	return &CreateResult{
		WorkspaceDir: workspaceDir,
		Created:      map[string]string{repo: clone.VCS},
		Slots:        []RepoProvenance{clone.Provenance},
	}, nil
}

func createMultiRepo(rs *RepoSets, taskName, workspaceDir string, repos []string) (*CreateResult, error) {
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating workspace directory: %w", err)
	}

	result := cloneInitialSlots(rs, workspaceDir, taskName, repos)

	if len(result.Created) == 0 && len(result.Errors) > 0 {
		_ = os.RemoveAll(workspaceDir)
		return nil, fmt.Errorf("all repos failed: %s", strings.Join(result.Errors, "; "))
	}

	return result, nil
}

// cloneInitialSlots gives each repo its initial working copy in the
// workspace, collecting per-repo failures instead of aborting.
func cloneInitialSlots(rs *RepoSets, workspaceDir, taskName string, repos []string) *CreateResult {
	result := &CreateResult{
		WorkspaceDir: workspaceDir,
		Created:      make(map[string]string),
	}

	for _, repo := range repos {
		identity := SlotIdentity{TaskName: taskName, RepoName: repo}
		clone := CloneRepoAs(rs, identity, SlotDst(rs, workspaceDir, identity))
		if clone.Err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s (%s): %v", repo, clone.VCS, clone.Err))
			continue
		}
		result.Created[repo] = clone.VCS
		result.Slots = append(result.Slots, clone.Provenance)
	}

	return result
}

// AddRepos adds working copies to an existing multi_repo workspace. A
// repo the workspace already holds lands in an auto-numbered slot
// rather than colliding with the copy that's there.
func AddRepos(rs *RepoSets, workspaceDir, taskName string, repos []string) (*CreateResult, error) {
	result := &CreateResult{
		WorkspaceDir: workspaceDir,
		Created:      make(map[string]string),
	}

	for _, repo := range repos {
		identity, err := ResolveSlotIdentity(rs, workspaceDir, taskName, repo, "")
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", repo, err))
			continue
		}
		clone := CloneRepoAs(rs, identity, SlotDst(rs, workspaceDir, identity))
		if clone.Err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s (%s): %v", repo, clone.VCS, clone.Err))
			continue
		}
		result.Created[repo] = clone.VCS
		result.Slots = append(result.Slots, clone.Provenance)
	}

	return result, nil
}

// PresentDirs returns the names of working-copy subdirectories in a
// multi_repo workspace. Only directories that contain .git or .jj are
// included; plain directories and files are ignored. These are
// directory names, not repo names — see PresentRepos for the repos a
// workspace holds.
func PresentDirs(workspaceDir string) []string {
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

// NonRepoItem describes a workspace entry that is not a managed repo.
type NonRepoItem struct {
	Name string // entry name (e.g. "docs", "CLAUDE.md")
	Kind string // "file" or "dir"
}

// NonRepoItems returns workspace entries that are not in the managed
// repos list. Directories get Kind "dir", everything else gets "file".
func NonRepoItems(workspaceDir string, managedRepos []string) []NonRepoItem {
	managed := make(map[string]bool, len(managedRepos))
	for _, r := range managedRepos {
		managed[r] = true
	}

	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return nil
	}

	var items []NonRepoItem
	for _, e := range entries {
		if managed[e.Name()] {
			continue
		}
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		}
		items = append(items, NonRepoItem{Name: e.Name(), Kind: kind})
	}
	return items
}

// CopyNonRepoItems copies all workspace entries from srcDir to dstDir
// except those in the managedRepos list. Managed repos are forked
// separately via ForkRepo. Everything else (files, symlinks, non-repo
// directories, and one-off cloned repos) is copied verbatim.
func CopyNonRepoItems(srcDir, dstDir string, managedRepos []string) error {
	managed := make(map[string]bool, len(managedRepos))
	for _, r := range managedRepos {
		managed[r] = true
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("reading workspace dir: %w", err)
	}

	for _, e := range entries {
		name := e.Name()
		if managed[name] {
			continue
		}
		srcPath := filepath.Join(srcDir, name)
		dstPath := filepath.Join(dstDir, name)

		info, err := os.Lstat(srcPath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", name, err)
		}

		switch {
		case info.IsDir():
			if err := copyTreeExcluding(srcPath, dstPath, nil); err != nil {
				return fmt.Errorf("copying directory %s: %w", name, err)
			}
		case info.Mode()&os.ModeSymlink != 0:
			target, readErr := os.Readlink(srcPath)
			if readErr != nil {
				return fmt.Errorf("readlink %s: %w", name, readErr)
			}
			if err := os.Symlink(target, dstPath); err != nil {
				return fmt.Errorf("symlink %s: %w", name, err)
			}
		default:
			if err := copyFile(srcPath, dstPath); err != nil {
				return fmt.Errorf("copying file %s: %w", name, err)
			}
		}
	}
	return nil
}

// DestroyRepoResult holds the outcome of forgetting a single repo's workspace.
type DestroyRepoResult struct {
	Repo   string
	VCS    string
	Output string
	Err    error
}

// RepoProvenance says where a directory inside a task's workspace came
// from: which repo under ReposDir it was created from, and the VCS
// identity it was created with. Recorded entries come from the
// workspace_repos table; entries with Recorded false were derived from
// a filesystem scan and are only as good as the directory name.
type RepoProvenance struct {
	DirName  string // directory name inside the workspace
	RepoName string // directory name under ReposDir
	VCS      string // "jj" or "git"
	VCSName  string // jj workspace name; git branches are krang/<VCSName>
	Label    string // slot label; empty for a task's initial working copy
	Base     string // revset/commit-ish the working copy started from
	Recorded bool
}

// DeriveProvenance reconstructs provenance for a workspace built before
// krang recorded it, using the pre-slot derivation: every repo-looking
// subdirectory is a working copy of the identically named repo, created
// under a VCS identity named after the workspace directory.
func DeriveProvenance(rs *RepoSets, workspaceDir string) []RepoProvenance {
	vcsName := filepath.Base(workspaceDir)
	var derived []RepoProvenance
	for _, dirName := range PresentDirs(workspaceDir) {
		entry := RepoProvenance{
			DirName:  dirName,
			RepoName: dirName,
			VCSName:  vcsName,
		}
		if rs != nil {
			entry.VCS = rs.DetectVCS(dirName)
		}
		derived = append(derived, entry)
	}
	return derived
}

// ForgetRepo cleans up a single working copy. For jj it runs jj
// workspace forget against the recorded workspace name; for git it
// removes the worktree and branch. Fields left empty on target fall
// back to the pre-provenance derivation so one-off clones krang never
// recorded still get cleaned up.
func ForgetRepo(rs *RepoSets, workspaceDir string, target RepoProvenance) DestroyRepoResult {
	resolved := ResolveProvenance(rs, workspaceDir, target)
	repoSrc := filepath.Join(rs.ReposDir, resolved.RepoName)

	switch resolved.VCS {
	case "jj":
		output, err := forgetJJWorkspaceOutput(repoSrc, resolved.VCSName)
		return DestroyRepoResult{Repo: resolved.RepoName, VCS: resolved.VCS, Output: output, Err: err}
	default:
		worktreePath := filepath.Join(workspaceDir, target.DirName)
		if rs.WorkspaceStrategy == StrategySingleRepo {
			worktreePath = workspaceDir
		}
		output, err := removeGitWorktree(repoSrc, worktreePath, resolved.VCSName)
		return DestroyRepoResult{Repo: resolved.RepoName, VCS: resolved.VCS, Output: output, Err: err}
	}
}

// ResolveProvenance fills in whatever a provenance entry leaves empty
// with the pre-provenance derivation, so an unrecorded directory — a
// one-off clone somebody made by hand — still resolves to a repo and a
// VCS identity. Cleanup and anything that has to *describe* cleanup
// before running it go through here, so the branch a warning names is
// the branch the removal will actually try to delete.
func ResolveProvenance(rs *RepoSets, workspaceDir string, target RepoProvenance) RepoProvenance {
	resolved := target
	if resolved.RepoName == "" {
		resolved.RepoName = target.DirName
	}
	if resolved.VCS == "" && rs != nil {
		resolved.VCS = rs.DetectVCS(resolved.RepoName)
	}
	if resolved.VCSName == "" {
		resolved.VCSName = filepath.Base(workspaceDir)
	}
	return resolved
}

// GitBranchFor names the branch cleanup will try to delete for a working
// copy. Slots make this worth asking for rather than deriving from the
// task name: a task holding three checkouts of one repo has three
// branches, and only the initial one is named after the task.
func GitBranchFor(rs *RepoSets, workspaceDir string, target RepoProvenance) string {
	return gitBranchPrefix + ResolveProvenance(rs, workspaceDir, target).VCSName
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

// DestroyRepoList returns the working copies in a workspace that need
// cleanup, one entry per working copy — a task holding three checkouts
// of one repo gets three, because each owns a VCS identity of its own.
// Recorded rows are authoritative and come first, in row order: they
// carry the repo and the identity a directory name can no longer imply
// once slots exist. A recorded row whose directory is already gone stays
// in the list, since the identity it names still has to be forgotten.
//
// The filesystem scan behind them is a best-effort fallback covering
// directories with no row, such as one-off clones made by hand inside
// the workspace. It only applies where the workspace directory is a
// *container* of working copies: in single_repo the workspace directory
// IS the checkout, so its subdirectories are that repo's own contents
// and scanning them would invent slots out of vendored checkouts.
func DestroyRepoList(rs *RepoSets, workspaceDir string, recorded []RepoProvenance) []RepoProvenance {
	targets := make([]RepoProvenance, 0, len(recorded))
	recordedDirs := make(map[string]bool, len(recorded))
	for _, row := range recorded {
		row.Recorded = true
		recordedDirs[row.DirName] = true
		targets = append(targets, row)
	}

	if rs != nil && rs.WorkspaceStrategy == StrategySingleRepo {
		return targets
	}

	for _, derived := range DeriveProvenance(rs, workspaceDir) {
		if recordedDirs[derived.DirName] {
			continue
		}
		targets = append(targets, derived)
	}

	return targets
}

func isRepoDir(dir string) bool {
	for _, marker := range []string{".jj", ".git"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// Destroy removes a workspace directory, forgetting each working copy
// it holds first. Recorded rows drive the cleanup; directories with no
// row fall back to the filesystem scan. The RepoSets parameter is
// needed to find source repos; pass nil to skip VCS cleanup.
func Destroy(rs *RepoSets, workspaceDir string, recorded []RepoProvenance) error {
	if rs != nil {
		targets := DestroyRepoList(rs, workspaceDir, recorded)
		for _, target := range targets {
			_ = ForgetRepo(rs, workspaceDir, target)
		}

		// In single_repo mode the workspace dir itself is the checkout,
		// so there is no subdirectory the scan could have found. With no
		// recorded row there is nothing to say which repo it came from
		// either, and the only way left is to ask every repo in turn.
		if rs.WorkspaceStrategy == StrategySingleRepo && len(targets) == 0 {
			_ = ForgetSingleRepoWorkspace(rs, workspaceDir)
		}
	}

	return os.RemoveAll(workspaceDir)
}

func forgetJJWorkspaceOutput(repoSrc, workspaceName string) (string, error) {
	output, err := runVCSCommand(repoSrc, "jj", "workspace", "forget", workspaceName)
	if err != nil {
		return output, fmt.Errorf("jj workspace forget: %w: %s", err, output)
	}
	return output, nil
}

// runVCSCommand runs a VCS command in dir and returns its combined
// output. Cleanup goes through this seam so tests can assert on the
// commands krang builds without standing up real repositories.
var runVCSCommand = func(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
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
	Repo       string
	VCS        string
	Output     string // combined stdout+stderr from the clone command
	Provenance RepoProvenance
	Err        error
}

// CloneRepoAs creates one working copy of a repo under an explicit
// identity, which fixes its directory name, its jj workspace name, and
// its git branch. Every working copy krang makes for a task goes
// through here, so the names are derived one way and the identity is
// checked against what the source repo already holds before anything
// is written. On success the result carries the provenance the caller
// should record.
func CloneRepoAs(rs *RepoSets, identity SlotIdentity, dst string) CloneRepoResult {
	result := CloneRepoResult{
		Repo: identity.RepoName,
		VCS:  rs.DetectVCS(identity.RepoName),
	}

	if err := identity.Validate(rs); err != nil {
		result.Err = err
		return result
	}
	if err := checkVCSNameFree(rs, identity); err != nil {
		result.Err = err
		return result
	}

	// The source is always the canonical repo under ReposDir, never
	// another slot in the workspace: a slot branched off a sibling
	// working copy would inherit that sibling's in-progress state and
	// its VCS identity's lifetime.
	repoSrc := filepath.Join(rs.ReposDir, identity.RepoName)
	var base string
	switch result.VCS {
	case "jj":
		result.Output, base, result.Err = cloneJJWorkspace(repoSrc, dst, identity.VCSName(), identity.Base)
	default:
		result.Output, base, result.Err = addGitWorktree(repoSrc, dst, identity.VCSName(), identity.Base)
	}
	if result.Err != nil {
		return result
	}

	result.Provenance = RepoProvenance{
		DirName:  identity.DirName(),
		RepoName: identity.RepoName,
		VCS:      result.VCS,
		VCSName:  identity.VCSName(),
		Label:    identity.Label,
		Base:     base,
	}
	return result
}

// CloneRepo creates a task's initial working copy of a repo, which
// keeps the pre-slot names. For single_repo mode, dst should be the
// workspace dir itself. For multi_repo mode, dst should be
// workspaceDir/repo.
func CloneRepo(rs *RepoSets, taskName, dst, repo string) CloneRepoResult {
	return CloneRepoAs(rs, SlotIdentity{TaskName: taskName, RepoName: repo}, dst)
}

// RepoDst returns the destination path for a repo within a workspace.
func RepoDst(rs *RepoSets, workspaceDir, repo string) string {
	if rs.WorkspaceStrategy == StrategySingleRepo {
		return workspaceDir
	}
	return filepath.Join(workspaceDir, repo)
}

// cloneJJWorkspace adds a jj workspace at repoDst. base is the revset to
// start from; empty means detect the remote default bookmark, which is
// what krang has always done. The revset actually used comes back as the
// second return value so the caller can record it — "where did this slot
// start?" is unanswerable later, since the bookmark will have moved.
func cloneJJWorkspace(repoSrc, repoDst, workspaceName, base string) (string, string, error) {
	// Ensure the source repo's working copy is up to date — a stale
	// working copy causes "jj workspace add" to fail.
	updateCmd := exec.Command("jj", "workspace", "update-stale")
	updateCmd.Dir = repoSrc
	_ = updateCmd.Run() // safe no-op if not stale

	// Fetch latest from origin so the workspace isn't based on stale
	// state. This runs before detection so "today's main@origin" means
	// today's, not whatever was last fetched.
	_ = fetchJJRemote(repoSrc)

	if base == "" {
		base = detectJJDefaultBookmark(repoSrc)
	}

	// jj workspace add must be run from the source repo.
	args := []string{"workspace", "add", repoDst, "--name", workspaceName}
	if base != "" {
		args = append(args, "-r", base)
	}

	cmd := exec.Command("jj", args...)
	cmd.Dir = repoSrc
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), base, fmt.Errorf("jj workspace add: %w: %s", err, output)
	}
	return string(output), base, nil
}

// addGitWorktree creates a git worktree at repoDst branching from repoSrc.
// The branch is named krang/<vcsName> to make it identifiable for cleanup.
// It fetches from origin first and, when base is empty, bases the
// worktree on the remote default branch so that new workspaces start
// from up-to-date code. The commit-ish actually used comes back as the
// second return value for recording.
func addGitWorktree(repoSrc, repoDst, taskName, base string) (string, string, error) {
	// Fetch latest from origin so the worktree isn't based on a stale
	// local HEAD. Non-fatal — proceed with whatever we have on failure.
	_ = fetchGitRemote(repoSrc)

	// Determine the remote default branch (e.g. "origin/main") unless
	// the caller named a base. Either way, delegate to
	// addGitWorktreeAt so the worktree starts there.
	if base == "" {
		base = detectGitDefaultBranch(repoSrc)
	}
	if base != "" {
		output, err := addGitWorktreeAt(repoSrc, repoDst, taskName, base)
		return output, base, err
	}

	// Fallback: no remote and no base given — branch from HEAD. There
	// is no stable name to record for that.
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
		return string(output), "", fmt.Errorf("git worktree add: %w: %s", err, output)
	}

	result := string(output)

	// Copy files listed in .worktreeinclude (e.g., .env).
	if inclErr := processWorktreeInclude(repoSrc, repoDst); inclErr != nil {
		// Non-fatal: log but don't fail workspace creation.
		result += "\nworktreeinclude warning: " + inclErr.Error()
	}

	return result, "", nil
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
	slots := PresentSlots(rs, workspaceDir, nil)
	for _, slot := range slots {
		if slot.VCS != "jj" {
			return false
		}
	}
	return len(slots) > 0
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
	branchName := gitBranchPrefix + taskName

	// If the worktree directory is already gone, prune stale entries.
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		_, _ = runVCSCommand(repoSrc, "git", "worktree", "prune")
	} else {
		rmOut, err := runVCSCommand(repoSrc, "git", "worktree", "remove", "--force", worktreePath)
		allOutput.WriteString(rmOut)
		if err != nil {
			// If removal fails, try pruning then force remove.
			_, _ = runVCSCommand(repoSrc, "git", "worktree", "prune")
		}
	}

	// Try to delete the branch. Use -d (not -D) so git refuses to
	// delete branches with unpushed commits.
	delOut, err := runVCSCommand(repoSrc, "git", "branch", "-d", branchName)
	allOutput.WriteString(delOut)
	if err != nil {
		// Branch has unpushed commits or doesn't exist — not fatal.
		allOutput.WriteString(fmt.Sprintf("(branch %s kept: %s)", branchName, strings.TrimSpace(delOut)))
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
