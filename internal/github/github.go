package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
)

type searchResult struct {
	Items []searchItem `json:"items"`
}

type searchItem struct {
	Name string `json:"name"`
}

func IsAvailable() bool {
	cmd := exec.Command("gh", "auth", "status")
	return cmd.Run() == nil
}

func SearchRepos(org, query string) ([]string, error) {
	q := fmt.Sprintf("org:%s %s in:name", org, query)
	cmd := exec.Command(
		"gh", "api", "/search/repositories",
		"-X", "GET",
		"-f", "q="+q,
		"-f", "per_page=30",
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh api: %s", exitErr.Stderr)
		}
		return nil, fmt.Errorf("gh api: %w", err)
	}

	var result searchResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing search results: %w", err)
	}

	repos := make([]string, len(result.Items))
	for i, item := range result.Items {
		repos[i] = item.Name
	}
	return repos, nil
}

func CloneRepo(org, repo, destDir, vcs string) error {
	url := fmt.Sprintf("https://github.com/%s/%s", org, repo)
	dst := filepath.Join(destDir, repo)

	var cmd *exec.Cmd
	switch vcs {
	case "jj":
		cmd = exec.Command("jj", "git", "clone", url, dst)
	default:
		cmd = exec.Command("git", "clone", url, dst)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s clone: %w: %s", vcs, err, output)
	}
	return nil
}
