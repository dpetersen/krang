package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type WorkspaceStrategy string

const (
	StrategySingleRepo WorkspaceStrategy = "single_repo"
	StrategyMultiRepo  WorkspaceStrategy = "multi_repo"
)

type Config struct {
	WorkspaceStrategy WorkspaceStrategy `yaml:"workspace_strategy"`
	ReposDir          string            `yaml:"repos_dir"`
	WorkspacesDir     string            `yaml:"workspaces_dir"`
	DefaultVCS        string            `yaml:"default_vcs"`
	GitHubOrgs        []string          `yaml:"github_orgs"`
	Sets              map[string][]string `yaml:"sets"`
}

type RepoSets struct {
	MetarepoDir       string
	WorkspaceStrategy WorkspaceStrategy
	ReposDir          string // absolute path to repos directory
	WorkspacesDir     string // absolute path to workspaces directory
	DefaultVCS        string // "git" (default) or "jj" for remote clones
	GitHubOrgs    []string
	Sets          map[string][]string
}

func Load(metarepoDir string) (*RepoSets, error) {
	data, err := os.ReadFile(filepath.Join(metarepoDir, "krang.yaml"))
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing krang.yaml: %w", err)
	}

	reposRel := cfg.ReposDir
	if reposRel == "" {
		reposRel = "repos"
	}

	workspacesRel := cfg.WorkspacesDir
	if workspacesRel == "" {
		workspacesRel = "workspaces"
	}

	if cfg.Sets == nil {
		cfg.Sets = make(map[string][]string)
	}

	reposDir := filepath.Join(metarepoDir, reposRel)
	workspacesDir := filepath.Join(metarepoDir, workspacesRel)

	// Ensure directories exist so ListRepos and workspace creation work
	// even on a fresh metarepo with no repos cloned yet.
	_ = os.MkdirAll(reposDir, 0o755)
	_ = os.MkdirAll(workspacesDir, 0o755)

	return &RepoSets{
		MetarepoDir:       metarepoDir,
		WorkspaceStrategy: cfg.WorkspaceStrategy,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		DefaultVCS:        cfg.DefaultVCS,
		GitHubOrgs:        cfg.GitHubOrgs,
		Sets:              cfg.Sets,
	}, nil
}

// ListRepos returns sorted names of directories in the repos dir.
func (rs *RepoSets) ListRepos() ([]string, error) {
	entries, err := os.ReadDir(rs.ReposDir)
	if err != nil {
		return nil, fmt.Errorf("reading repos dir %s: %w", rs.ReposDir, err)
	}

	var repos []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "." && entry.Name() != ".." {
			repos = append(repos, entry.Name())
		}
	}
	sort.Strings(repos)
	return repos, nil
}

// DetectVCS returns "jj" or "git" for a repo. Probes the repo
// directory for .jj or .git, then falls back to DefaultVCS (or "git"
// if unset).
func (rs *RepoSets) DetectVCS(repoName string) string {
	repoDir := filepath.Join(rs.ReposDir, repoName)
	if info, err := os.Stat(filepath.Join(repoDir, ".jj")); err == nil && info.IsDir() {
		return "jj"
	}
	if info, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
		return "git"
	}
	if rs.DefaultVCS != "" {
		return rs.DefaultVCS
	}
	return "git"
}

// ResolveRepos expands set names and merges with individual repo
// names, returning a deduplicated sorted list.
func (rs *RepoSets) ResolveRepos(setNames, individualRepos []string) []string {
	seen := make(map[string]bool)
	var result []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}

	for _, setName := range setNames {
		if members, ok := rs.Sets[setName]; ok {
			for _, m := range members {
				add(m)
			}
		}
	}
	for _, r := range individualRepos {
		add(r)
	}

	sort.Strings(result)
	return result
}
