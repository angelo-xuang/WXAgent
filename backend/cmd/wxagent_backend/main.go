package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/pipeline"
	"github.com/billyoftea/wxagent/go_backend/internal/server"
)

func main() {
	configPath := flag.String("config", "", "Path to config.json")
	flag.Parse()

	if flag.NArg() < 1 {
		printUsage()
		os.Exit(1)
	}

	cmd := flag.Arg(0)

	// Load config
	cfg, err := config.LoadPipeline(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	pipe, err := pipeline.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing pipeline: %v\n", err)
		os.Exit(1)
	}

	switch cmd {
	case "refresh-key":
		runRefreshKey(pipe)
	case "export":
		runExport(pipe)
	case "export-auto":
		runExportAuto(pipe)
	case "analyze-chat":
		runAnalyzeChat(pipe)
	case "summarize":
		runSummarize(pipe)
	case "server":
		runServer(pipe)
	case "start":
		runStart(pipe)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: wxagent_backend [options] <command>")
	fmt.Println("Commands:")
	fmt.Println("  start         Start backend server and launch UI")
	fmt.Println("  refresh-key   Refresh WeChat database key")
	fmt.Println("  export        Trigger echotrace GUI export")
	fmt.Println("  export-auto   Automatic export (no GUI)")
	fmt.Println("  analyze-chat  Analyze chat messages with AI (DeepSeek)")
	fmt.Println("  summarize     Run summary pipeline")
	fmt.Println("  server        Start HTTP server")
	fmt.Println("Options:")
	flag.PrintDefaults()
}

func runRefreshKey(p *pipeline.Pipeline) {
	fs := flag.NewFlagSet("refresh-key", flag.ExitOnError)
	ensure := fs.Bool("ensure", false, "Auto launch wx_key if missing")
	wait := fs.Int("wait", 120, "Wait seconds")
	poll := fs.Int("poll", 5, "Poll interval seconds")
	fs.Parse(flag.Args()[1:])

	payload, err := p.RefreshKey(*ensure, *wait, *poll)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error refreshing key: %v\n", err)
		os.Exit(1)
	}

	out, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Println(string(out))
}

func runExport(p *pipeline.Pipeline) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	fs.Parse(flag.Args()[1:])

	if err := p.TriggerExport(fs.Args(), false); err != nil {
		fmt.Fprintf(os.Stderr, "Error triggering export: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Export triggered successfully.")
}

func runExportAuto(p *pipeline.Pipeline) {
	fs := flag.NewFlagSet("export-auto", flag.ExitOnError)
	wechatDir := fs.String("wechat-dir", "", "WeChat data directory (auto-detect if empty)")
	startDate := fs.String("start-date", "", "Start date YYYY-MM-DD (incremental if empty)")
	endDate := fs.String("end-date", "", "End date YYYY-MM-DD (default: today)")
	fs.Parse(flag.Args()[1:])

	if err := p.ExportAuto(*wechatDir, *startDate, *endDate); err != nil {
		fmt.Fprintf(os.Stderr, "Error during auto export: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Auto export completed successfully.")
}

func runAnalyzeChat(p *pipeline.Pipeline) {
	fs := flag.NewFlagSet("analyze-chat", flag.ExitOnError)
	startDate := fs.String("start-date", "", "Start date YYYY-MM-DD")
	endDate := fs.String("end-date", "", "End date YYYY-MM-DD (default: today)")
	sessions := fs.String("sessions", "", "Comma-separated session names (empty for all)")
	maxTokens := fs.Int("max-tokens", 80000, "Max tokens per chunk (default: 80000)")
	output := fs.String("output", "", "Output file path (default: output/chat_analysis.md)")
	fs.Parse(flag.Args()[1:])

	// 解析会话列表
	var sessionList []string
	if *sessions != "" {
		sessionList = strings.Split(*sessions, ",")
		for i := range sessionList {
			sessionList[i] = strings.TrimSpace(sessionList[i])
		}
	}

	fmt.Println("=== 微信聊天记录智能分析 ===")
	fmt.Println()
	result, err := p.AnalyzeChat(*startDate, *endDate, sessionList, *maxTokens, *output, "", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error analyzing chat: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n=== 分析完成 ===")
	fmt.Printf("✓ 分析消息: %d 条\n", result.TotalMessages)
	fmt.Printf("✓ 涉及会话: %d 个\n", result.TotalSessions)
	fmt.Printf("✓ 分段处理: %d 段\n", result.ChunkCount)
	fmt.Printf("✓ 报告保存: %s\n", result.OutputFile)
}

func runSummarize(p *pipeline.Pipeline) {
	fs := flag.NewFlagSet("summarize", flag.ExitOnError)
	start := fs.String("start-date", "", "Start date YYYY-MM-DD")
	end := fs.String("end-date", "", "End date YYYY-MM-DD")
	full := fs.Bool("full", false, "Full export (ignore incremental)")
	// questions handling is tricky with flag, skipping for now or simple string
	fs.Parse(flag.Args()[1:])

	res, err := p.RunSummary(*start, *end, !*full, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running summary: %v\n", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
}

func runServer(p *pipeline.Pipeline) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	port := fs.Int("port", 8000, "Port to listen on")
	host := fs.String("host", "127.0.0.1", "Host to listen on")
	fs.Parse(flag.Args()[1:])

	addr := fmt.Sprintf("%s:%d", *host, *port)
	srv := server.New(p)

	fmt.Printf("🚀 Starting server on %s...\n", addr)
	if err := srv.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func runStart(p *pipeline.Pipeline) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	port := fs.Int("port", 8000, "Port to listen on")
	host := fs.String("host", "127.0.0.1", "Host to listen on")
	uiPath := fs.String("ui", "echotrace.exe", "Path to UI executable")
	fs.Parse(flag.Args()[1:])

	addr := fmt.Sprintf("%s:%d", *host, *port)
	srv := server.New(p)

	// Start server in background
	go func() {
		fmt.Printf("🚀 Starting backend server on %s...\n", addr)
		if err := srv.Run(addr); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	}()

	// Give server a moment to start
	time.Sleep(1 * time.Second)

	// Resolve UI path
	finalUIPath := *uiPath
	if finalUIPath == "echotrace.exe" {
		if _, err := os.Stat(finalUIPath); os.IsNotExist(err) {
			// Try bundled path
			bundledPath := "bin/echotrace/echotrace.exe"
			if _, err := os.Stat(bundledPath); err == nil {
				finalUIPath = bundledPath
			} else {
				// Try dev path
				devPath := "../echotrace/build/windows/x64/runner/Release/echotrace.exe"
				if _, err := os.Stat(devPath); err == nil {
					finalUIPath = devPath
				}
			}
		}
	}

	// Launch UI
	fmt.Printf("🎨 Launching UI: %s...\n", finalUIPath)
	cmd := exec.Command(finalUIPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Printf("❌ Failed to start UI: %v\n", err)
		fmt.Println("Running in server-only mode. Press Ctrl+C to exit.")
		select {} // Block forever
	}

	// Wait for UI to exit
	if err := cmd.Wait(); err != nil {
		fmt.Printf("UI exited with error: %v\n", err)
	}

	fmt.Println("👋 UI closed, shutting down...")
}
