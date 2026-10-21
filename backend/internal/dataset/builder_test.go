package dataset

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/billyoftea/wxagent/go_backend/internal/state"
)

func TestBuildDoesNotAdvanceProgressOrDropSameNameSessions(t *testing.T) {
	dir := t.TempDir()
	store := state.New(filepath.Join(dir, "state.json"))
	for _, id := range []string{"first", "second"} {
		data := fmt.Sprintf(`{"session":{"wxid":%q,"displayName":"Same name"},"messages":[{"createTime":100,"content":%q}]}`, id, id)
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		store.UpdateSession(id, 100, 1)
	}
	result, err := NewBuilder(dir, store).Build("", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Dataset["Same name"]) != 2 || len(result.Stats) != 2 {
		t.Fatalf("same-name sessions lost data: %+v", result)
	}
	for _, stat := range result.Stats {
		if stat.SessionID == "" || store.LastSummaryTimestamp(stat.SessionID) != 0 || store.LastTimestamp(stat.SessionID) != 100 {
			t.Fatalf("building a dataset changed progress: %+v", stat)
		}
	}
}
