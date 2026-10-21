package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func readJSON(t *testing.T, args ...string) map[string]any {
	t.Helper()
	code, stdout, stderr := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("Run(%q) = %d, stderr=%s", args, code, stderr)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout is not one JSON value: %q: %v", stdout, err)
	}
	return result
}

func TestHelpAndVersionDoNotLoadConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{}, {"help"}, {"--help"}, {"-h"},
		{"--config", "missing.json", "help"},
		{"help", "messages"},
		{"messages", "--config", "missing.json", "--help"},
		{"serve", "-h"}, {"server", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := runCLI(t, args...)
			if code != 0 || stdout == "" || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			for _, obsolete := range []string{"analyze-chat", "summarize", "export-auto", "LLM"} {
				if strings.Contains(stdout, obsolete) {
					t.Fatalf("help exposes obsolete capability %q", obsolete)
				}
			}
		})
	}
	for _, args := range [][]string{{"version"}, {"--version"}, {"--config", "missing.json", "version"}} {
		if result := readJSON(t, args...); result["name"] != "wxagent" || result["version"] != Version {
			t.Fatalf("unexpected version: %#v", result)
		}
	}
}

func TestInvalidArgumentsReturnNonzeroBeforeReadingConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	cases := [][]string{
		{"analyze-chat"}, {"summarize"}, {"start"}, {"export-auto"}, {"unknown"},
		{"--bad-flag"}, {"messages", "--bad-flag"}, {"sessions", "unexpected"},
		{"messages", "--start-date", "2026-02-30"},
		{"messages", "--end-date", "yesterday"},
		{"messages", "--start-date", "2026-10-22", "--end-date", "2026-10-21"},
		{"messages", "--limit", "-1"}, {"messages", "--offset", "-1"},
		{"messages", "--limit", "abc"}, {"messages", "--session", ""},
		{"messages", "--sessions", "one,,two"},
		{"stats", "--limit", "10"},
		{"export", "--start-date", "not-a-date"},
		{"refresh-key", "--poll", "0"}, {"refresh-key", "--wait", "-1"},
		{"serve", "--port", "0"}, {"server", "--port", "65536"}, {"serve", "--host", ""},
		{"status", "--config", ""}, {"status", "--database", ""},
		{"import", "--export-dir", ""}, {"--config"},
		{"help", "unknown"}, {"--version", "status"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := runCLI(t, args...)
			if code != 2 || stdout != "" || stderr == "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestInitCreatesMinimalConfigAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "nested", "config.json")
	database := filepath.Join(dir, "records.db")
	exports := filepath.Join(dir, "exports")
	result := readJSON(t, "init", "--config", path, "--database", database, "--export-dir", exports)
	if result["status"] != "created" || result["config_path"] != path {
		t.Fatalf("unexpected init output: %#v", result)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(before, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 || stored["database_path"] != database || stored["export_dir"] != exports || stored["state_file"] != "./.wxagent/state.json" {
		t.Fatalf("unexpected minimal config: %#v", stored)
	}
	code, stdout, stderr := runCLI(t, "init", "--config", path)
	if code != 1 || stdout != "" || stderr == "" {
		t.Fatalf("second init: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("init changed existing configuration: %v", err)
	}
	for _, forbidden := range []string{database, exports, filepath.Join(dir, "nested", ".wxagent")} {
		if _, err := os.Stat(forbidden); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("init unexpectedly created %s: %v", forbidden, err)
		}
	}
}

func TestExplicitMissingConfigFails(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	code, stdout, stderr := runCLI(t, "status", "--config", "missing.json", "--database", filepath.Join(dir, "records.db"))
	if code != 1 || stdout != "" || stderr == "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("failed command modified current directory: %v %v", entries, err)
	}
}

func TestCorruptDatabaseReturnsAnError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "records.db")
	original := []byte("not a sqlite database")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"status", "sessions", "messages", "stats"} {
		code, stdout, stderr := runCLI(t, command, "--database", path)
		if code != 1 || stdout != "" || stderr == "" {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatalf("read commands changed a corrupt database: %v", err)
	}
}

func TestReadCommandsNeverImplicitlyImport(t *testing.T) {
	dir, exports, database := fixture(t)
	t.Chdir(dir)
	for _, command := range []string{"status", "sessions", "messages", "stats"} {
		code, stdout, stderr := runCLI(t, command, "--export-dir", exports, "--database", database)
		if code != 0 || !json.Valid([]byte(stdout)) {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
		if command == "sessions" && strings.TrimSpace(stdout) != "[]" {
			t.Fatalf("sessions silently imported records: %s", stdout)
		}
		if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s created a database before import: %v", command, err)
		}
	}
}

