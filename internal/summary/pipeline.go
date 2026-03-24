package summary

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/proctree"
	"github.com/dpetersen/krang/internal/tmux"
)

const (
	captureLines    = 50
	minInterval     = 30 * time.Second
	maxConcurrentAI = 2
)

type Pipeline struct {
	taskStore   *db.TaskStore
	lastSummary map[string]time.Time
	mu          sync.Mutex
	aiSemaphore chan struct{}
}

func NewPipeline(taskStore *db.TaskStore) *Pipeline {
	return &Pipeline{
		taskStore:   taskStore,
		lastSummary: make(map[string]time.Time),
		aiSemaphore: make(chan struct{}, maxConcurrentAI),
	}
}

// summarizeTask captures the pane, checks if content changed, and
// calls Haiku if needed. Returns a status string for debug logging.
func (p *Pipeline) summarizeTask(task db.Task, processContext string) string {
	if task.TmuxWindow == "" {
		return fmt.Sprintf("%s: no window", task.Name)
	}

	raw, err := tmux.CapturePane(task.TmuxWindow, captureLines)
	if err != nil {
		return fmt.Sprintf("%s: capture error: %v", task.Name, err)
	}

	stripped := StripANSI(raw)
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return fmt.Sprintf("%s: empty pane", task.Name)
	}

	contentHash := hash(stripped)
	if contentHash == task.SummaryHash {
		return fmt.Sprintf("%s: unchanged", task.Name)
	}

	p.mu.Lock()
	lastTime := p.lastSummary[task.ID]
	p.mu.Unlock()

	if time.Since(lastTime) < minInterval {
		return fmt.Sprintf("%s: rate limited", task.Name)
	}

	p.aiSemaphore <- struct{}{}
	defer func() { <-p.aiSemaphore }()

	result, err := Summarize(task.Name, stripped, processContext)
	if err != nil {
		return fmt.Sprintf("%s: AI error: %v", task.Name, err)
	}

	_ = p.taskStore.UpdateSummary(task.ID, result.OneLiner, contentHash)

	p.mu.Lock()
	p.lastSummary[task.ID] = time.Now()
	p.mu.Unlock()

	return fmt.Sprintf("%s: %q", task.Name, result.OneLiner)
}

// SummarizeAll runs summaries for all eligible tasks. Returns a
// status string per task for debug logging.
func (p *Pipeline) SummarizeAll(tasks []db.Task, processes map[string]*proctree.TaskProcesses) []string {
	type result struct {
		index int
		msg   string
	}

	var eligible []struct {
		index int
		task  db.Task
	}
	for i, t := range tasks {
		if t.TmuxWindow == "" {
			continue
		}
		if t.State != db.StateActive && t.State != db.StateParked {
			continue
		}
		eligible = append(eligible, struct {
			index int
			task  db.Task
		}{i, t})
	}

	results := make([]result, len(eligible))
	var wg sync.WaitGroup
	for i, e := range eligible {
		wg.Add(1)
		go func(idx int, task db.Task) {
			defer wg.Done()
			procCtx := proctree.FormatForPrompt(processes[task.ID])
			results[idx] = result{index: idx, msg: p.summarizeTask(task, procCtx)}
		}(i, e.task)
	}
	wg.Wait()

	var msgs []string
	for _, r := range results {
		msgs = append(msgs, r.msg)
	}
	return msgs
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}
