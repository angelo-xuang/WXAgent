package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesCompleteFileAndCleansStaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	for _, content := range []string{`{"before":true}`, `{"after":true}`} {
		if err := Write(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("saved file = %q, %v", got, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Fatalf("staging files remained: %v, %v", entries, err)
	}
}