func TestImportQueryAndStatsProducePureJSON(t *testing.T) {
	dir, exports, database := fixture(t)
	t.Chdir(dir)
	result := readJSON(t, "--database", database, "--export-dir", exports, "import")
	if result["messages"] != float64(5) || result["sessions"] != float64(2) {
		t.Fatalf("unexpected import result: %#v", result)
	}
	result = readJSON(t, "messages", "--database", database)
	if result["total"] != float64(5) || len(result["messages"].([]any)) != 5 || result["limit"] != float64(0) {
		t.Fatalf("default query truncated records: %#v", result)
	}
	filtered := []string{"--database", database, "--session", "team@chatroom", "--start-date", "2026-10-20", "--end-date", "2026-10-20", "--keyword", "会议"}
	result = readJSON(t, append([]string{"messages"}, filtered...)...)
	if result["total"] != float64(2) || len(result["messages"].([]any)) != 2 {
		t.Fatalf("date/session/keyword filter mismatch: %#v", result)
	}
	paged := append(append([]string{"messages"}, filtered...), "--offset", "1", "--limit", "1")
	result = readJSON(t, paged...)
	if result["total"] != float64(2) || len(result["messages"].([]any)) != 1 || result["offset"] != float64(1) {
		t.Fatalf("pagination mismatch: %#v", result)
	}
	message := result["messages"].([]any)[0].(map[string]any)
	if message["content"] != "项目会议 Beta" {
		t.Fatalf("unstable query ordering: %#v", message)
	}
	result = readJSON(t, "messages", "--database", database, "--keyword", "ALPHA")
	if result["total"] != float64(1) {
		t.Fatalf("ASCII keyword search should ignore case: %#v", result)
	}
	result = readJSON(t, "messages", "--database", database, "--keyword", "不存在的词")
	if result["total"] != float64(0) || len(result["messages"].([]any)) != 0 {
		t.Fatalf("empty query mismatch: %#v", result)
	}
	result = readJSON(t, append([]string{"stats"}, filtered...)...)
	if result["total_messages"] != float64(2) || result["session_count"] != float64(1) || result["sent_messages"] != float64(1) || result["received_messages"] != float64(1) {
		t.Fatalf("stats mismatch: %#v", result)
	}
	code, stdout, stderr := runCLI(t, "sessions", "--database", database)
	var sessions []map[string]any
	if code != 0 || json.Unmarshal([]byte(stdout), &sessions) != nil || len(sessions) != 2 {
		t.Fatalf("sessions mismatch: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	// Explicit repeated imports must leave the database free of duplicates.
	result = readJSON(t, "import", "--database", database, "--export-dir", exports)
	if result["unchanged_files"] != float64(2) || result["changed_files"] != float64(0) {
		t.Fatalf("unchanged import was not incremental: %#v", result)
	}
	result = readJSON(t, "messages", "--database", database)
	if result["total"] != float64(5) {
		t.Fatalf("reimport duplicated messages: %#v", result)
	}
}

func TestGlobalFlagsBeforeAndAfterCommandAndRepeatedSessions(t *testing.T) {
	dir, exports, database := fixture(t)
	t.Chdir(dir)
	readJSON(t, "import", "--database", database, "--export-dir", exports)
	for _, args := range [][]string{
		{"--database", database, "messages", "--session", "team@chatroom", "--session", "friend"},
		{"messages", "--database", database, "--sessions", "team@chatroom,friend"},
	} {
		result := readJSON(t, args...)
		if result["total"] != float64(5) {
			t.Fatalf("query arguments mismatch: %#v", result)
		}
	}
}

func TestJSONWriteFailureIsReported(t *testing.T) {
	var stderr bytes.Buffer
	if code := Run([]string{"version"}, failingWriter{}, &stderr); code != 1 || stderr.Len() == 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestProcessStdoutContainsOnlyJSON(t *testing.T) {
	dir, exports, database := fixture(t)
	for _, command := range []string{"import", "status", "sessions", "messages", "stats"} {
		args := []string{"-test.run=^TestCLIProcessHelper$", "--", command, "--database", database, "--export-dir", exports}
		cmd := exec.Command(os.Args[0], args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "WXAGENT_CLI_TEST_PROCESS=1")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v, stderr=%q", command, err, stderr.String())
		}
		if !json.Valid(stdout.Bytes()) {
			t.Fatalf("%s wrote progress outside JSON to process stdout: %q", command, stdout.String())
		}
	}
}

// Running through a subprocess also catches accidental fmt.Print calls in
// dependencies, which an injected stdout buffer alone cannot detect.
func TestCLIProcessHelper(t *testing.T) {
	if os.Getenv("WXAGENT_CLI_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(Run(os.Args[i+1:], os.Stdout, os.Stderr))
		}
	}
	os.Exit(99)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// Fixtures are synthetic and isolated. The tests never access WeChat or launch
// the key helper, model providers, an HTTP listener, or a GUI.
func fixture(t *testing.T) (dir, exports, database string) {
	t.Helper()
	dir = t.TempDir()
	exports = filepath.Join(dir, "exports")
	database = filepath.Join(dir, "records.db")
	if err := os.MkdirAll(exports, 0700); err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	stamp := func(day, hour, minute, second int) int64 {
		return time.Date(2026, 10, day, hour, minute, second, 0, zone).Unix()
	}
	files := map[string]any{
		"team.json": map[string]any{
			"session_id": "team@chatroom", "session_name": "工作群",
			"messages": []map[string]any{
				{"create_time": stamp(19, 23, 59, 59), "str_content": "昨天的会议", "type": "文本消息", "is_send": 0},
				{"create_time": stamp(20, 0, 0, 0), "str_content": "项目会议 Alpha", "type": "文本消息", "is_send": 1},
				{"create_time": stamp(20, 23, 59, 59), "str_content": "项目会议 Beta", "type": "文本消息", "is_send": 0},
				{"create_time": stamp(21, 0, 0, 0), "str_content": "明天的会议", "type": "文本消息", "is_send": 0},
			},
		},
		"friend.json": map[string]any{
			"session_id": "friend", "session_name": "朋友",
			"messages": []map[string]any{
				{"create_time": stamp(20, 12, 0, 0), "str_content": "图片", "type": "图片消息", "is_send": 0},
			},
		},
	}
	for name, value := range files {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(exports, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, exports, database
}
