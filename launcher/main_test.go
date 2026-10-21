package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInstalledConfigDiscovery(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "versions", "1.1.0")
	if err := os.MkdirAll(versionDir, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := installedConfig(versionDir); got != "" || err != nil {
		t.Fatalf("unmanaged bundle config = %q, %v", got, err)
	}
	dataRoot := t.TempDir()
	marker := map[string]string{"product": "WXAgent", "install_root": root, "data_root": dataRoot}
	writeMarker := func() {
		t.Helper()
		raw, _ := json.Marshal(marker)
		if err := os.WriteFile(filepath.Join(root, "current.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeMarker()
	got, err := installedConfig(versionDir)
	if err != nil || got != filepath.Join(dataRoot, "config.json") {
		t.Fatalf("installed config = %q, %v", got, err)
	}
	marker["install_root"] = dataRoot
	writeMarker()
	if _, err := installedConfig(versionDir); err == nil {
		t.Fatal("accepted marker for another installation")
	}
	if got, err := installedConfig(root); err != nil || got != "" {
		t.Fatalf("portable bundle unexpectedly uses installation config: %q, %v", got, err)
	}
}

func TestBackendArgumentsUsesExplicitUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user config.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := backendArguments(options{port: 8123, configPath: path})
	want := []string{"--config", path, "server", "--port=8123", "--host=127.0.0.1"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %v, %v; want %v", got, err, want)
	}
	if _, err := backendArguments(options{configPath: filepath.Dir(path)}); err == nil {
		t.Fatal("directory accepted as configuration")
	}
	if _, err := backendArguments(options{configPath: path + ".missing"}); err == nil {
		t.Fatal("missing configuration accepted")
	}
	got, err = backendArguments(options{port: 8000})
	if err != nil || !reflect.DeepEqual(got, []string{"server", "--port=8000", "--host=127.0.0.1"}) {
		t.Fatalf("portable arguments = %v, %v", got, err)
	}
}

func TestPlatformCandidates(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		backends := backendCandidates("bundle", goos)
		if backends[0] != filepath.Join("bundle", "backend", executableName("wxagent", goos)) {
			t.Fatalf("CLI is not the preferred backend: %v", backends)
		}
		// A root wxagent.exe would be the WXAgent.exe launcher itself on Windows.
		for _, path := range backends {
			if strings.EqualFold(path, filepath.Join("bundle", "WXAgent.exe")) {
				t.Fatal("backend lookup can recursively launch the desktop launcher")
			}
		}
		paths := append(backendCandidates("bundle", goos), frontendCandidates("bundle", goos)...)
		foundApp := false
		for _, path := range paths {
			if strings.HasSuffix(path, ".exe") != (goos == "windows") {
				t.Errorf("incorrect executable extension for %s: %s", goos, path)
			}
			foundApp = foundApp || strings.Contains(path, ".app")
		}
		if goos == "darwin" && !foundApp {
			t.Fatal("missing macOS app bundle candidate")
		}
	}
}

func TestFindExecutableSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app")
	if err := os.WriteFile(file, nil, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := findExecutable([]string{dir, filepath.Join(dir, "missing"), file})
	if err != nil || got != file {
		t.Fatalf("findExecutable = %q, %v", got, err)
	}
}

func TestInvalidOptions(t *testing.T) {
	for _, opts := range []options{{port: 0}, {port: 65536}, {port: 8000, backendOnly: true, frontendOnly: true}} {
		if err := run(context.Background(), t.TempDir(), opts); err == nil {
			t.Fatal("expected option validation failure")
		}
	}
}

func TestBackendReadiness(t *testing.T) {
	for _, status := range []string{"ok", "unavailable"} {
		t.Run(status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"status":%q}`, status)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			err := waitForBackend(ctx, server.URL, &childProcess{done: make(chan struct{})})
			if status == "ok" && err != nil || status != "ok" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("readiness error = %v", err)
			}
		})
	}
}

func TestBackendReadinessDetectsExit(t *testing.T) {
	process := &childProcess{done: make(chan struct{}), err: errors.New("startup failure")}
	close(process.done)
	if err := waitForBackend(context.Background(), "http://127.0.0.1:1", process); err == nil || !strings.Contains(err.Error(), "startup failure") {
		t.Fatalf("lost process exit: %v", err)
	}
}

func TestChildProcessIsReaped(t *testing.T) {
	// Execute only this test in a child process, so no app or user data is touched.
	if os.Getenv("WXAGENT_TEST_CHILD") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcessIsReaped$")
	cmd.Env = append(os.Environ(), "WXAGENT_TEST_CHILD=1")
	process, err := startChild(cmd)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.done:
	case <-time.After(10 * time.Second):
		process.stop()
		t.Fatal("child was not reaped")
	}
	process.stop() // Must also be safe after Wait has completed.
	if process.err != nil {
		t.Fatal(process.err)
	}
}

func TestStopTerminatesRunningChild(t *testing.T) {
	if os.Getenv("WXAGENT_TEST_SLEEPING_CHILD") == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStopTerminatesRunningChild$")
	cmd.Env = append(os.Environ(), "WXAGENT_TEST_SLEEPING_CHILD=1")
	process, err := startChild(cmd)
	if err != nil {
		t.Fatal(err)
	}
	process.stop()
	select {
	case <-process.done:
	default:
		t.Fatal("stopped process was not reaped")
	}
}
