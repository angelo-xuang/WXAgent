// Package cli provides the shared command line interface for wxagent and GUI launchers.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/dataset"
	"github.com/billyoftea/wxagent/go_backend/internal/pipeline"
	"github.com/billyoftea/wxagent/go_backend/internal/server"
)

// Version can be set at build time with -ldflags -X.
var Version = "dev"

type app struct {
	out io.Writer
	err io.Writer
}

type globals struct {
	configPath   string
	exportDir    string
	databasePath string
}

// Run executes a command without exiting the process. Structured results go to
// stdout; diagnostics go to stderr. Exit codes: 0 success, 1 operation failed,
// 2 invalid command or argument.
func Run(args []string, stdout, stderr io.Writer) int {
	return (&app{out: stdout, err: stderr}).run(args)
}

func (a *app) run(args []string) int {
	options := &globals{}
	root := a.flags("wxagent", options)
	showHelp := root.Bool("help", false, "显示帮助")
	root.BoolVar(showHelp, "h", false, "显示帮助")
	showVersion := root.Bool("version", false, "显示版本")
	root.Usage = func() { a.usage(a.err) }
	if err := root.Parse(args); err != nil {
		return 2
	}
	if *showHelp {
		a.usage(a.out)
		return 0
	}
	if *showVersion {
		if root.NArg() != 0 {
			return a.fail(2, errors.New("--version 不能与命令同时使用"))
		}
		return a.version()
	}
	if root.NArg() == 0 {
		a.usage(a.out)
		return 0
	}
	command, rest := root.Arg(0), root.Args()[1:]
	if command == "help" {
		if len(rest) == 0 {
			a.usage(a.out)
			return 0
		}
		if len(rest) != 1 || !knownCommand(rest[0]) {
			return a.fail(2, errors.New("用法：wxagent help [命令]"))
		}
		return a.command(rest[0], []string{"--help"}, options)
	}
	if !knownCommand(command) {
		return a.fail(2, fmt.Errorf("未知命令 %q；运行 wxagent help 查看可用命令", command))
	}
	return a.command(command, rest, options)
}

func knownCommand(name string) bool {
	switch name {
	case "version", "init", "status", "import", "sessions", "messages", "stats", "export", "refresh-key", "serve", "server":
		return true
	default:
		return false
	}
}

func (a *app) flags(name string, options *globals) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.err)
	fs.Func("config", "配置文件路径；省略时读取当前目录 config.json 或使用安全默认值", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("配置文件路径不能为空")
		}
		options.configPath = value
		return nil
	})
	fs.Func("export-dir", "导入来源或原生导出的 JSON 目录；相对路径以当前工作目录为基准", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("导出目录不能为空")
		}
		options.exportDir = value
		return nil
	})
	fs.Func("database", "SQLite 索引数据库路径；相对路径以当前工作目录为基准", func(value string) error {
		if strings.TrimSpace(value) == "" {
			return errors.New("数据库路径不能为空")
		}
		options.databasePath = value
		return nil
	})
	return fs
}

