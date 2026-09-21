// StreamGuard 桌面版前端逻辑（TypeScript）
//
// 通过 Wails 生成的绑定调用 Go 后端（window.go.main.App.*）。
// 每秒轮询状态与日志，保持界面实时。
//
// 模块划分：
//   - toast.ts        轻量非阻塞通知（支持「去修复」动作按钮）
//   - theme.ts        三态主题（跟随系统/浅色/深色）
//   - main.ts         视图切换、配置表单、状态轮询、日志渲染、快捷键

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
import { showToast } from "./toast.js";
import { initTheme } from "./theme.js";
import { initAbout } from "./about.js";

// ---------- DOM 引用 ----------

const $ = (id: string): HTMLElement => {
    const node = document.getElementById(id);
    if (!node) throw new Error(`缺少 DOM 元素 #${id}`);
    return node;
};

const el = {
    topbar: $("topbar"),
    statusPill: $("statusPill"),
    statusText: $("statusText"),
    upstream: $("upstream") as HTMLInputElement,
    preserveHost: $("preserveHost") as HTMLInputElement,
    rate: $("rate") as HTMLInputElement,
    burst: $("burst") as HTMLInputElement,
    maxWait: $("maxWait") as HTMLInputElement,
    timeout: $("timeout") as HTMLInputElement,
    listen: $("listen") as HTMLInputElement,
    logLevel: $("logLevel") as HTMLSelectElement,
    logFileMode: $("logFileMode") as HTMLSelectElement,
    logFile: $("logFile") as HTMLInputElement,
    logRetainDays: $("logRetainDays") as HTMLInputElement,
    breakerEnabled: $("breakerEnabled") as HTMLInputElement,
    breakerThreshold: $("breakerThreshold") as HTMLInputElement,
    breakerCooldown: $("breakerCooldown") as HTMLInputElement,
    retryEnabled: $("retryEnabled") as HTMLInputElement,
    retryMaxAttempts: $("retryMaxAttempts") as HTMLInputElement,
    retryInitialWait: $("retryInitialWait") as HTMLInputElement,
    retryMaxWait: $("retryMaxWait") as HTMLInputElement,
    sizeLimitEnabled: $("sizeLimitEnabled") as HTMLInputElement,
    sizeLimitThreshold: $("sizeLimitThreshold") as HTMLInputElement,
    sizeLimitSmallConcurrent: $("sizeLimitSmallConcurrent") as HTMLInputElement,
    sizeLimitLargeConcurrent: $("sizeLimitLargeConcurrent") as HTMLInputElement,
    btnSave: $("btnSave") as HTMLButtonElement,
    btnReload: $("btnReload") as HTMLButtonElement,
    btnBackMain: $("btnBackMain") as HTMLButtonElement,
    btnToggle: $("btnToggle") as HTMLButtonElement,
    btnSettings: $("btnSettings") as HTMLButtonElement,
    btnAbout: $("btnAbout") as HTMLButtonElement,
    btnCopyAddr: $("btnCopyAddr") as HTMLButtonElement,
    dirtyHint: $("dirtyHint"),
    btnClearLogs: $("btnClearLogs") as HTMLButtonElement,
    btnMinimize: $("btnMinimize") as HTMLButtonElement,
    btnMaximize: $("btnMaximize") as HTMLButtonElement,
    btnClose: $("btnClose") as HTMLButtonElement,
    viewMain: $("viewMain"),
    viewConfig: $("viewConfig"),
    statTotal: $("statTotal"),
    statWaited: $("statWaited"),
    statRate: $("statRate"),
    statAddr: $("statAddr"),
    statBreaker: $("statBreaker"),
    configSummary: $("configSummary"),
    sparkTotal: $("sparkTotal") as unknown as SVGSVGElement,
    emptyGuide: $("emptyGuide"),
    logFilter: $("logFilter") as HTMLSelectElement,
    logSearch: $("logSearch") as HTMLInputElement,
    logShowRequests: $("logShowRequests") as HTMLInputElement,
    logAutoScroll: $("logAutoScroll") as HTMLInputElement,
    logList: $("logList"),
    configPath: $("configPath"),
    logPath: $("logPath"),
};

