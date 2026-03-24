package proctree

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MinAge is the minimum elapsed time a process must have to be
// considered a background process. Processes younger than this are
// assumed to be ephemeral tool invocations.
const MinAge = 30 * time.Second

type Process struct {
	PID     int
	Command string
	Elapsed time.Duration
}

type TaskProcesses struct {
	ClaudePID int
	Children  []Process
}

type rawProcess struct {
	PID     int
	PPID    int
	Elapsed time.Duration
	Command string
}

// CollectAll gathers child process trees for multiple tasks in a single
// ps call. shellPIDs maps task ID to the tmux pane's shell PID.
func CollectAll(shellPIDs map[string]int) map[string]*TaskProcesses {
	out, err := exec.Command("ps", "-eo", "pid,ppid,etime,command", "-ww").Output()
	if err != nil {
		return nil
	}
	snapshot := parsePSOutput(string(out))
	return collectAllFromSnapshot(snapshot, shellPIDs)
}

func collectAllFromSnapshot(snapshot []rawProcess, shellPIDs map[string]int) map[string]*TaskProcesses {
	tree := buildTree(snapshot)
	byPID := make(map[int]rawProcess, len(snapshot))
	for _, p := range snapshot {
		byPID[p.PID] = p
	}

	result := make(map[string]*TaskProcesses, len(shellPIDs))
	for taskID, shellPID := range shellPIDs {
		claudePID := findClaude(tree, byPID, shellPID)
		if claudePID == 0 {
			continue
		}

		descendants := collectDescendants(tree, claudePID)
		var rawDescendants []rawProcess
		for _, pid := range descendants {
			if p, ok := byPID[pid]; ok {
				rawDescendants = append(rawDescendants, p)
			}
		}

		filtered := filterNoise(rawDescendants)
		filtered = filterYoung(filtered, MinAge)
		filtered = filterAncestors(filtered)
		if len(filtered) == 0 {
			result[taskID] = &TaskProcesses{ClaudePID: claudePID}
			continue
		}

		procs := make([]Process, len(filtered))
		for i, rp := range filtered {
			procs[i] = Process{PID: rp.PID, Command: rp.Command, Elapsed: rp.Elapsed}
		}
		result[taskID] = &TaskProcesses{ClaudePID: claudePID, Children: procs}
	}
	return result
}

func parsePSOutput(output string) []rawProcess {
	lines := strings.Split(output, "\n")
	var procs []rawProcess
	for i, line := range lines {
		if i == 0 {
			continue // skip header
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Format: "  PID  PPID     ELAPSED COMMAND..."
		// First three fields are whitespace-separated, rest is command.
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		elapsed := parseEtime(fields[2])
		command := strings.Join(fields[3:], " ")
		procs = append(procs, rawProcess{PID: pid, PPID: ppid, Elapsed: elapsed, Command: command})
	}
	return procs
}

// parseEtime parses ps etime format: [[DD-]HH:]MM:SS
func parseEtime(s string) time.Duration {
	var days, hours, minutes, seconds int

	// Split off days if present.
	if idx := strings.Index(s, "-"); idx >= 0 {
		days, _ = strconv.Atoi(s[:idx])
		s = s[idx+1:]
	}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 3:
		hours, _ = strconv.Atoi(parts[0])
		minutes, _ = strconv.Atoi(parts[1])
		seconds, _ = strconv.Atoi(parts[2])
	case 2:
		minutes, _ = strconv.Atoi(parts[0])
		seconds, _ = strconv.Atoi(parts[1])
	case 1:
		seconds, _ = strconv.Atoi(parts[0])
	}

	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second
}

func buildTree(procs []rawProcess) map[int][]int {
	tree := make(map[int][]int)
	for _, p := range procs {
		tree[p.PPID] = append(tree[p.PPID], p.PID)
	}
	return tree
}

// findClaude walks the process tree from shellPID looking for a
// process whose basename is "claude". Handles sandbox wrappers
// (e.g. fish -> bash safehouse -> claude) by following single-child
// chains and checking all children at each level.
func findClaude(tree map[int][]int, byPID map[int]rawProcess, shellPID int) int {
	visited := make(map[int]bool)
	queue := tree[shellPID]
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if visited[pid] {
			continue
		}
		visited[pid] = true

		if p, ok := byPID[pid]; ok {
			fields := strings.Fields(p.Command)
			if len(fields) > 0 && filepath.Base(fields[0]) == "claude" {
				return pid
			}
		}
		queue = append(queue, tree[pid]...)
	}
	return 0
}

func collectDescendants(tree map[int][]int, rootPID int) []int {
	var result []int
	stack := tree[rootPID]
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		result = append(result, pid)
		stack = append(stack, tree[pid]...)
	}
	return result
}

// Noise basenames: shells used as tool wrappers, sandbox tools,
// transient inspection commands.
var noiseBasenames = map[string]bool{
	"caffeinate": true,
	"pgrep":      true,
	"ps":         true,
	"nah":        true,
}

func isNoise(command string) bool {
	if strings.Contains(command, "mcp") {
		return true
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return true
	}
	basename := filepath.Base(fields[0])
	return noiseBasenames[basename]
}

func filterNoise(procs []rawProcess) []rawProcess {
	var filtered []rawProcess
	for _, p := range procs {
		if !isNoise(p.Command) {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// filterAncestors removes processes that are parents of other
// processes in the list. This eliminates wrapper shells (e.g. a zsh
// that spawned gh run watch) when the actual command is also present.
func filterAncestors(procs []rawProcess) []rawProcess {
	parentOfAnother := make(map[int]bool)
	for _, p := range procs {
		parentOfAnother[p.PPID] = true
	}
	var filtered []rawProcess
	for _, p := range procs {
		if !parentOfAnother[p.PID] {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func filterYoung(procs []rawProcess, minAge time.Duration) []rawProcess {
	var filtered []rawProcess
	for _, p := range procs {
		if p.Elapsed >= minAge {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// FormatForPrompt returns a human-readable list of child processes
// suitable for inclusion in an AI prompt.
func FormatForPrompt(tp *TaskProcesses) string {
	if tp == nil || len(tp.Children) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Background processes running under this session:")
	for _, p := range tp.Children {
		fmt.Fprintf(&b, "\n- %s", p.Command)
	}
	return b.String()
}
