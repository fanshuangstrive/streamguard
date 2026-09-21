// StreamGuard 桌面版前端逻辑
//
// 通过 Wails 生成的绑定调用 Go 后端（window.go.main.App.*）。
// 每秒轮询状态与日志，保持界面实时。

import {
    GetConfig,
    SaveConfig,
    Start,
    Stop,
    GetStatus,
    GetLogs,
    ClearLogs,
    GetConfigPath,
    GetWorkPaths,
    Minimize,
    ToggleMaximize,
    Quit,
} from "../wailsjs/go/main/App.js";

// ---------- DOM 引用 ----------
const $ = (id) => document.getElementById(id);

const el = {
    topbar: $("topbar"),
    statusPill: $("statusPill"),
    statusText: $("statusText"),
    upstream: $("upstream"),
    preserveHost: $("preserveHost"),
    rate: $("rate"),
    burst: $("burst"),
    maxWait: $("maxWait"),
    timeout: $("timeout"),
    listen: $("listen"),
    logLevel: $("logLevel"),
    logFileMode: $("logFileMode"),
    logFile: $("logFile"),
    logRetainDays: $("logRetainDays"),
    breakerEnabled: $("breakerEnabled"),
    breakerThreshold: $("breakerThreshold"),
    breakerCooldown: $("breakerCooldown"),
    retryEnabled: $("retryEnabled"),
    retryMaxAttempts: $("retryMaxAttempts"),
    retryInitialWait: $("retryInitialWait"),
    retryMaxWait: $("retryMaxWait"),
    sizeLimitEnabled: $("sizeLimitEnabled"),
    sizeLimitThreshold: $("sizeLimitThreshold"),
    sizeLimitSmallConcurrent: $("sizeLimitSmallConcurrent"),
    sizeLimitLargeConcurrent: $("sizeLimitLargeConcurrent"),
    btnSave: $("btnSave"),
    btnReload: $("btnReload"),
    btnStart: $("btnStart"),
    btnStop: $("btnStop"),
    btnClearLogs: $("btnClearLogs"),
    btnMinimize: $("btnMinimize"),
    btnMaximize: $("btnMaximize"),
    btnClose: $("btnClose"),
    statTotal: $("statTotal"),
    statWaited: $("statWaited"),
    statRate: $("statRate"),
    statAddr: $("statAddr"),
    logList: $("logList"),
    configPath: $("configPath"),
    logPath: $("logPath"),
};

let lastLogCount = -1;

// ---------- 配置读写 ----------

// 从表单收集配置对象。
function collectConfig() {
    return {
        listen: el.listen.value.trim(),
        upstream: el.upstream.value.trim(),
        preserve_host: el.preserveHost.checked,
        rate: parseFloat(el.rate.value) || 1,
        burst: parseInt(el.burst.value, 10) || 1,
        max_wait: el.maxWait.value.trim() || "30s",
        timeout: el.timeout.value.trim() || "120s",
        log_level: el.logLevel.value,
        log_file: logFileModeToValue(),
        log_retain_days: parseInt(el.logRetainDays.value, 10) || 0,
        breaker_enabled: el.breakerEnabled.checked,
        breaker_threshold: parseInt(el.breakerThreshold.value, 10) || 5,
        breaker_cooldown: el.breakerCooldown.value.trim() || "30s",
        retry_enabled: el.retryEnabled.checked,
        retry_max_attempts: parseInt(el.retryMaxAttempts.value, 10) || 3,
        retry_initial_wait: el.retryInitialWait.value.trim() || "1s",
        retry_max_wait: el.retryMaxWait.value.trim() || "10s",
        size_limit_enabled: el.sizeLimitEnabled.checked,
        size_limit_threshold: parseInt(el.sizeLimitThreshold.value, 10) || 10000,
        size_limit_small_concurrent: parseInt(el.sizeLimitSmallConcurrent.value, 10) || 0,
        size_limit_large_concurrent: parseInt(el.sizeLimitLargeConcurrent.value, 10) || 0,
    };
}

