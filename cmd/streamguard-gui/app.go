// Package main 是 StreamGuard 桌面版（Wails）的入口。
//
// 本文件实现 GUI 与核心层（internal/app）之间的绑定层：
// 前端通过 Wails 调用这里的方法，这里再委托给 app.App。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/streamguard/streamguard/internal/app"
	"github.com/streamguard/streamguard/internal/config"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// LogEntry 是一条日志记录，供前端展示。
type LogEntry struct {
	Time    string `json:"time"`    // 时间 HH:MM:SS
	Level   string `json:"level"`   // 级别 info/warn/error
	Message string `json:"message"` // 内容
}

// Status 是返回给前端的运行状态快照。
type Status struct {
	Running              bool    `json:"running"`              // 是否运行中
	Addr                 string  `json:"addr"`                 // 实际监听地址
	Upstream             string  `json:"upstream"`             // 上游地址
	Rate                 float64 `json:"rate"`                 // 限流速率
	Burst                int     `json:"burst"`                // 突发容量
	Total                int64   `json:"total"`                // 累计请求数
	Waited               int64   `json:"waited"`               // 累计等待数
	TimedOut             int64   `json:"timedOut"`             // 等待超时（429）数
	Canceled             int64   `json:"canceled"`             // 等待期间取消数
	AvgWaitMs            int64   `json:"avgWaitMs"`            // 平均等待耗时（毫秒）
	MaxWaitMs            int64   `json:"maxWaitMs"`            // 单次最大等待耗时（毫秒）
	BreakerEnabled       bool    `json:"breakerEnabled"`       // 熔断器是否启用
	BreakerState         string  `json:"breakerState"`         // 熔断器状态
	BreakerTrips         int64   `json:"breakerTrips"`         // 累计熔断次数
	BreakerRejected      int64   `json:"breakerRejected"`      // 累计快速拒绝数
	RetryEnabled         bool    `json:"retryEnabled"`         // 上游限流重试是否启用
	SizeLimitEnabled     bool    `json:"sizeLimitEnabled"`     // 大小限流是否启用
	SizeLimitSmallActive int     `json:"sizeLimitSmallActive"` // 小请求当前在途数
	SizeLimitLargeActive int     `json:"sizeLimitLargeActive"` // 大请求当前在途数
	SizeLimitWaiting     int     `json:"sizeLimitWaiting"`     // 当前排队等待数
	SizeLimitTimedOut    int64   `json:"sizeLimitTimedOut"`    // 大小限流等待超时数
	ConfigPath           string  `json:"configPath"`           // 配置文件路径
}

// App 是暴露给前端的绑定对象。
type App struct {
	ctx context.Context

	mu      sync.Mutex
	core    *app.App
	logs    []LogEntry
	cfgPath string

	// explicitConfig 是命令行 -config 指定的配置文件路径，为空时自动查找。
	explicitConfig string
}

// NewApp 创建绑定对象。
//
// explicitConfig 为命令行 -config 指定的路径，为空时按优先级自动查找。
func NewApp(explicitConfig string) *App {
	return &App{
		logs:           make([]LogEntry, 0, 256),
		explicitConfig: explicitConfig,
	}
}

// startup 在 Wails 启动时调用。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 按优先级查找配置文件：
	//  1. 命令行 -config 指定的路径
	//  2. 当前工作目录 / exe 目录 / exe 上级目录下的 config.local.json
	//  3. 用户配置目录 %AppData%\StreamGuard\config.json（不存在则创建默认配置）
	a.cfgPath = resolveConfigPath(a.explicitConfig)

	cfg, err := a.loadOrCreateConfig()
	if err != nil {
		a.addLog("error", fmt.Sprintf("failed to load config: %v", err))
		cfg = config.Default()
	}

	a.mu.Lock()
	a.core = app.New(cfg)
	// 详细日志（请求/响应内容）由 core 按 log_file 配置决定输出目标：
	//   - log_file 为空 → 控制台（GUI 无控制台时被系统丢弃）
	//   - log_file 非空 → 写入文件（推荐 GUI 场景使用）
	// 无论哪种方式，都不会进入界面日志面板，避免刷屏。
	a.mu.Unlock()

	a.addLog("info", fmt.Sprintf("config file: %s", a.cfgPath))
	a.addLog("info", "StreamGuard ready")
}

// resolveConfigPath 按优先级确定配置文件路径。
//
// 优先使用仓库/程序目录下的 config.local.json（与 CLI 行为一致），
// 找不到时回退到用户配置目录，避免污染程序目录。
func resolveConfigPath(explicit string) string {
	if explicit != "" {
		return explicit
	}

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

	// 回退：用户配置目录
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "StreamGuard", "config.json")
}

// loadOrCreateConfig 读取配置；不存在时写入默认配置。
func (a *App) loadOrCreateConfig() (*config.Config, error) {
	if _, err := os.Stat(a.cfgPath); os.IsNotExist(err) {
		cfg := config.Default()
		if err := a.writeConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		return nil, err
	}
	cfg.ApplyEnv()
	cfg.Normalize()
	return cfg, nil
}

// writeConfig 将配置写入磁盘。
func (a *App) writeConfig(cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(a.cfgPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.cfgPath, data, 0o600)
}

