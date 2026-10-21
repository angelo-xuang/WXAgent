package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSavePreservesLegacyAndUnknownData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw := `{"sessions":{"a":{"last_timestamp":7}},"summary_sessions":{"legacy":{"last_timestamp":99,"extra":true}},"meta":{"owner":"test"},"future":{"keep":true}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	store.UpdateSession("a", 8, 2)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	var before, after map[string]json.RawMessage
	json.Unmarshal([]byte(raw), &before)
	json.Unmarshal(saved, &after)
	for _, key := range []string{"summary_sessions", "future"} {
		var a, b any
		json.Unmarshal(before[key], &a)
		json.Unmarshal(after[key], &b)
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		if string(left) != string(right) {
			t.Fatalf("legacy field %s changed", key)
		}
	}
	reloaded := New(path)
	if err := reloaded.Load(); err != nil || reloaded.LastTimestamp("a") != 8 {
		t.Fatalf("progress not saved: %v", err)
	}
}
func TestCorruptStateIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw := []byte(`{"sessions":`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := New(path).Load(); err == nil {
		t.Fatal("corrupt state accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("corrupt source overwritten")
	}
}
func TestFailedSavePreservesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "keep")
	os.WriteFile(sentinel, []byte("original"), 0600)
	store := New(dir)
	store.UpdateSession("x", 10, 1)
	if err := store.Save(); err == nil {
		t.Fatal("expected error")
	}
	data, _ := os.ReadFile(sentinel)
	if string(data) != "original" {
		t.Fatal("destination changed")
	}
}
