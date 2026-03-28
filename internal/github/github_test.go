package github

import (
	"encoding/json"
	"testing"
)

func TestParseSearchResults(t *testing.T) {
	raw := `{
		"total_count": 3,
		"items": [
			{"name": "repo-alpha", "full_name": "org/repo-alpha"},
			{"name": "repo-beta", "full_name": "org/repo-beta"},
			{"name": "repo-gamma", "full_name": "org/repo-gamma"}
		]
	}`

	var result searchResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(result.Items))
	}

	want := []string{"repo-alpha", "repo-beta", "repo-gamma"}
	for i, item := range result.Items {
		if item.Name != want[i] {
			t.Errorf("items[%d].Name = %q, want %q", i, item.Name, want[i])
		}
	}
}

func TestParseEmptyResults(t *testing.T) {
	raw := `{"total_count": 0, "items": []}`

	var result searchResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Items) != 0 {
		t.Fatalf("items len = %d, want 0", len(result.Items))
	}
}