let lastSavedConfig: string | null = null; // 最近一次保存/加载的配置快照，用于 dirty 检测
let latestLogs: LogEntryView[] = [];       // 后端最近一次返回的日志（渲染时按筛选条件过滤）
let logHovered = false;                    // 悬停冻结：鼠标在日志区时暂停重绘
let prevTotal = -1;                        // 上次累计请求数，用于 sparkline 增量采样
let uptimeBase = 0;                        // 代理开始运行的本地时间戳（0=未运行）

// DOMPurify 未引入（零依赖红线），确认弹窗使用原生 confirm。

// ---------- 视图切换（主视图 ⇄ 配置） ----------

function showView(name: "main" | "config"): void {
    el.viewMain.classList.toggle("hidden", name !== "main");
    el.viewConfig.classList.toggle("hidden", name !== "config");
}

function openConfigView(): void {
    showView("config");
    // 首次运行引导：上游为空时聚焦必填项
    if (!el.upstream.value.trim()) el.upstream.focus();
}

function tryLeaveConfig(): void {
    if (isDirty() && !confirm("有未保存的配置更改，离开将丢弃这些修改。确定离开？")) return;
    showView("main");
}

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
function fillConfig(cfg: Record<string, unknown> | null): void {
    if (!cfg) return;
    el.listen.value = (cfg.listen as string) || "";
    el.upstream.value = (cfg.upstream as string) || "";
    el.preserveHost.checked = !!cfg.preserve_host;
    el.rate.value = String(cfg.rate ?? 1);
    el.burst.value = String(cfg.burst ?? 1);
    el.maxWait.value = (cfg.max_wait as string) || "30s";
    el.timeout.value = (cfg.timeout as string) || "120s";
    el.logLevel.value = (cfg.log_level as string) || "info";
    applyLogFileValue((cfg.log_file as string) || "");
    el.logRetainDays.value = String(cfg.log_retain_days ?? 7);
    el.breakerEnabled.checked = !!cfg.breaker_enabled;
    el.breakerThreshold.value = String(cfg.breaker_threshold ?? 5);
    el.breakerCooldown.value = (cfg.breaker_cooldown as string) || "30s";
    el.retryEnabled.checked = !!cfg.retry_enabled;
    el.retryMaxAttempts.value = String(cfg.retry_max_attempts ?? 3);
    el.retryInitialWait.value = (cfg.retry_initial_wait as string) || "1s";
    el.retryMaxWait.value = (cfg.retry_max_wait as string) || "10s";
    el.sizeLimitEnabled.checked = !!cfg.size_limit_enabled;
    el.sizeLimitThreshold.value = String(cfg.size_limit_threshold ?? 10000);
    el.sizeLimitSmallConcurrent.value = String(cfg.size_limit_small_concurrent ?? 0);
    el.sizeLimitLargeConcurrent.value = String(cfg.size_limit_large_concurrent ?? 0);
}

// 加载配置到表单。
async function loadConfig(): Promise<void> {
    try {
        const cfg = await GetConfig();
        fillConfig(cfg as unknown as Record<string, unknown>);
        renderConfigSummary(cfg as unknown as Record<string, unknown>);
        lastSavedConfig = JSON.stringify(collectConfig());
        updateDirty();
    } catch (err) {
        console.error("加载配置失败", err);
    }
}

// ---------- 配置总览（主视图一句话说明当前生效配置） ----------

// 取字符串配置项，空/缺失时回退默认值。
function cfgStr(v: unknown, fallback: string): string {
    return typeof v === "string" && v ? v : fallback;
}

// 取数值配置项，非法时回退默认值。
function cfgNum(v: unknown, fallback: number): number {
    const n = Number(v);
    return Number.isFinite(n) ? n : fallback;
}

