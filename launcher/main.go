// WXAgent Launcher starts and supervises the desktop app and its local backend.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

type options struct {
	port         int
	showConsole  bool
	backendOnly  bool
	frontendOnly bool
	configPath   string
}

func main() {
	var opts options
	flag.IntVar(&opts.port, "port", 8000, "后端服务器端口")
	flag.BoolVar(&opts.showConsole, "console", false, "显示后端控制台")
	flag.BoolVar(&opts.backendOnly, "backend-only", false, "仅启动后端")
	flag.BoolVar(&opts.frontendOnly, "frontend-only", false, "仅启动前端")
	flag.StringVar(&opts.configPath, "config", "", "用户配置文件路径")
	flag.Parse()

	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("获取执行路径失败: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, filepath.Dir(exePath), opts); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

func (o options) validate() error {
	if o.port < 1 || o.port > 65535 {
		return errors.New("端口必须在 1–65535 之间")
	}
	if o.backendOnly && o.frontendOnly {
		return errors.New("--backend-only 与 --frontend-only 不能同时使用")
	}
	return nil
}

func run(ctx context.Context, baseDir string, opts options) error {
	if err := opts.validate(); err != nil {
		return err
	}
	if opts.configPath == "" {
		var err error
		opts.configPath, err = installedConfig(baseDir)
		if err != nil {
			return err
		}
	}
	fmt.Printf("WXAgent · 工作目录: %s\n", baseDir)
	backendURL := fmt.Sprintf("http://127.0.0.1:%d", opts.port)
	var backend *childProcess
	if !opts.frontendOnly {
		path, err := findExecutable(backendCandidates(baseDir, runtime.GOOS))
		if err != nil {
			return fmt.Errorf("未找到后端程序: %w", err)
		}
		args, err := backendArguments(opts)
		if err != nil {
			return err
		}
		cmd := exec.Command(path, args...)
		cmd.Dir = baseDir
		cmd.Stderr = os.Stderr
		if opts.showConsole {
			cmd.Stdout = os.Stdout
		}
		configureBackendProcess(cmd, opts.showConsole)
		backend, err = startChild(cmd)
		if err != nil {
			return fmt.Errorf("启动后端: %w", err)
		}
		defer backend.stop()
		readyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = waitForBackend(readyCtx, backendURL, backend)
		cancel()
		if err != nil {
			return err
		}
		fmt.Printf("后端已就绪: %s\n", backendURL)
	}

	var frontend *childProcess
	if !opts.backendOnly {
		path, err := findExecutable(frontendCandidates(baseDir, runtime.GOOS))
		if err != nil {
			return fmt.Errorf("未找到前端程序: %w", err)
		}
		cmd := exec.Command(path)
		cmd.Dir = filepath.Dir(path)
		cmd.Env = append(os.Environ(), "WXAGENT_BACKEND_URL="+backendURL)
		cmd.Stderr = os.Stderr
		frontend, err = startChild(cmd)
		if err != nil {
			return fmt.Errorf("启动前端: %w", err)
		}
		defer frontend.stop()
	}

	fmt.Println("WXAgent 已启动，关闭窗口或按 Ctrl+C 退出。")
	var backendDone, frontendDone <-chan struct{}
	if backend != nil {
		backendDone = backend.done
	}
	if frontend != nil {
		frontendDone = frontend.done
	}
	select {
	case <-ctx.Done():
		return nil
	case <-frontendDone:
		if frontend.err != nil {
			return fmt.Errorf("前端退出: %w", frontend.err)
		}
		return nil
	case <-backendDone:
		return fmt.Errorf("后端已退出: %v", backend.err)
	}
}

// App Paths can launch an executable but cannot supply its arguments. Recognize
// the installer's marker so that this entry point shares the same user database
// as the shortcuts and CLI; portable bundles retain their local configuration.
func installedConfig(baseDir string) (string, error) {
	if filepath.Base(filepath.Dir(baseDir)) != "versions" {
		return "", nil
	}
	root := filepath.Dir(filepath.Dir(baseDir))
	raw, err := os.ReadFile(filepath.Join(root, "current.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取安装记录: %w", err)
	}
	var marker struct {
		Product     string `json:"product"`
		InstallRoot string `json:"install_root"`
		DataRoot    string `json:"data_root"`
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return "", fmt.Errorf("解析安装记录: %w", err)
	}
	if marker.Product != "WXAgent" || !filepath.IsAbs(marker.InstallRoot) || !filepath.IsAbs(marker.DataRoot) {
		return "", errors.New("无效的 WXAgent 安装记录")
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	markerRootInfo, err := os.Stat(marker.InstallRoot)
	if err != nil || !os.SameFile(rootInfo, markerRootInfo) {
		return "", errors.New("WXAgent 安装记录与程序路径不一致")
	}
	return filepath.Join(marker.DataRoot, "config.json"), nil
}

func backendArguments(opts options) ([]string, error) {
	args := []string{}
	if opts.configPath != "" {
		path, err := filepath.Abs(opts.configPath)
		if err != nil {
			return nil, fmt.Errorf("解析配置路径: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("读取配置文件: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("配置路径必须是文件")
		}
		args = append(args, "--config", path)
	}
	return append(args, "server", fmt.Sprintf("--port=%d", opts.port), "--host=127.0.0.1"), nil
}

type childProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error // Written before done closes; readers wait for done.
}

func startChild(cmd *exec.Cmd) (*childProcess, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	child := &childProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		child.err = cmd.Wait()
		close(child.done)
	}()
	return child, nil
}

func (p *childProcess) stop() {
	select {
	case <-p.done:
		return
	default:
		_ = p.cmd.Process.Kill()
		<-p.done // Reap each process exactly once, including failure paths.
	}
}

func waitForBackend(ctx context.Context, baseURL string, process *childProcess) error {
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-process.done:
			return fmt.Errorf("后端在就绪前退出: %v", process.err)
		case <-ctx.Done():
			return fmt.Errorf("等待后端就绪: %w", ctx.Err())
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			var health struct {
				Status string `json:"status"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&health)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil && health.Status == "ok" {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待后端就绪: %w", ctx.Err())
		case <-process.done:
			return fmt.Errorf("后端在就绪前退出: %v", process.err)
		case <-ticker.C:
		}
	}
}

func executableName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

func backendCandidates(baseDir, goos string) []string {
	paths := []string{}
	for _, name := range []string{"wxagent", "wxagent_backend", "wx_agent_backend"} {
		paths = append(paths, filepath.Join(baseDir, "backend", executableName(name, goos)))
	}
	for _, name := range []string{"wxagent_backend", "wx_agent_backend", "wx_agent"} {
		paths = append(paths, filepath.Join(baseDir, executableName(name, goos)))
	}
	return paths
}

func frontendCandidates(baseDir, goos string) []string {
	name := executableName("wx_agent_app", goos)
	paths := []string{
		filepath.Join(baseDir, "frontend", name),
		filepath.Join(baseDir, name),
		filepath.Join(baseDir, "bin", "wx_agent_app", name),
	}
	if goos == "darwin" {
		for _, app := range []string{"wx_agent_app.app", "WXAgent.app"} {
			paths = append(paths,
				filepath.Join(baseDir, "frontend", app, "Contents", "MacOS", name),
				filepath.Join(baseDir, app, "Contents", "MacOS", name),
			)
		}
	}
	return paths
}

func findExecutable(candidates []string) (string, error) {
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("已检查 %v", candidates)
}
