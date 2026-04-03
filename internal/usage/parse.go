package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TokenSnapshot records usage from a single API response.
type TokenSnapshot struct {
	Timestamp   time.Time
	Model       string
	Input       int
	Output      int
	CacheCreate int
	CacheRead   int
}

// Cost returns the estimated cost of this single snapshot.
func (s TokenSnapshot) Cost() float64 {
	p := pricingForModel(s.Model)
	return tokenCost(s.Input, p.Input) +
		tokenCost(s.Output, p.Output) +
		tokenCost(s.CacheRead, p.CacheRead) +
		tokenCost(s.CacheCreate, p.CacheCreate)
}

// ModelUsage aggregates token counts for a single model.
type ModelUsage struct {
	Input       int
	Output      int
	CacheCreate int
	CacheRead   int
	Cost        float64
}

// UsageSummary holds parsed usage for an entire session.
type UsageSummary struct {
	Snapshots     []TokenSnapshot       // chronological
	TotalByModel  map[string]ModelUsage // keyed by model ID
	EstimatedCost float64
	Err           error // non-nil if parsing failed
}

// ParseTranscript reads a Claude Code transcript JSONL and extracts token
// usage from assistant messages. It also scans for subagent transcripts in
// a sibling "subagents/" directory.
//
// The transcript may contain multiple entries for the same API response
// (streaming updates), so entries are deduplicated by message ID, keeping
// the last occurrence which has final usage counts.
func ParseTranscript(path string) (*UsageSummary, error) {
	// Collect deduplicated snapshots keyed by message ID.
	byMessageID := make(map[string]TokenSnapshot)

	if err := parseFile(path, byMessageID); err != nil {
		return nil, err
	}

	// Include subagent transcripts if present.
	// Subagents live in <session-id>/subagents/ alongside <session-id>.jsonl.
	sessionDir := strings.TrimSuffix(path, filepath.Ext(path))
	subagentDir := filepath.Join(sessionDir, "subagents")
	entries, err := os.ReadDir(subagentDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
				continue
			}
			_ = parseFile(filepath.Join(subagentDir, entry.Name()), byMessageID)
		}
	}

	summary := &UsageSummary{
		TotalByModel: make(map[string]ModelUsage),
	}

	for _, snap := range byMessageID {
		summary.Snapshots = append(summary.Snapshots, snap)

		mu := summary.TotalByModel[snap.Model]
		mu.Input += snap.Input
		mu.Output += snap.Output
		mu.CacheCreate += snap.CacheCreate
		mu.CacheRead += snap.CacheRead
		summary.TotalByModel[snap.Model] = mu
	}

	sort.Slice(summary.Snapshots, func(i, j int) bool {
		return summary.Snapshots[i].Timestamp.Before(summary.Snapshots[j].Timestamp)
	})

	var total float64
	for model, mu := range summary.TotalByModel {
		mu.Cost = mu.computeCost(model)
		summary.TotalByModel[model] = mu
		total += mu.Cost
	}
	summary.EstimatedCost = total

	return summary, nil
}

// transcriptEntry is the minimal structure we need from each JSONL line.
type transcriptEntry struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func parseFile(path string, byMessageID map[string]TokenSnapshot) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 2*1024*1024)

	for scanner.Scan() {
		var entry transcriptEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != "assistant" {
			continue
		}
		u := entry.Message.Usage
		if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheCreationInputTokens == 0 && u.CacheReadInputTokens == 0 {
			continue
		}

		msgID := entry.Message.ID
		if msgID == "" {
			msgID = entry.Timestamp.String() // fallback for entries without ID
		}

		// Last entry for a given message ID wins (final streaming usage).
		byMessageID[msgID] = TokenSnapshot{
			Timestamp:   entry.Timestamp,
			Model:       entry.Message.Model,
			Input:       u.InputTokens,
			Output:      u.OutputTokens,
			CacheCreate: u.CacheCreationInputTokens,
			CacheRead:   u.CacheReadInputTokens,
		}
	}

	return scanner.Err()
}