func (a *app) command(name string, args []string, options *globals) int {
	fs := a.flags(name, options)
	showHelp := fs.Bool("help", false, "显示此命令的帮助")
	fs.BoolVar(showHelp, "h", false, "显示此命令的帮助")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "用法：wxagent %s [选项]\n\n", name)
		fs.PrintDefaults()
	}
	query := dataset.QueryOptions{}
	var wechatDir, host string
	var ensure bool
	var wait, poll, port int
	switch name {
	case "messages", "stats":
		fs.Func("session", "准确会话 ID，可重复指定；可先用 sessions 查看", func(value string) error {
			if strings.TrimSpace(value) == "" {
				return errors.New("会话不能为空")
			}
			query.Sessions = append(query.Sessions, strings.TrimSpace(value))
			return nil
		})
		fs.Func("sessions", "多个准确会话 ID，以逗号分隔", func(value string) error {
			for _, session := range strings.Split(value, ",") {
				if strings.TrimSpace(session) == "" {
					return errors.New("会话列表不能含空值")
				}
				query.Sessions = append(query.Sessions, strings.TrimSpace(session))
			}
			return nil
		})
		fs.StringVar(&query.Keyword, "keyword", "", "按消息内容关键字筛选（不区分英文大小写）")
		addDateFlags(fs, &query)
		if name == "messages" {
			fs.IntVar(&query.Offset, "offset", 0, "跳过的匹配消息数")
			fs.IntVar(&query.Limit, "limit", 0, "最多返回的消息数；0 返回全部，不默认截断")
		}
	case "export":
		fs.StringVar(&wechatDir, "wechat-dir", "", "Windows 微信数据目录；省略时使用配置或自动查找")
		addDateFlags(fs, &query)
	case "refresh-key":
		fs.BoolVar(&ensure, "ensure", false, "找不到密钥时启动已配置的 Windows 密钥工具")
		fs.IntVar(&wait, "wait", 120, "启动密钥工具后最多等待的秒数")
		fs.IntVar(&poll, "poll", 5, "检查密钥的间隔秒数")
	case "serve", "server":
		fs.StringVar(&host, "host", "127.0.0.1", "HTTP 监听地址，默认仅本机")
		fs.IntVar(&port, "port", 8000, "HTTP 监听端口")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showHelp {
		fs.SetOutput(a.out)
		fs.Usage()
		return 0
	}
	if fs.NArg() != 0 {
		return a.fail(2, fmt.Errorf("%s 不接受位置参数：%s", name, strings.Join(fs.Args(), " ")))
	}
	if err := validateQuery(query); err != nil {
		return a.fail(2, err)
	}
	if name == "version" {
		return a.version()
	}
	if name == "init" {
		return a.init(options)
	}
	if name == "refresh-key" && (wait <= 0 || poll <= 0) {
		return a.fail(2, errors.New("--wait 和 --poll 必须大于 0"))
	}
	if (name == "serve" || name == "server") && (port < 1 || port > 65535 || strings.TrimSpace(host) == "") {
		return a.fail(2, errors.New("监听地址不能为空，端口必须在 1 到 65535 之间"))
	}
	cfg, err := loadConfig(options)
	if err != nil {
		return a.fail(1, err)
	}
	p, err := pipeline.New(cfg)
	if err != nil {
		return a.fail(1, fmt.Errorf("初始化后端：%w", err))
	}
	var result any
	switch name {
	case "status":
		status := p.GetStatus()
		if indexErr, ok := status["index_error"]; ok {
			return a.fail(1, fmt.Errorf("读取索引数据库：%v", indexErr))
		}
		result = status
	case "import":
		result, err = p.Import("")
	case "sessions":
		result, err = p.ListSessions()
	case "messages":
		result, err = p.QueryMessages(query)
	case "stats":
		result, err = p.Stats(query)
	case "refresh-key":
		key, keyErr := p.RefreshKey(ensure, wait, poll)
		err = keyErr
		if err == nil {
			// A command should not accidentally leak database keys into shell logs.
			result = map[string]any{"status": "ready", "timestamp": key.Timestamp}
		}
	case "export":
		err = p.ExportAuto(wechatDir, query.StartDate, query.EndDate)
		result = map[string]any{"status": "success", "indexed": true, "export_dir": cfg.ExportDir, "database_path": cfg.DatabasePath}
	case "serve", "server":
		addr := net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
		fmt.Fprintf(a.err, "本地 HTTP 服务：http://%s\n", addr)
		if err := server.New(p).Run(addr); err != nil {
			return a.fail(1, fmt.Errorf("HTTP 服务：%w", err))
		}
		return 0
	}
	if err != nil {
		return a.fail(1, err)
	}
	return a.json(result)
}

func addDateFlags(fs *flag.FlagSet, query *dataset.QueryOptions) {
	fs.StringVar(&query.StartDate, "start-date", "", "起始日期 YYYY-MM-DD，包含当天，时区 Asia/Shanghai")
	fs.StringVar(&query.EndDate, "end-date", "", "结束日期 YYYY-MM-DD，包含当天，时区 Asia/Shanghai")
}

func validateQuery(query dataset.QueryOptions) error {
	if query.Offset < 0 || query.Limit < 0 {
		return errors.New("--offset 和 --limit 不能为负数")
	}
	for _, date := range []string{query.StartDate, query.EndDate} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return fmt.Errorf("无效日期 %q，请使用 YYYY-MM-DD", date)
			}
		}
	}
	if query.StartDate != "" && query.EndDate != "" && query.StartDate > query.EndDate {
		return errors.New("开始日期不能晚于结束日期")
	}
	return nil
}

