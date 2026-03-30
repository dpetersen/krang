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
	cmd := exec.Command("jj", "workspace", "forget", workspaceName)
	cmd.Dir = repoSrc
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("jj workspace forget: %w: %s", err, output)
	}
	return nil
}

func createJJWorkspace(repoSrc, repoDst, workspaceName string) error {
	// jj workspace add must be run from the source repo.
	cmd := exec.Command("jj", "workspace", "add", repoDst, "--name", workspaceName)
	cmd.Dir = repoSrc
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("jj workspace add: %w: %s", err, output)
	}
	return nil
}

func createGitClone(repoSrc, repoDst string) error {
	// Local clone uses hardlinks for objects — fast and space-efficient.
	cmd := exec.Command("git", "clone", repoSrc, repoDst)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone: %w: %s", err, output)
	}
	return nil
}
