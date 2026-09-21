// Command streamguard 是本地大模型 API 限流代理（支持 Windows / macOS / Linux）。
//
// 功能：监听本地端口，接收 /v1/chat/completions 请求，按配置的速率
// 排队等待限流后转发到上游模型服务，并逐块透传 SSE 流式响应。
//
// 用法：
//
//	streamguard.exe -config config.local.json   # 命令行模式
//	streamguard.exe -gui                        # 桌面界面模式
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/streamguard/streamguard/internal/app"
	"github.com/streamguard/streamguard/internal/config"
)

// version 是程序版本号，构建时可通过 -ldflags 注入。
var version = "dev"

// commit 是构建时的 git 提交短哈希，构建时可通过 -ldflags 注入。
var commit = "unknown"

const banner = `
   _____ __             __  ______                     __
  / ___// /__________ _/ / / ____/___  __  ___________/ /
  \__ \/ __/ ___/ __ / / / / __/ __ \/ / / / ___/ __  / 
 ___/ / /_/ /  / /_/ / / / /_/ / /_/ / /_/ / /  / /_/ /  
/____/\__/_/   \__,_/_/  \____/\____/\__,_/_/   \__,_/   
                                                         
  Local LLM API Rate-Limit Proxy  v%s (%s)  (%s/%s)
`

func main() {
	if err := run(); err != nil {
		log.Fatalf("startup failed: %v", err)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to config file (JSON); auto-discovers config.local.json if empty")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("streamguard %s (%s)\n", version, commit)
		return nil
	}

	// 1. Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	logger := log.New(os.Stdout, "", log.LstdFlags)
	fmt.Printf(banner, version, commit, runtime.GOOS, runtime.GOARCH)

	// Show the config file actually used, to help diagnose "my change had no effect".
	if used := resolveConfigPath(*configPath); used != "" {
		logger.Printf("config file: %s", used)
	} else {
		logger.Printf("config file: not found, using built-in defaults")
	}

	logger.Printf("config loaded: listen=%s upstream=%s preserve_host=%v rate=%.2f/s burst=%d max_wait=%v",
		cfg.Listen, cfg.Upstream, cfg.PreserveHost, cfg.Rate, cfg.Burst, cfg.MaxWait.Duration())

	// 2. Create app and start
	a := app.New(cfg)
	a.SetLogger(logger)

	if err := a.Start(); err != nil {
		return err
	}

	logger.Printf("rate limit: waiting mode, %.2f req/s, burst %d, max wait %v",
		cfg.Rate, cfg.Burst, cfg.MaxWait.Duration())
	logger.Printf("proxy address : http://%s", a.Addr())
	logger.Printf("upstream      : %s", cfg.Upstream)

	if cfg.LogLevel == "debug" {
		if cfg.LogFile != "" {
			logger.Printf("verbose mode  : ON (writing full request/response to log file)")
		} else {
			logger.Printf("verbose mode  : ON (printing full request/response to console)")
		}
	} else {
		logger.Printf("verbose mode  : OFF (set log_level=debug to print full request/response)")
	}

	// 3. Wait for exit signal, shut down gracefully.
	//    SIGHUP (Unix) triggers a config reload without restarting the process.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	reload := make(chan os.Signal, 1)
	notifyReload(reload)

	for {
		select {
		case sig := <-quit:
			logger.Printf("received signal %v, shutting down gracefully...", sig)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := a.Stop(ctx)
			cancel()
			if err != nil {
				return err
			}
			logger.Printf("StreamGuard stopped")
			return nil

		case <-reload:
			logger.Printf("received reload signal, reloading config...")
			newCfg, err := loadConfig(*configPath)
			if err != nil {
				logger.Printf("config reload failed, keeping current config: %v", err)
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err = a.UpdateConfig(ctx, newCfg)
			cancel()
			if err != nil {
				logger.Printf("config reload failed, keeping current config: %v", err)
				continue
			}
			logger.Printf("config reloaded: listen=%s upstream=%s rate=%.2f/s burst=%d",
				newCfg.Listen, newCfg.Upstream, newCfg.Rate, newCfg.Burst)
		}
	}
}

// notifyReload 注册配置热重载信号。
//
// Unix 平台监听 SIGHUP（标准做法）；Windows 无 SIGHUP，
// 此处为空实现，热重载通过 GUI 的「保存并重启」或重启进程完成。
func notifyReload(ch chan<- os.Signal) {
	notifyReloadSignal(ch)
}

// loadConfig 加载配置：默认值 → 配置文件 → 环境变量。
//
// path 为空时，会自动按以下顺序查找配置文件（找到第一个即用）：
//  1. 当前工作目录下的 config.local.json
//  2. 可执行文件所在目录下的 config.local.json
//  3. 可执行文件上级目录下的 config.local.json（适配 dist/ 布局）
//
// 均未找到时使用内置默认配置。
func loadConfig(path string) (*config.Config, error) {
	var cfg *config.Config

	if path == "" {
		path = findConfigFile()
	}

	if path == "" {
		cfg = config.Default()
	} else {
		loaded, err := config.Load(path)
		if err != nil {
			return nil, err
		}
		cfg = loaded
	}

	cfg.ApplyEnv()
	cfg.Normalize()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置校验失败: %w", err)
	}
	return cfg, nil
}

// findConfigFile 按优先级查找默认配置文件，返回空字符串表示未找到。
func findConfigFile() string {
	const name = "config.local.json"

	candidates := []string{name}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, name),
			// 适配 dist/ 布局：exe 在 dist/，配置在仓库根目录
			filepath.Join(filepath.Dir(exeDir), name),
		)
	}

	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// resolveConfigPath 返回实际使用的配置文件路径（用于日志展示）。
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return findConfigFile()
}