// 把当前生效配置组装成一句可读的整体说明。
// 反映的是已保存/生效的配置（非表单草稿），保存后 loadConfig 会刷新它。
function buildConfigSummary(cfg: Record<string, unknown>): string {
    const listen = cfgStr(cfg.listen, "127.0.0.1:8080");
    const upstream = cfgStr(cfg.upstream, "未配置上游");
    const rate = cfgNum(cfg.rate, 1);
    const burst = cfgNum(cfg.burst, 1);
    const maxWait = cfgStr(cfg.max_wait, "30s");
    const timeout = cfgStr(cfg.timeout, "120s");

    const on: string[] = [];
    const off: string[] = [];
    if (cfg.breaker_enabled) on.push(`熔断保护（连败 ${cfgNum(cfg.breaker_threshold, 5)} 次触发，冷却 ${cfgStr(cfg.breaker_cooldown, "30s")}）`);
    else off.push("熔断保护");
    if (cfg.retry_enabled) on.push(`上游重试（最多 ${cfgNum(cfg.retry_max_attempts, 3)} 次）`);
    else off.push("上游重试");
    if (cfg.size_limit_enabled) on.push(`大请求并发限制（阈值 ${cfgNum(cfg.size_limit_threshold, 10000)} token，小/大并发 ${cfgNum(cfg.size_limit_small_concurrent, 0)}/${cfgNum(cfg.size_limit_large_concurrent, 0)}）`);
    else off.push("大请求并发限制");

    let guard: string;
    if (on.length === 0) {
        guard = `${off.join("、")}均未启用`;
    } else {
        guard = on.join("、") + (off.length ? `；${off.join("、")}未启用` : "");
    }

    return `本机 ${listen} → ${upstream}；限流 ${rate} 次/秒（突发 ${burst}，排队超 ${maxWait} 返回 429），请求超时 ${timeout}；${guard}。`;
}

// 渲染配置总览到主视图。用 textContent，避免上游地址等文本破坏 DOM。
function renderConfigSummary(cfg: Record<string, unknown>): void {
    el.configSummary.textContent = buildConfigSummary(cfg);
}

// ---------- Dirty 跟踪 ----------

function isDirty(): boolean {
    return lastSavedConfig !== null && JSON.stringify(collectConfig()) !== lastSavedConfig;
}

// 表单与最近保存的配置不一致时，显示「未保存更改」提示。
function updateDirty(): void {
    el.dirtyHint.style.display = isDirty() ? "inline" : "none";
}

// 所有表单控件变化时刷新 dirty 状态。
for (const node of document.querySelectorAll<HTMLInputElement | HTMLSelectElement>("input, select")) {
    node.addEventListener("input", updateDirty);
    node.addEventListener("change", updateDirty);
}

// ---------- 字段级即时校验（失焦即提示，不等保存） ----------

const validators: Array<[HTMLElement, () => string]> = [
    [el.upstream, () =>
        el.upstream.value.trim() && !/^https?:\/\/.+/i.test(el.upstream.value.trim())
            ? "需以 http:// 或 https:// 开头" : ""],
    [el.listen, () => {
        const m = el.listen.value.trim().match(/^[\d.]+:(\d+)$/);
        if (!m) return "格式应为 地址:端口，如 127.0.0.1:8080";
        const port = Number(m[1]);
        return port > 0 && port < 65536 ? "" : "端口需在 1-65535 之间";
    }],
    [el.sizeLimitSmallConcurrent, () =>
        el.sizeLimitEnabled.checked
            && Number(el.sizeLimitSmallConcurrent.value) === 0
            && Number(el.sizeLimitLargeConcurrent.value) === 0
            ? "小/大请求并发不能同时为 0" : ""],
    [el.sizeLimitLargeConcurrent, () =>
        el.sizeLimitEnabled.checked
            && Number(el.sizeLimitSmallConcurrent.value) === 0
            && Number(el.sizeLimitLargeConcurrent.value) === 0
            ? "小/大请求并发不能同时为 0" : ""],
];

function showError(node: HTMLElement, msg: string): void {
    const field = node.closest(".field");
    if (!field) return;
    let err = field.querySelector<HTMLElement>(".field-error");
    if (msg) {
        if (!err) {
            err = document.createElement("small");
            err.className = "field-error";
            field.appendChild(err);
        }
        err.textContent = msg;
        err.hidden = false;
        node.classList.add("invalid");
    } else if (err) {
        err.textContent = "";
        err.hidden = true;
        node.classList.remove("invalid");
    }
}

for (const [node, check] of validators) {
    node.addEventListener("blur", () => showError(node, check()));
    node.addEventListener("input", () => {
        // 已报错的字段在输入时即时消除，未报错的不打断输入
        if (node.classList.contains("invalid")) showError(node, check());
    });
}

// ---------- 主开关与子参数联动 ----------

// 每个主开关控制其分组内子参数的可编辑性：
// 关闭时子参数区半透明且不可交互，开关文字显示「未启用」。
const toggleGroups: Array<{ checkbox: HTMLInputElement; group: HTMLElement }> = [
    { checkbox: el.sizeLimitEnabled, group: $("sizeLimitGroup") },
    { checkbox: el.breakerEnabled, group: $("breakerGroup") },
    { checkbox: el.retryEnabled, group: $("retryGroup") },
];

