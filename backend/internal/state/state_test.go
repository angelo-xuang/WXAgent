package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedSummaryCommitPreservesMemoryAndExistingData(t *testing.T) {
	dir := t.TempDir()
	// A directory cannot be replaced with the staged state file.
	store := New(dir)
	store.SummarySessions["existing"] = SessionState{LastTimestamp: 7}
	sentinel := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitSummaries(map[string]SessionState{"existing": {LastTimestamp: 9}}); err == nil {
		t.Fatal("expected a persistence failure")
	}
	if store.LastSummaryTimestamp("existing") != 7 {
		t.Fatal("failed commit advanced live progress")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "original" {
		t.Fatalf("existing data changed: %q, %v", data, err)
	}
}