// addLog 追加一条日志（保留最近 500 条）。
func (a *App) addLog(level, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.logs = append(a.logs, LogEntry{
		Time:    time.Now().Format("15:04:05"),
		Level:   level,
		Message: msg,
	})
	if len(a.logs) > 500 {
		a.logs = a.logs[len(a.logs)-500:]
	}
}

// ---------- 以下方法暴露给前端 ----------

// GetConfig 返回当前配置。
func (a *App) GetConfig() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.core == nil {
		return config.Default()
	}
	return a.core.Config()
}

// SaveConfig 保存配置到磁盘，并在运行中时重启服务。
func (a *App) SaveConfig(cfg *config.Config) error {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		a.addLog("error", fmt.Sprintf("invalid config: %v", err))
		return err
	}
	if err := a.writeConfig(cfg); err != nil {
		a.addLog("error", fmt.Sprintf("failed to save config: %v", err))
		return err
	}

	a.mu.Lock()
	core := a.core
	a.mu.Unlock()

	if core != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := core.UpdateConfig(ctx, cfg); err != nil {
			a.addLog("error", fmt.Sprintf("failed to apply config: %v", err))
			return err
		}
	}
	a.addLog("info", "config saved")
	return nil
}

// Start 启动代理服务。
func (a *App) Start() error {
	a.mu.Lock()
	core := a.core
	a.mu.Unlock()

	if core == nil {
		return fmt.Errorf("app not initialized")
	}
	if err := core.Start(); err != nil {
		a.addLog("error", fmt.Sprintf("failed to start: %v", err))
		return err
	}
	a.addLog("info", fmt.Sprintf("server started, listening on %s", core.Addr()))
	return nil
}

// Stop 停止代理服务。
func (a *App) Stop() error {
	a.mu.Lock()
	core := a.core
	a.mu.Unlock()

	if core == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := core.Stop(ctx); err != nil {
		a.addLog("error", fmt.Sprintf("failed to stop: %v", err))
		return err
	}
	a.addLog("info", "server stopped")
	return nil
}

// Restart 重启代理服务。
func (a *App) Restart() error {
	if err := a.Stop(); err != nil {
		return err
	}
	return a.Start()
}

// GetStatus 返回运行状态与统计。
func (a *App) GetStatus() Status {
	a.mu.Lock()
	core := a.core
	cfgPath := a.cfgPath
	a.mu.Unlock()

	st := Status{ConfigPath: cfgPath}
	if core == nil {
		return st
	}
	s := core.Stats()
	st.Running = s.Running
	st.Addr = s.Addr
	st.Upstream = s.Upstream
	st.Rate = s.Rate
	st.Burst = s.Burst
	st.Total = s.Total
	st.Waited = s.Waited
	st.TimedOut = s.TimedOut
	st.Canceled = s.Canceled
	st.AvgWaitMs = s.AvgWaitMs
	st.MaxWaitMs = s.MaxWaitMs
	st.BreakerEnabled = s.BreakerEnabled
	st.RetryEnabled = s.RetryEnabled
	st.BreakerState = s.BreakerState
	st.BreakerTrips = s.BreakerTrips
	st.BreakerRejected = s.BreakerRejected
	st.SizeLimitEnabled = s.SizeLimitEnabled
	st.SizeLimitSmallActive = s.SizeLimitSmallActive
	st.SizeLimitLargeActive = s.SizeLimitLargeActive
	st.SizeLimitWaiting = s.SizeLimitWaiting
	st.SizeLimitTimedOut = s.SizeLimitTimedOut
	return st
}

// GetLogs 返回日志列表。
func (a *App) GetLogs() []LogEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]LogEntry, len(a.logs))
	copy(out, a.logs)
	return out
}

// ClearLogs 清空日志。
func (a *App) ClearLogs() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = a.logs[:0]
}

// GetConfigPath 返回配置文件路径。
func (a *App) GetConfigPath() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfgPath
}

// GetWorkPaths 返回工作路径信息，供界面展示，方便用户查看或清理。
//
// 返回：配置文件路径、详细日志文件路径（空表示输出到控制台）、日志目录。
func (a *App) GetWorkPaths() map[string]string {
	a.mu.Lock()
	cfgPath := a.cfgPath
	core := a.core
	a.mu.Unlock()

	logPath := ""
	if core != nil {
		logPath = core.VerboseLogPath()
	}
	logDir := ""
	if logPath != "" {
		logDir = filepath.Dir(logPath)
	}
	return map[string]string{
		"configPath": cfgPath,
		"logFile":    logPath,
		"logDir":     logDir,
	}
}

// ---------- 窗口控制 ----------

// Minimize 最小化窗口。
func (a *App) Minimize() {
	if a.ctx == nil {
		return
	}
	runtime.WindowMinimise(a.ctx)
}

// ToggleMaximize 在最大化与还原之间切换。
func (a *App) ToggleMaximize() {
	if a.ctx == nil {
		return
	}
	if runtime.WindowIsMaximised(a.ctx) {
		runtime.WindowUnmaximise(a.ctx)
		return
	}
	runtime.WindowMaximise(a.ctx)
}

// Quit 退出应用（关闭窗口）。
func (a *App) Quit() {
	if a.ctx == nil {
		os.Exit(0)
		return
	}
	runtime.Quit(a.ctx)
}
