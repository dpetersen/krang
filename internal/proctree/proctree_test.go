package proctree

import (
	"testing"
	"time"
)

func TestParsePSOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []rawProcess
	}{
		{
			name:   "empty output",
			output: "",
			want:   nil,
		},
		{
			name:   "header only",
			output: "  PID  PPID     ELAPSED COMMAND\n",
			want:   nil,
		},
		{
			name: "simple processes",
			output: `  PID  PPID     ELAPSED COMMAND
  100     1       05:00 /bin/bash
  200   100       03:00 node /path/to/claude
  300   200       01:30 python build.py
`,
			want: []rawProcess{
				{PID: 100, PPID: 1, Elapsed: 5 * time.Minute, Command: "/bin/bash"},
				{PID: 200, PPID: 100, Elapsed: 3 * time.Minute, Command: "node /path/to/claude"},
				{PID: 300, PPID: 200, Elapsed: 90 * time.Second, Command: "python build.py"},
			},
		},
		{
			name: "elapsed with days and hours",
			output: `  PID  PPID     ELAPSED COMMAND
12345   100  1-02:30:15 gh run watch 99999 --exit-status
`,
			want: []rawProcess{
				{PID: 12345, PPID: 100, Elapsed: 26*time.Hour + 30*time.Minute + 15*time.Second, Command: "gh run watch 99999 --exit-status"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePSOutput(tt.output)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d processes, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("process %d: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseEtime(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"05", 5 * time.Second},
		{"01:30", 1*time.Minute + 30*time.Second},
		{"02:30:15", 2*time.Hour + 30*time.Minute + 15*time.Second},
		{"1-02:30:15", 26*time.Hour + 30*time.Minute + 15*time.Second},
		{"3-00:00:00", 72 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseEtime(tt.input)
			if got != tt.want {
				t.Errorf("parseEtime(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildTree(t *testing.T) {
	procs := []rawProcess{
		{PID: 1, PPID: 0, Command: "/sbin/init"},
		{PID: 100, PPID: 1, Command: "/bin/bash"},
		{PID: 200, PPID: 100, Command: "node claude"},
		{PID: 300, PPID: 200, Command: "python build.py"},
		{PID: 400, PPID: 200, Command: "node mcp-server"},
		{PID: 500, PPID: 300, Command: "gcc main.c"},
	}

	tree := buildTree(procs)

	assertChildren := func(parent int, want []int) {
		t.Helper()
		got := tree[parent]
		if len(got) != len(want) {
			t.Errorf("parent %d: got %d children %v, want %d %v", parent, len(got), got, len(want), want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("parent %d child %d: got %d, want %d", parent, i, got[i], want[i])
			}
		}
	}

	assertChildren(0, []int{1})
	assertChildren(1, []int{100})
	assertChildren(100, []int{200})
	assertChildren(200, []int{300, 400})
	assertChildren(300, []int{500})
}

func TestCollectDescendants(t *testing.T) {
	tree := map[int][]int{
		100: {200},
		200: {300, 400},
		300: {500},
	}

	got := collectDescendants(tree, 200)
	want := map[int]bool{300: true, 400: true, 500: true}

	if len(got) != len(want) {
		t.Fatalf("got %d descendants, want %d", len(got), len(want))
	}
	for _, pid := range got {
		if !want[pid] {
			t.Errorf("unexpected descendant PID %d", pid)
		}
	}
}

func TestCollectDescendantsNoPID(t *testing.T) {
	tree := map[int][]int{
		100: {200},
	}

	got := collectDescendants(tree, 999)
	if len(got) != 0 {
		t.Errorf("expected no descendants for missing PID, got %v", got)
	}
}

func TestIsNoise(t *testing.T) {
	tests := []struct {
		command string
		noise   bool
	}{
		{"node /path/to/mcp-obsidian/dist/index.js", true},
		{"npm exec mcp-server-slack", true},
		{"caffeinate -i", true},
		{"pgrep -P 12345", true},
		{"ps -eo pid,ppid,command -ww", true},
		{"/bin/bash -c 'some tool command'", false},
		{"/bin/zsh -c source /path/to/snapshot.sh && eval 'grep foo'", false},
		{"safehouse --append-profile foo claude", false},
		{"nah classify -- gh run watch", true},
		{"docker run -i --rm hashicorp/terraform-mcp-server", true},
		{"npm exec @mauricio.wolff/mcp-obsidian@latest", true},
		{"python build.py", false},
		{"gh run watch 12345 --exit-status", false},
		{"node /path/to/claude", false},
		{"gcc main.c", false},
		{"go test ./...", false},
		{"kubectl wait --for=condition=ready pod/foo", false},
		{"npm test", false},
		{"docker build -t myapp .", false},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := isNoise(tt.command)
			if got != tt.noise {
				t.Errorf("isNoise(%q) = %v, want %v", tt.command, got, tt.noise)
			}
		})
	}
}

func TestFilterNoise(t *testing.T) {
	procs := []rawProcess{
		{PID: 300, PPID: 200, Elapsed: 2 * time.Minute, Command: "node /path/to/mcp-server"},
		{PID: 400, PPID: 200, Elapsed: 2 * time.Minute, Command: "python build.py"},
		{PID: 500, PPID: 400, Elapsed: 2 * time.Minute, Command: "gcc main.c"},
		{PID: 600, PPID: 200, Elapsed: 2 * time.Minute, Command: "caffeinate -i"},
	}

	got := filterNoise(procs)
	if len(got) != 2 {
		t.Fatalf("got %d processes, want 2: %+v", len(got), got)
	}
	if got[0].PID != 400 || got[1].PID != 500 {
		t.Errorf("got PIDs %d, %d; want 400, 500", got[0].PID, got[1].PID)
	}
}

func TestFilterYoung(t *testing.T) {
	procs := []rawProcess{
		{PID: 100, Elapsed: 5 * time.Second, Command: "gh run watch"},
		{PID: 200, Elapsed: 45 * time.Second, Command: "npm test"},
		{PID: 300, Elapsed: 2 * time.Minute, Command: "python build.py"},
	}

	got := filterYoung(procs, 30*time.Second)
	if len(got) != 2 {
		t.Fatalf("got %d processes, want 2: %+v", len(got), got)
	}
	if got[0].PID != 200 || got[1].PID != 300 {
		t.Errorf("got PIDs %d, %d; want 200, 300", got[0].PID, got[1].PID)
	}
}

func TestFilterAncestors(t *testing.T) {
	procs := []rawProcess{
		{PID: 100, PPID: 50, Elapsed: 5 * time.Minute, Command: "/bin/zsh -c 'gh run watch'"},
		{PID: 200, PPID: 100, Elapsed: 5 * time.Minute, Command: "gh run watch 12345"},
		{PID: 300, PPID: 50, Elapsed: 5 * time.Minute, Command: "python build.py"},
	}

	got := filterAncestors(procs)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(got), got)
	}
	// zsh (100) is parent of gh (200), so filtered out.
	// gh (200) and python (300) remain.
	if got[0].PID != 200 || got[1].PID != 300 {
		t.Errorf("got PIDs %d, %d; want 200, 300", got[0].PID, got[1].PID)
	}
}

func TestCollectAllFromSnapshot(t *testing.T) {
	snapshot := []rawProcess{
		{PID: 1, PPID: 0, Elapsed: time.Hour, Command: "/sbin/init"},
		// Task A: shell -> claude -> children
		{PID: 100, PPID: 1, Elapsed: 10 * time.Minute, Command: "/bin/zsh"},
		{PID: 200, PPID: 100, Elapsed: 10 * time.Minute, Command: "claude"},
		{PID: 300, PPID: 200, Elapsed: 10 * time.Minute, Command: "node /path/to/mcp-obsidian"},
		{PID: 400, PPID: 200, Elapsed: 5 * time.Minute, Command: "python build.py"},
		{PID: 500, PPID: 400, Elapsed: 5 * time.Minute, Command: "gcc main.c"},
		// Task B: shell -> claude -> children
		{PID: 600, PPID: 1, Elapsed: 10 * time.Minute, Command: "/bin/bash"},
		{PID: 700, PPID: 600, Elapsed: 10 * time.Minute, Command: "claude --resume foo"},
		{PID: 800, PPID: 700, Elapsed: 3 * time.Minute, Command: "gh run watch 12345"},
	}

	shellPIDs := map[string]int{
		"task-a": 100,
		"task-b": 600,
	}

	got := collectAllFromSnapshot(snapshot, shellPIDs)

	// Task A
	a := got["task-a"]
	if a == nil {
		t.Fatal("task-a: got nil")
	}
	if a.ClaudePID != 200 {
		t.Errorf("task-a ClaudePID: got %d, want 200", a.ClaudePID)
	}
	// python (400) is parent of gcc (500), so only gcc remains.
	// mcp-obsidian filtered as noise.
	if len(a.Children) != 1 {
		t.Fatalf("task-a: got %d children, want 1: %+v", len(a.Children), a.Children)
	}
	if a.Children[0].PID != 500 {
		t.Errorf("task-a child: got PID %d, want 500", a.Children[0].PID)
	}

	// Task B
	b := got["task-b"]
	if b == nil {
		t.Fatal("task-b: got nil")
	}
	if b.ClaudePID != 700 {
		t.Errorf("task-b ClaudePID: got %d, want 700", b.ClaudePID)
	}
	if len(b.Children) != 1 {
		t.Fatalf("task-b: got %d children, want 1: %+v", len(b.Children), b.Children)
	}
	if b.Children[0].Command != "gh run watch 12345" {
		t.Errorf("task-b child: got %q, want %q", b.Children[0].Command, "gh run watch 12345")
	}
}

func TestCollectAllSandboxWrapper(t *testing.T) {
	// Real-world chain: fish -> bash safehouse -> claude -> children
	snapshot := []rawProcess{
		{PID: 100, PPID: 1, Elapsed: 10 * time.Minute, Command: "fish -c export KRANG_STATEFILE=...; safehouse ... claude --resume foo"},
		{PID: 200, PPID: 100, Elapsed: 10 * time.Minute, Command: "bash /opt/homebrew/bin/safehouse --append-profile ... claude --resume foo"},
		{PID: 300, PPID: 200, Elapsed: 10 * time.Minute, Command: "claude --resume foo"},
		{PID: 400, PPID: 300, Elapsed: 10 * time.Minute, Command: "npm exec @mauricio.wolff/mcp-obsidian@latest /vault"},
		{PID: 410, PPID: 400, Elapsed: 10 * time.Minute, Command: "node /npx/mcp-obsidian /vault"},
		{PID: 420, PPID: 300, Elapsed: 10 * time.Minute, Command: "caffeinate -i -t 300"},
		{PID: 500, PPID: 300, Elapsed: 5 * time.Minute, Command: "gh run watch 12345 --exit-status"},
	}

	got := collectAllFromSnapshot(snapshot, map[string]int{"task": 100})
	tp := got["task"]
	if tp == nil {
		t.Fatal("got nil")
	}
	if tp.ClaudePID != 300 {
		t.Errorf("ClaudePID: got %d, want 300", tp.ClaudePID)
	}
	// Only gh run watch should survive (mcp + caffeinate filtered)
	if len(tp.Children) != 1 {
		t.Fatalf("got %d children, want 1: %+v", len(tp.Children), tp.Children)
	}
	if tp.Children[0].Command != "gh run watch 12345 --exit-status" {
		t.Errorf("got %q, want gh run watch", tp.Children[0].Command)
	}
}

func TestCollectAllFiltersYoungProcesses(t *testing.T) {
	snapshot := []rawProcess{
		{PID: 100, PPID: 1, Elapsed: 10 * time.Minute, Command: "/bin/bash"},
		{PID: 200, PPID: 100, Elapsed: 10 * time.Minute, Command: "claude"},
		{PID: 300, PPID: 200, Elapsed: 5 * time.Second, Command: "grep -r foo"},         // too young
		{PID: 400, PPID: 200, Elapsed: 2 * time.Minute, Command: "gh run watch 12345"},   // old enough
	}

	got := collectAllFromSnapshot(snapshot, map[string]int{"task": 100})
	tp := got["task"]
	if tp == nil {
		t.Fatal("got nil")
	}
	if len(tp.Children) != 1 {
		t.Fatalf("got %d children, want 1 (young process should be filtered): %+v", len(tp.Children), tp.Children)
	}
	if tp.Children[0].PID != 400 {
		t.Errorf("got PID %d, want 400", tp.Children[0].PID)
	}
}

func TestCollectAllNoClaudeChild(t *testing.T) {
	snapshot := []rawProcess{
		{PID: 100, PPID: 1, Elapsed: time.Minute, Command: "/bin/bash"},
	}

	got := collectAllFromSnapshot(snapshot, map[string]int{"task-a": 100})
	a := got["task-a"]
	if a != nil {
		t.Errorf("expected nil for task with no Claude child, got %+v", a)
	}
}

func TestCollectAllDeepTree(t *testing.T) {
	snapshot := []rawProcess{
		{PID: 100, PPID: 1, Elapsed: 10 * time.Minute, Command: "/bin/bash"},
		{PID: 200, PPID: 100, Elapsed: 10 * time.Minute, Command: "claude"},
		{PID: 300, PPID: 200, Elapsed: 5 * time.Minute, Command: "python test_runner.py"},
		{PID: 400, PPID: 300, Elapsed: 5 * time.Minute, Command: "python -m pytest"},
		{PID: 500, PPID: 400, Elapsed: 4 * time.Minute, Command: "node jest"},
		{PID: 600, PPID: 500, Elapsed: 4 * time.Minute, Command: "node test-runner.js"},
	}

	got := collectAllFromSnapshot(snapshot, map[string]int{"task": 100})
	tp := got["task"]
	if tp == nil {
		t.Fatal("got nil")
	}
	// Chain collapses to just the leaf: node test-runner.js (600)
	if len(tp.Children) != 1 {
		t.Fatalf("got %d children, want 1 (leaf only): %+v", len(tp.Children), tp.Children)
	}
	if tp.Children[0].PID != 600 {
		t.Errorf("got PID %d, want 600", tp.Children[0].PID)
	}
}

func TestCollectAllToolExecutionFiltered(t *testing.T) {
	// Simulates what we see in real Claude process trees:
	// tool wrapper shells, sandbox, nah, and MCP servers.
	snapshot := []rawProcess{
		{PID: 100, PPID: 1, Elapsed: 10 * time.Minute, Command: "/bin/fish"},
		{PID: 200, PPID: 100, Elapsed: 10 * time.Minute, Command: "claude --resume test"},
		{PID: 300, PPID: 200, Elapsed: 10 * time.Minute, Command: "npm exec @mauricio.wolff/mcp-obsidian@latest /vault"},
		{PID: 301, PPID: 300, Elapsed: 10 * time.Minute, Command: "node /npx/mcp-obsidian /vault"},
		{PID: 310, PPID: 200, Elapsed: 10 * time.Minute, Command: "docker run -i --rm hashicorp/terraform-mcp-server"},
		{PID: 320, PPID: 200, Elapsed: 10 * time.Minute, Command: "caffeinate -i -t 300"},
		{PID: 400, PPID: 200, Elapsed: 5 * time.Second, Command: "/bin/zsh -c source snapshot.sh && eval 'grep foo'"},
		{PID: 410, PPID: 400, Elapsed: 3 * time.Second, Command: "grep -r foo src/"},
		{PID: 500, PPID: 200, Elapsed: 5 * time.Minute, Command: "gh run watch 12345 --exit-status"},
	}

	got := collectAllFromSnapshot(snapshot, map[string]int{"task": 100})
	tp := got["task"]
	if tp == nil {
		t.Fatal("got nil")
	}
	// Only gh run watch should survive: MCP (300,301,310), caffeinate (320)
	// filtered as noise; zsh shell (400) and grep (410) filtered as too young.
	if len(tp.Children) != 1 {
		t.Fatalf("got %d children, want 1: %+v", len(tp.Children), tp.Children)
	}
	if tp.Children[0].Command != "gh run watch 12345 --exit-status" {
		t.Errorf("got %q, want gh run watch", tp.Children[0].Command)
	}
}