// 将配置对象填充到表单。
function fillConfig(cfg) {
    if (!cfg) return;
    el.listen.value = cfg.listen || "";
    el.upstream.value = cfg.upstream || "";
    el.preserveHost.checked = !!cfg.preserve_host;
    el.rate.value = cfg.rate ?? 1;
    el.burst.value = cfg.burst ?? 1;
    el.maxWait.value = cfg.max_wait || "30s";
    el.timeout.value = cfg.timeout || "120s";
    el.logLevel.value = cfg.log_level || "info";
    applyLogFileValue(cfg.log_file || "");
    el.logRetainDays.value = cfg.log_retain_days ?? 7;
    el.breakerEnabled.checked = !!cfg.breaker_enabled;
    el.breakerThreshold.value = cfg.breaker_threshold ?? 5;
    el.breakerCooldown.value = cfg.breaker_cooldown || "30s";
    el.retryEnabled.checked = !!cfg.retry_enabled;
    el.retryMaxAttempts.value = cfg.retry_max_attempts ?? 3;
    el.retryInitialWait.value = cfg.retry_initial_wait || "1s";
    el.retryMaxWait.value = cfg.retry_max_wait || "10s";
    el.sizeLimitEnabled.checked = !!cfg.size_limit_enabled;
    el.sizeLimitThreshold.value = cfg.size_limit_threshold ?? 10000;
    el.sizeLimitSmallConcurrent.value = cfg.size_limit_small_concurrent ?? 0;
    el.sizeLimitLargeConcurrent.value = cfg.size_limit_large_concurrent ?? 0;
}

// 加载配置到表单。
async function loadConfig() {
    try {
        const cfg = await GetConfig();
        fillConfig(cfg);
    } catch (err) {
        console.error("加载配置失败", err);
    }
}

// ---------- 主开关与子参数联动 ----------

// 每个主开关控制其分组内子参数的可编辑性：
// 关闭时子参数区半透明且不可交互，开关文字显示「未启用」。
const toggleGroups = [
    { checkbox: el.sizeLimitEnabled, group: $("sizeLimitGroup") },
    { checkbox: el.breakerEnabled, group: $("breakerGroup") },
    { checkbox: el.retryEnabled, group: $("retryGroup") },
];

function syncToggleGroups() {
    for (const { checkbox, group } of toggleGroups) {
        const on = checkbox.checked;
        group.classList.toggle("off", !on);
        const text = group.querySelector(".switch-text");
        if (text) text.textContent = on ? "已启用" : "未启用";
    }
}

for (const { checkbox } of toggleGroups) {
    checkbox.addEventListener("change", syncToggleGroups);
}

// ---------- 日志落盘模式 ----------

// 将 log_file 配置值同步到「模式下拉 + 自定义输入」组合控件。
function applyLogFileValue(v) {
    if (!v) {
        el.logFileMode.value = "console";
    } else if (v === "auto") {
        el.logFileMode.value = "auto";
    } else {
        el.logFileMode.value = "custom";
    }
    el.logFile.value = v === "auto" ? "" : v || "";
    syncLogFileInput();
}

// 将界面选择转换回 log_file 配置值。
function logFileModeToValue() {
    switch (el.logFileMode.value) {
        case "auto": return "auto";
        case "custom": return el.logFile.value.trim();
        default: return "";
    }
}

// 自定义路径输入框仅在选中「自定义路径」时显示。
function syncLogFileInput() {
    el.logFile.style.display = el.logFileMode.value === "custom" ? "block" : "none";
}

el.logFileMode.addEventListener("change", syncLogFileInput);

// ---------- 状态刷新 ----------