function syncToggleGroups(): void {
    for (const { checkbox, group } of toggleGroups) {
        const on = checkbox.checked;
        group.classList.toggle("off", !on);
        const text = group.querySelector<HTMLElement>(".switch-text");
        if (text) text.textContent = on ? "已启用" : "未启用";
    }
}

for (const { checkbox } of toggleGroups) {
    checkbox.addEventListener("change", syncToggleGroups);
}

// ---------- 日志落盘模式 ----------

// 将 log_file 配置值同步到「模式下拉 + 自定义输入」组合控件。
function applyLogFileValue(v: string): void {
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
function logFileModeToValue(): string {
    switch (el.logFileMode.value) {
        case "auto": return "auto";
        case "custom": return el.logFile.value.trim();
        default: return "";
    }
}

// 自定义路径输入框仅在选中「自定义路径」时显示。
function syncLogFileInput(): void {
    el.logFile.style.display = el.logFileMode.value === "custom" ? "block" : "none";
}

el.logFileMode.addEventListener("change", syncLogFileInput);

// ---------- 状态刷新 ----------

interface StatusView {
    running: boolean;
    addr: string;
    rate: number;
    total: number;
    waited: number;
    breakerEnabled?: boolean;
    breakerState?: string;
}

// 状态 pill 文案：运行中 · 2h13m（时长让用户确认"它一直在工作"）
function renderUptime(): void {
    if (!uptimeBase) return;
    const s = Math.floor((Date.now() - uptimeBase) / 1000);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    el.statusText.textContent = h > 0 ? `运行中 · ${h}h${m}m` : `运行中 · ${Math.max(m, 0)}m${String(s % 60).padStart(2, "0")}s`;
}

// 累计请求最近 60 次采样的速率曲线（纯 SVG，无依赖）。
function pushSpark(total: number): void {
    const delta = prevTotal < 0 ? 0 : Math.max(0, total - prevTotal);
    prevTotal = total;
    el.sparkTotal.dataset.n = String(delta);
    renderSpark();
}

function renderSpark(): void {
    const svg = el.sparkTotal;
    const n = Number(svg.dataset.n ?? 0);
    const arr = JSON.parse(svg.dataset.arr ?? "[]") as number[];
    arr.push(n);
    if (arr.length > 60) arr.shift();
    svg.dataset.arr = JSON.stringify(arr);
    if (arr.length < 2) { svg.innerHTML = ""; return; }
    const max = Math.max(...arr, 1);
    const pts = arr
        .map((v, i) => `${((i / (arr.length - 1)) * 100).toFixed(1)},${(24 - (v / max) * 22).toFixed(1)}`)
        .join(" ");
    svg.innerHTML = `<polygon points="0,24 ${pts} 100,24" class="spark-fill"/><polyline points="${pts}" class="spark-line"/>`;
}

const BREAKER_LABEL: Record<string, string> = {
    closed: "正常",
    open: "⚠ 已熔断",
    halfopen: "半开探测",
};

// 刷新运行状态与统计。
async function refreshStatus(): Promise<void> {
    try {
        const st = (await GetStatus()) as unknown as StatusView;

        // 起止沿检测：启动时记时长基准，停止时清零
        if (st.running && !uptimeBase) uptimeBase = Date.now();
        if (!st.running && uptimeBase) uptimeBase = 0;

        el.statusPill.classList.toggle("on", st.running);
        el.statusText.textContent = st.running ? "运行中" : "未启动";
        if (st.running) renderUptime();

        const total = st.total ?? 0;
        el.statTotal.textContent = String(total);
        el.statWaited.textContent = String(st.waited ?? 0);
        el.statRate.textContent = st.rate ? `${st.rate}/s` : "-";
        el.statAddr.textContent = st.running ? `http://${st.addr}` : "未启动";

        // 熔断卡片：未启用 / 状态（状态区分不只靠颜色，open 附 ⚠ 徽标）
        if (!st.breakerEnabled) {
            el.statBreaker.textContent = "未启用";
            el.statBreaker.className = "card-value card-value-sm";
        } else {
            const label = BREAKER_LABEL[st.breakerState ?? "closed"] ?? (st.breakerState || "-");
            el.statBreaker.textContent = label;
            el.statBreaker.className =
                "card-value card-value-sm" +
                (st.breakerState === "open" ? " breaker-open" : st.breakerState === "halfopen" ? " breaker-half" : " breaker-ok");
        }

        // 空状态引导：运行中但尚无请求 → 告诉用户下一步做什么
        el.emptyGuide.style.display = st.running && !total ? "inline" : "none";

        pushSpark(total);

        // 复制按钮仅在运行中显示
        el.btnCopyAddr.style.display = st.running ? "inline-flex" : "none";
        el.btnCopyAddr.dataset.addr = st.running ? `http://${st.addr}` : "";

        // 单按钮启停：状态语义由按钮自身承载
        const loading = el.btnToggle.classList.contains("loading");
        el.btnToggle.textContent = loading ? el.btnToggle.textContent : (st.running ? "■ 停止" : "▶ 启动");
        el.btnToggle.classList.toggle("success", !st.running);
        el.btnToggle.classList.toggle("danger", st.running);
        el.btnToggle.disabled = loading;
    } catch (err) {
        console.error("刷新状态失败", err);
    }
}

interface LogEntryView {
    time: string;
    level: string;
    source: string; // system（生命周期）/request（逐请求）
    message: string;
}

// ---------- 日志渲染（筛选 + hover 冻结 + 自动滚动） ----------

function filteredLogs(): LogEntryView[] {
    const level = el.logFilter.value;
    const q = el.logSearch.value.trim().toLowerCase();
    const showRequests = el.logShowRequests.checked;
    return latestLogs.filter((l) => {
        // 未勾选「显示请求日志」时，隐藏逐请求基础信息（source=request）。
        if (!showRequests && l.source === "request") return false;
        if (level !== "all" && l.level !== level) return false;
        if (q && !`${l.time} ${l.message}`.toLowerCase().includes(q)) return false;
        return true;
    });
}

function renderLogs(force = false): void {
    // hover 冻结：鼠标停在日志区时暂停重绘，移开后补渲染
    if (logHovered && !force) return;
    const logs = filteredLogs().slice(-500); // 渲染上限，防止大日志量卡顿
    if (!logs.length) {
        el.logList.innerHTML = `<div class="log-empty">${latestLogs.length ? "无匹配日志" : "暂无日志"}</div>`;
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
    if (el.logAutoScroll.checked) el.logList.scrollTop = el.logList.scrollHeight;
}

el.logList.addEventListener("mouseenter", () => { logHovered = true; });
el.logList.addEventListener("mouseleave", () => { logHovered = false; renderLogs(true); });
el.logFilter.addEventListener("change", () => renderLogs(true));
el.logSearch.addEventListener("input", () => renderLogs(true));
el.logShowRequests.addEventListener("change", () => renderLogs(true));

// 刷新日志列表。
async function refreshLogs(): Promise<void> {
    try {
        const logs = (await GetLogs()) as unknown as LogEntryView[];
        const last = logs.length ? logs[logs.length - 1] : null;
        const prev = latestLogs.length ? latestLogs[latestLogs.length - 1] : null;
        if (logs.length === latestLogs.length && last?.time === prev?.time && last?.message === prev?.message) return;
        latestLogs = logs;
        renderLogs();
    } catch (err) {
        console.error("刷新日志失败", err);
    }
}

// 转义 HTML，防止日志内容破坏结构。
function escapeHtml(s: unknown): string {
    return String(s)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;");
}

// ---------- 事件绑定 ----------

let saving = false; // 保存进行中标记，防重入

// 保存配置。状态写在同步/回调路径，避免 await 后写共享标记触发 require-atomic-updates。
function doSave(): Promise<void> {
    if (saving) return Promise.resolve();
    saving = true;
    el.btnSave.disabled = true;
    return SaveConfig(collectConfig())
        .then(loadConfig)
        .then(refreshStatus)
        .then(refreshLogs)
        .then(() => showToast("配置已保存", "success"))
        .catch((err) => showToast("保存失败：" + err, "error"))
        .finally(() => {
            saving = false;
            el.btnSave.disabled = false;
        });
}

el.btnSave.addEventListener("click", doSave);

el.btnReload.addEventListener("click", async () => {
    await loadConfig();
    syncToggleGroups();
    showToast("已重新加载配置");
});

// 启动/停止失败反馈：端口占用是最高频错误，toast 附「去修改端口」直达修复。
function notifyStartStopError(running: boolean, err: unknown): void {
    const msg = String(err);
    if (!running && /bind|占用|permission/i.test(msg)) {
        showToast("启动失败：" + msg, "error", {
            label: "去修改端口",
            onClick: () => {
                openConfigView();
                el.listen.focus();
            },
        });
        return;
    }
    showToast((running ? "停止失败：" : "启动失败：") + msg, "error");
}

// 单按钮启停：根据当前状态决定动作，期间显示 loading。
async function doToggle(): Promise<void> {
    if (el.btnToggle.classList.contains("loading")) return;
    const running = el.btnToggle.classList.contains("danger");
    el.btnToggle.classList.add("loading");
    el.btnToggle.textContent = running ? "停止中…" : "启动中…";
    try {
        if (running) {
            await Stop();
        } else {
            await Start();
        }
    } catch (err) {
        notifyStartStopError(running, err);
    }
    el.btnToggle.classList.remove("loading");
    await refreshStatus();
    await refreshLogs();
}

el.btnToggle.addEventListener("click", doToggle);

// 复制代理地址到剪贴板。
el.btnCopyAddr.addEventListener("click", async () => {
    const addr = el.btnCopyAddr.dataset.addr;
    if (!addr) return;
    try {
        await navigator.clipboard.writeText(addr);
        showToast("已复制：" + addr, "success");
    } catch {
        showToast("复制失败，请手动复制", "error");
    }
});

// ⚙ 切换到配置视图；返回按钮回主视图（dirty 时确认，绝不静默丢弃）。
el.btnSettings.addEventListener("click", openConfigView);
el.btnBackMain.addEventListener("click", tryLeaveConfig);

el.btnClearLogs.addEventListener("click", async () => {
    await ClearLogs();
    latestLogs = [];
    renderLogs(true);
});

// ---------- 快捷键（效率感的一半来自键盘） ----------

document.addEventListener("keydown", (e) => {
    if (!e.ctrlKey && !e.metaKey) return;
    const k = e.key.toLowerCase();
    if (k === "s") {
        e.preventDefault(); // 拦截浏览器默认保存页
        if (el.viewConfig.classList.contains("hidden")) openConfigView();
        doSave();
    } else if (k === "r") {
        e.preventDefault(); // 拦截浏览器默认刷新
        doToggle();
    }
});

// ---------- 窗口控制 ----------

el.btnMinimize.addEventListener("click", () => Minimize());
el.btnMaximize.addEventListener("click", () => ToggleMaximize());
el.btnClose.addEventListener("click", () => Quit());

// 双击标题栏切换最大化（与原生窗口行为一致）。
el.topbar.addEventListener("dblclick", (e) => {
    if ((e.target as HTMLElement).closest(".window-controls")) return;
    ToggleMaximize();
});

// ---------- 初始化 ----------

async function init(): Promise<void> {
    initTheme($("btnTheme") as HTMLButtonElement);
    initAbout(el.btnAbout);
    await loadConfig();
    syncToggleGroups();
    await refreshStatus();
    await refreshLogs();

    try {
        const p = await GetConfigPath();
        el.configPath.textContent = "配置文件：" + p;
    } catch {
        /* 忽略 */
    }

    await refreshWorkPaths();

    // 首次运行向导：上游未配置 → 直接进入配置视图并聚焦必填项
    if (!el.upstream.value.trim()) openConfigView();

    // 每秒轮询
    setInterval(refreshStatus, 1000);
    setInterval(refreshLogs, 1000);
    setInterval(refreshWorkPaths, 5000);
    setInterval(renderUptime, 1000);
}

// 刷新底部工作路径说明（配置文件 + 日志位置，方便查看或清理）。
async function refreshWorkPaths(): Promise<void> {
    try {
        const p = (await GetWorkPaths()) as unknown as Record<string, string>;
        el.configPath.textContent = "配置文件：" + (p.configPath || "-");
        el.logPath.textContent = p.logFile
            ? "详细日志：" + p.logFile
            : "详细日志：控制台（配置 log_file 或 auto 落盘）";
        el.logPath.title = p.logDir
            ? "日志目录：" + p.logDir + "（可直接打开查看或清理）"
            : "设置 log_level=debug 且 log_file=auto 后，日志写入当前目录 logs/";
    } catch {
        /* 忽略 */
    }
}

init();
