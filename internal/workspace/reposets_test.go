package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "")

	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rs.ReposDir != filepath.Join(dir, "repos") {
		t.Errorf("ReposDir = %q, want %q", rs.ReposDir, filepath.Join(dir, "repos"))
	}
	if rs.WorkspacesDir != filepath.Join(dir, "workspaces") {
		t.Errorf("WorkspacesDir = %q, want %q", rs.WorkspacesDir, filepath.Join(dir, "workspaces"))
	}
}

func TestLoadCustomDirs(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
repos_dir: my-repos
workspaces_dir: my-workspaces
`)

	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rs.ReposDir != filepath.Join(dir, "my-repos") {
		t.Errorf("ReposDir = %q, want %q", rs.ReposDir, filepath.Join(dir, "my-repos"))
	}
	if rs.WorkspacesDir != filepath.Join(dir, "my-workspaces") {
		t.Errorf("WorkspacesDir = %q, want %q", rs.WorkspacesDir, filepath.Join(dir, "my-workspaces"))
	}
}

func TestLoadStrategy(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "workspace_strategy: single_repo")

	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rs.WorkspaceStrategy != StrategySingleRepo {
		t.Errorf("WorkspaceStrategy = %q, want %q", rs.WorkspaceStrategy, StrategySingleRepo)
	}
}

func TestLoadNoStrategy(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "repos_dir: my-repos")

	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rs.WorkspaceStrategy != "" {
		t.Errorf("WorkspaceStrategy = %q, want empty", rs.WorkspaceStrategy)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing krang.yaml")
	}
}

func TestListRepos(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "")

	reposDir := filepath.Join(dir, "repos")
	mkdirs(t, reposDir, "alpha", "beta", "gamma")
	// Create a file to ensure it's excluded.
	os.WriteFile(filepath.Join(reposDir, "README.md"), []byte("hi"), 0o644)

	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	repos, err := rs.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}

	want := []string{"alpha", "beta", "gamma"}
	if len(repos) != len(want) {
		t.Fatalf("ListRepos = %v, want %v", repos, want)
	}
	for i, r := range repos {
		if r != want[i] {
			t.Errorf("repos[%d] = %q, want %q", i, r, want[i])
		}
	}
}

func TestDetectVCS(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
repos:
  forced-git:
    vcs: git
`)
	reposDir := filepath.Join(dir, "repos")
	mkdirs(t, reposDir, "jj-repo", "git-repo", "forced-git")
	// Make jj-repo look like a jj repo.
	mkdirs(t, filepath.Join(reposDir, "jj-repo"), ".jj")

	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		repo string
		want string
	}{
		{"jj-repo", "jj"},
		{"git-repo", "git"},
		{"forced-git", "git"},
	}

	for _, tc := range tests {
		got := rs.DetectVCS(tc.repo)
		if got != tc.want {
			t.Errorf("DetectVCS(%q) = %q, want %q", tc.repo, got, tc.want)
		}
	}
}

func TestResolveRepos(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `
sets:
  backend:
    - gonfalon
    - gonfalon-priv
  terraform:
    - tf-config
    - tf-modules
`)

	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		name       string
		sets       []string
		individual []string
		want       []string
	}{
		{
			name: "single set",
			sets: []string{"backend"},
			want: []string{"gonfalon", "gonfalon-priv"},
		},
		{
			name:       "set plus individual",
			sets:       []string{"backend"},
			individual: []string{"catfood"},
			want:       []string{"catfood", "gonfalon", "gonfalon-priv"},
		},
		{
			name:       "deduplication",
			sets:       []string{"backend"},
			individual: []string{"gonfalon", "catfood"},
			want:       []string{"catfood", "gonfalon", "gonfalon-priv"},
		},
		{
			name: "multiple sets",
			sets: []string{"backend", "terraform"},
			want: []string{"gonfalon", "gonfalon-priv", "tf-config", "tf-modules"},
		},
		{
			name:       "individual only",
			individual: []string{"standalone"},
			want:       []string{"standalone"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rs.ResolveRepos(tc.sets, tc.individual)
			if len(got) != len(tc.want) {
				t.Fatalf("ResolveRepos = %v, want %v", got, tc.want)
			}
			for i, r := range got {
				if r != tc.want[i] {
					t.Errorf("result[%d] = %q, want %q", i, r, tc.want[i])
				}
			}
		})
	}
}

func writeYAML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "krang.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing krang.yaml: %v", err)
	}
}

func mkdirs(t *testing.T, base string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
}