// 刷新运行状态与统计。
async function refreshStatus() {
    try {
        const st = await GetStatus();

        el.statusPill.classList.toggle("on", st.running);
        el.statusText.textContent = st.running ? "运行中" : "未启动";

        el.statTotal.textContent = st.total ?? 0;
        el.statWaited.textContent = st.waited ?? 0;
        el.statRate.textContent = st.rate ? `${st.rate}/s` : "-";
        el.statAddr.textContent = st.running ? `http://${st.addr}` : "未启动";

        el.btnStart.disabled = st.running;
        el.btnStop.disabled = !st.running;
    } catch (err) {
        console.error("刷新状态失败", err);
    }
}

// 刷新日志列表。
async function refreshLogs() {
    try {
        const logs = await GetLogs();
        if (logs.length === lastLogCount) return;
        lastLogCount = logs.length;

        if (!logs.length) {
            el.logList.innerHTML = '<div class="log-empty">暂无日志</div>';
            return;
        }

        el.logList.innerHTML = logs
            .map(
                (l) =>
                    `<div class="log-line ${l.level}">` +
                    `<span class="log-time">${l.time}</span>` +
                    `<span class="log-msg">${escapeHtml(l.message)}</span>` +
                    `</div>`
            )
            .join("");
        el.logList.scrollTop = el.logList.scrollHeight;
    } catch (err) {
        console.error("刷新日志失败", err);
    }
}

// 转义 HTML，防止日志内容破坏结构。
function escapeHtml(s) {
    return String(s)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;");
}

// ---------- 事件绑定 ----------

el.btnSave.addEventListener("click", async () => {
    el.btnSave.disabled = true;
    try {
        await SaveConfig(collectConfig());
        await loadConfig();
        await refreshStatus();
        await refreshLogs();
    } catch (err) {
        alert("保存失败：" + err);
    } finally {
        el.btnSave.disabled = false;
    }
});

el.btnReload.addEventListener("click", async () => {
    await loadConfig();
    syncToggleGroups();
});

el.btnStart.addEventListener("click", async () => {
    el.btnStart.disabled = true;
    try {
        await Start();
    } catch (err) {
        alert("启动失败：" + err);
    }
    await refreshStatus();
    await refreshLogs();
});

el.btnStop.addEventListener("click", async () => {
    el.btnStop.disabled = true;
    try {
        await Stop();
    } catch (err) {
        alert("停止失败：" + err);
    }
    await refreshStatus();
    await refreshLogs();
});

el.btnClearLogs.addEventListener("click", async () => {
    await ClearLogs();
    lastLogCount = -1;
    await refreshLogs();
});

// ---------- 窗口控制 ----------

el.btnMinimize.addEventListener("click", () => Minimize());
el.btnMaximize.addEventListener("click", () => ToggleMaximize());
el.btnClose.addEventListener("click", () => Quit());

// 双击标题栏切换最大化（与原生窗口行为一致）。
el.topbar.addEventListener("dblclick", (e) => {
    if (e.target.closest(".window-controls")) return;
    ToggleMaximize();
});

// ---------- 初始化 ----------

async function init() {
    await loadConfig();
    syncToggleGroups();
    await refreshStatus();
    await refreshLogs();

    try {
        const p = await GetConfigPath();
        el.configPath.textContent = "配置文件：" + p;
    } catch (_) {
        /* 忽略 */
    }

    await refreshWorkPaths();

    // 每秒轮询
    setInterval(refreshStatus, 1000);
    setInterval(refreshLogs, 1000);
    setInterval(refreshWorkPaths, 5000);
}

// 刷新底部工作路径说明（配置文件 + 日志位置，方便查看或清理）。
async function refreshWorkPaths() {
    try {
        const p = await GetWorkPaths();
        el.configPath.textContent = "配置文件：" + (p.configPath || "-");
        el.logPath.textContent = p.logFile
            ? "详细日志：" + p.logFile
            : "详细日志：控制台（配置 log_file 或 auto 落盘）";
        el.logPath.title = p.logDir
            ? "日志目录：" + p.logDir + "（可直接打开查看或清理）"
            : "设置 log_level=debug 且 log_file=auto 后，日志写入当前目录 logs/";
    } catch (_) {
        /* 忽略 */
    }
}

init();
