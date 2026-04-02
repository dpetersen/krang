package tui

import "testing"

func TestFormatCwd(t *testing.T) {
	tests := []struct {
		name             string
		taskCwd          string
		taskWorkspaceDir string
		krangCwd         string
		want             string
	}{
		{
			name:             "workspace root shows folder slash",
			taskCwd:          "/home/user/project/workspaces/abc123",
			taskWorkspaceDir: "/home/user/project/workspaces/abc123",
			krangCwd:         "/home/user/project",
			want:             "📂/",
		},
		{
			name:             "subdirectory within workspace",
			taskCwd:          "/home/user/project/workspaces/abc123/src/components",
			taskWorkspaceDir: "/home/user/project/workspaces/abc123",
			krangCwd:         "/home/user/project",
			want:             "📂/src/components",
		},
		{
			name:             "cwd outside workspace falls back to krang-relative",
			taskCwd:          "/home/user/project/other/dir",
			taskWorkspaceDir: "/home/user/project/workspaces/abc123",
			krangCwd:         "/home/user/project",
			want:             "other/dir",
		},
		{
			name:             "no workspace uses krang-relative path",
			taskCwd:          "/home/user/project/src/main",
			taskWorkspaceDir: "",
			krangCwd:         "/home/user/project",
			want:             "src/main",
		},
		{
			name:             "cwd equals krang cwd shows dot",
			taskCwd:          "/home/user/project",
			taskWorkspaceDir: "",
			krangCwd:         "/home/user/project",
			want:             ".",
		},
		{
			name:             "cwd outside krang dir gets tildeified",
			taskCwd:          "/tmp/elsewhere",
			taskWorkspaceDir: "",
			krangCwd:         "/home/user/project",
			want:             "/tmp/elsewhere",
		},
		{
			name:             "workspace set but cwd matches krang cwd exactly",
			taskCwd:          "/home/user/project",
			taskWorkspaceDir: "/home/user/project/workspaces/abc123",
			krangCwd:         "/home/user/project",
			want:             ".",
		},
		{
			name:             "empty krang cwd falls back to tildeify",
			taskCwd:          "/home/user/project/src",
			taskWorkspaceDir: "",
			krangCwd:         "",
			want:             "/home/user/project/src",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCwd(tt.taskCwd, tt.taskWorkspaceDir, tt.krangCwd)
			if got != tt.want {
				t.Errorf("formatCwd(%q, %q, %q) = %q, want %q",
					tt.taskCwd, tt.taskWorkspaceDir, tt.krangCwd, got, tt.want)
			}
		})
	}
}