func loadConfig(options *globals) (*config.PipelineConfig, error) {
	var cfg *config.PipelineConfig
	var err error
	if options.configPath != "" {
		cfg, err = config.LoadPipeline(options.configPath)
	} else {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return nil, cwdErr
		}
		path := filepath.Join(cwd, "config.json")
		if _, statErr := os.Stat(path); statErr == nil {
			cfg, err = config.LoadPipeline(path)
		} else if errors.Is(statErr, os.ErrNotExist) {
			cfg = config.DefaultPipeline(cwd)
		} else {
			return nil, fmt.Errorf("读取默认配置：%w", statErr)
		}
	}
	if err != nil {
		return nil, err
	}
	if options.exportDir != "" {
		cfg.ExportDir, err = absolutePath(options.exportDir)
		if err != nil {
			return nil, err
		}
	}
	if options.databasePath != "" {
		cfg.DatabasePath, err = absolutePath(options.databasePath)
		if err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func absolutePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimLeft(path[1:], "/\\"))
	}
	return filepath.Abs(path)
}

func (a *app) init(options *globals) int {
	path := options.configPath
	if path == "" {
		path = "config.json"
	}
	path, err := absolutePath(path)
	if err != nil {
		return a.fail(1, err)
	}
	exportDir := "./exports"
	if options.exportDir != "" {
		exportDir, err = absolutePath(options.exportDir)
		if err != nil {
			return a.fail(1, err)
		}
	}
	databasePath := "./data/wxagent.db"
	if options.databasePath != "" {
		databasePath, err = absolutePath(options.databasePath)
		if err != nil {
			return a.fail(1, err)
		}
	}
	template := map[string]any{
		"export_dir":    exportDir,
		"database_path": databasePath,
		"state_file":    "./.wxagent/state.json",
	}
	data, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return a.fail(1, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return a.fail(1, err)
	}
	// O_EXCL guarantees that init never truncates an existing configuration,
	// including when two processes initialize the same path concurrently.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return a.fail(1, fmt.Errorf("新建配置（不覆盖已有文件）：%w", err))
	}
	_, writeErr := file.Write(append(data, '\n'))
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return a.fail(1, errors.Join(writeErr, closeErr))
	}
	return a.json(map[string]any{"status": "created", "config_path": path})
}

func (a *app) version() int {
	return a.json(map[string]string{"name": "wxagent", "version": Version, "platform": runtime.GOOS, "arch": runtime.GOARCH})
}

func (a *app) json(value any) int {
	encoder := json.NewEncoder(a.out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return a.fail(1, fmt.Errorf("写入 JSON 输出：%w", err))
	}
	return 0
}

func (a *app) fail(code int, err error) int {
	fmt.Fprintf(a.err, "错误：%s\n", err)
	return code
}

func (a *app) usage(out io.Writer) {
	fmt.Fprint(out, `WXAgent — 本地微信记录索引数据库与 CLI

用法：wxagent [全局选项] <命令> [命令选项]

命令：
  init          新建最小配置；已有文件不会被覆盖
  status        查看索引库状态、路径、平台及可用能力
  import        将导出 JSON 增量导入 SQLite；重复导入不会重复记账
  sessions      列出索引数据库中的会话
  messages      从索引库按会话、日期和关键字查询消息
  stats         统计索引库中匹配消息的数量、类型和收发情况
  export        从 Windows 微信获取记录、保存 JSON 并导入索引库
  refresh-key   读取或刷新已配置的 Windows 数据库密钥
  serve         启动可选的本地 HTTP 服务（兼容别名 server）
  version       查看版本
  help          查看帮助，例如 wxagent help messages

全局选项（也可放在命令后）：
  --config <路径>       使用指定配置；不存在时明确报错
  --database <路径>     使用指定 SQLite 索引库，无需先创建配置
  --export-dir <路径>   import 的 JSON 来源目录或 export 的输出目录

数据结果以 JSON 写入 stdout，运行日志写入 stderr。
messages 默认返回全部匹配消息；--offset / --limit 可显式分页。
日期采用 Asia/Shanghai 时区，起止日期均包含当天。
查询命令不会隐式导入或改写记录；已有 JSON 用 import，Windows 原生采集用 export。
索引导入和查询支持 Windows、macOS 和 Linux；微信原生导出目前限 Windows。

示例：
  wxagent init
  wxagent --database ./data/records.db --export-dir ./exports import
  wxagent --database ./data/records.db sessions
  wxagent messages --database ./data/records.db --session "room@chatroom" --keyword "会议"
  wxagent messages --start-date 2026-10-01 --end-date 2026-10-22 --limit 100
  wxagent stats --database ./data/records.db
  wxagent serve --port 8000
`)
}
