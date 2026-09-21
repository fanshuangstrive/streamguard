# 配置说明

StreamGuard 使用 JSON 配置文件，支持环境变量覆盖。**GUI 与 CLI 共用同一份配置**。

## 配置方式对比

| 方式 | 适用 | 说明 |
|---|---|---|
| **GUI 界面** | 桌面用户 | 图形化编辑，点击「保存并重启」生效 |
| **配置文件** | 所有用户 | 直接编辑 JSON，重启生效 |
| **环境变量** | 脚本/容器 | 优先级最高，覆盖配置文件 |

三种方式最终都落到同一份配置文件（GUI 保存时写入）。

## 配置文件查找规则

**GUI 与 CLI 使用相同的查找逻辑**，按以下优先级确定配置文件（找到第一个即用）：

| 优先级 | 位置 | 说明 |
|---|---|---|
| 1 | 命令行 `-config <path>` | 显式指定，最高优先级 |
| 2 | 当前工作目录 `config.local.json` | 在仓库根目录启动时命中 |
| 3 | exe 所在目录 `config.local.json` | 双击 exe 启动时命中 |
| 4 | exe 上级目录 `config.local.json` | 适配 `dist/` 布局 |
| 5 | `%AppData%\StreamGuard\config.json` | 回退：用户配置目录（不存在则创建默认配置） |

> **GUI 与 CLI 行为一致**：只要仓库根目录存在 `config.local.json`，两者都会加载它。
> 仅当以上位置都找不到时，GUI 才回退到 `%AppData%\StreamGuard\config.json`。

## 配置文件约定

| 文件 | 是否提交 git | 用途 |
|---|---|---|
| `config.example.json` | ✅ 提交 | 配置模板，占位符形式，供他人参考 |
| `config.local.json` | ❌ **禁止提交** | 本地真实配置，含真实上游地址 |

> `config.local.json` 已在 `.gitignore` 中排除。**切勿将真实上游地址提交到仓库。**

## 配置项

| 字段 | 类型 | 默认值 | GUI 界面标签 | 说明 |
|---|---|---|---|---|
| `listen` | string | `127.0.0.1:8080` | 监听地址 | 本地监听地址，默认仅回环，不暴露局域网 |
| `upstream` | string | `http://127.0.0.1:11434` | 上游地址 | 上游模型服务基础地址，请求路径原样拼接后转发 |
| `preserve_host` | bool | `false` | 保留原始 Host 头 | 为 `true` 时保留客户端原始 `Host`，不改写为上游主机名 |
| `rate` | number | `1` | 限流速率 | 等待式限流速率（QPS），如 `1` 表示 1 秒 1 次 |
| `burst` | number | `1` | 突发容量 | 突发容量，允许瞬时通过的请求数 |
| `max_wait` | string | `30s` | 最大等待 | 单次请求最大等待时长，超时返回 429 |
| `timeout` | string | `120s` | 请求超时 | 非 SSE 请求的整体超时；SSE 流式请求自动豁免 |
| `log_level` | string | `info` | 日志级别 | 日志级别：debug / info / warn / error。设为 `debug` 时打印完整请求/响应内容 |
| `log_file` | string | `""` | 日志文件 | 详细日志落盘路径。为空时输出到控制台；设为 `auto` 时自动写入 `<当前目录>/logs/streamguard-YYYYMMDD.log`。相对路径基于命令当前目录解析。仅在 `log_level=debug` 时生效 |
| `log_retain_days` | number | `7` | 日志保留天数 | 启动时清理 `logs/` 下超过该天数的 `streamguard-YYYYMMDD.log`。设为 `0` 关闭清理 |
| `breaker_enabled` | bool | `false` | 启用熔断 | 为 `true` 时，上游连续失败达到阈值后快速失败（返回 503），冷却后自动半开探测 |
| `breaker_threshold` | number | `5` | 熔断阈值 | 连续失败次数达到该值即熔断 |
| `breaker_cooldown` | string | `30s` | 熔断冷却 | 熔断后进入半开探测前的冷却时长 |
| `retry_enabled` | bool | `false` | 启用上游重试 | 为 `true` 时，上游返回 429/503 会等待后自动重试 |
| `retry_max_attempts` | number | `3` | 最大尝试次数 | 含首次请求，默认 3 次 |
| `retry_initial_wait` | string | `1s` | 首次重试等待 | 指数退避起点：1s → 2s → 4s… |
| `retry_max_wait` | string | `10s` | 单次等待上限 | 退避与 `Retry-After` 均受此上限约束 |
| `size_limit_enabled` | bool | `false` | 启用按请求大小限制并发 | 为 `true` 时，按估算 token 数分档限制并发，避免大请求长时间占用上游连接 |
| `size_limit_threshold` | number | `10000` | 大小阈值（token） | 估算 token ≤ 阈值视为小请求，> 阈值视为大请求 |
| `size_limit_small_concurrent` | number | `0` | 小请求最大并发 | 小请求并发上限，`0` 表示不限制 |
| `size_limit_large_concurrent` | number | `0` | 大请求最大并发 | 大请求并发上限，`0` 表示不限制 |

### 路径转发规则

StreamGuard 不做路径改写：**客户端请求的路径会原样拼接到 `upstream` 后转发**。

```
客户端请求  http://127.0.0.1:8080/model/v1/chat/completions
上游地址    https://modelapi.example.com
实际转发    https://modelapi.example.com/model/v1/chat/completions
```

因此客户端只需把 base_url 指向本地代理，其余路径保持不变即可。

### 请求头透传

StreamGuard 是**透明代理**：客户端请求头会被**原样转发**到上游，不做任何修改或丢弃。

| 请求头 | 行为 |
|---|---|
| `Authorization` | 原样透传（客户端自行携带凭据） |
| `Content-Type` / `Accept` | 原样透传 |
| 自定义业务头（如 `X-Request-Id`） | 原样透传 |
| `Host` | 默认改写为上游主机名；`preserve_host=true` 时保留客户端原始值 |
| `X-Forwarded-For` | 自动追加客户端 IP，便于上游排查 |
| `Connection` / `Keep-Alive` 等逐跳头 | 按 HTTP 规范移除（不转发） |

> 上游响应头（含 `Content-Type`、自定义头）同样原样返回给客户端。

#### 关于 `Host` 头

`Host` 是唯一会被代理改写的请求头，因为很多上游网关（nginx、Kong 等）**按 `Host` 做虚拟主机路由**：

| 配置 | 转发时的 `Host` | 适用场景 |
|---|---|---|
| `preserve_host: false`（默认） | 上游主机名（如 `api.example.com`） | **大多数情况**，上游按域名路由 |
| `preserve_host: true` | 客户端原始 `Host`（如 `127.0.0.1:8080`） | 上游要求保留原始 Host 的特殊场景 |

> ⚠️ **若上游返回 404 而直连正常，通常是 `Host` 不匹配**。
> 排查方法：直连上游并手动设置 `Host: 127.0.0.1:8080`，若返回 404 则说明上游按 `Host` 路由，
> 此时应保持 `preserve_host: false`。

### 协议适配

代理对 SSE 流式响应与普通 JSON 响应**自动适配**，无需配置：

| 响应类型 | 行为 |
|---|---|
| `text/event-stream`（SSE） | 关闭缓冲，逐块实时透传，不等待响应结束 |
| `application/json`（普通） | 正常缓冲转发 |

### 详细日志（调试请求/响应内容）

将 `log_level` 设为 `debug`，即可打印完整的请求与响应内容：

```bash
# 方式一：环境变量（临时开启，无需改配置）
$env:STREAMGUARD_LOG_LEVEL="debug"; .\streamguard.exe -config config.local.json

# 方式二：修改 config.local.json 中的 log_level 为 "debug"
```

#### 输出位置：控制台 vs 日志文件

由 `log_file` 决定：

| `log_file` | 输出位置 | 适用场景 |
|---|---|---|
| `""`（默认） | 控制台（标准输出） | CLI 前台运行，实时观察 |
| `"auto"` | `<当前目录>/logs/streamguard-YYYYMMDD.log` | **GUI 场景推荐**，按天分文件 |
| 相对路径（如 `"my.log"`） | `<当前目录>/logs/my.log` | 固定文件名，便于持续追加 |
| 绝对路径 | 指定文件（追加写入） | 需要固定位置归档 |

> **路径解析规则**：`auto` 与相对路径都基于**命令当前目录**（`os.Getwd()`）解析，
> 统一挂到当前目录下的 `logs/` 子目录；绝对路径原样使用。
> 启动日志会打印实际使用的绝对路径，便于确认。
>
> ⚠️ **GUI 双击启动时没有控制台**，`log_file` 为空会导致详细日志被系统丢弃。
> GUI 用户请设置 `log_file: "auto"`。

```json
{
  "log_level": "debug",
  "log_file": "auto"
}
```

开启后日志内容如下（控制台或文件）：

```
[req] >>> POST /v1/chat/completions HTTP/1.1
[req] Host: 127.0.0.1:8080
[req] Authorization: Bearer ***
[req] Content-Type: application/json
[req] body (58 bytes): {"model":"...","stream":true}
[resp] <<< 200 OK
[resp] Content-Type: text/event-stream
[resp] chunk#1 (32 bytes): data: {"choices":[...]}
[resp] chunk#2 (32 bytes): data: {"choices":[...]}
[resp] chunk#3 (14 bytes): data: [DONE]
[resp] <<< done: status=200 chunks=3 bytes=78
[proxy] POST /v1/chat/completions client=127.0.0.1 elapsed=1.2s
```

| 前缀 | 含义 |
|---|---|
| `[req] >>>` | 请求行（方法 / 路径 / 协议） |
| `[req] <Header>: <值>` | 请求头逐条打印 |
| `[req] body (N bytes)` | 请求体内容 |
| `[resp] <<< <状态码>` | 响应状态行 |
| `[resp] <Header>: <值>` | 响应头逐条打印 |
| `[resp] chunk#N` | 响应体数据块（SSE 逐块实时打印） |
| `[resp] <<< done` | 响应汇总（状态码 / 块数 / 字节数） |

> ⚠️ **详细日志会包含 `Authorization` 等敏感信息，仅用于本地调试，切勿在生产环境开启。**
>
> 无论输出到控制台还是文件，详细日志都**不会进入 GUI 界面日志面板**（避免刷屏）。
> 日志文件路径非法时会自动回退到控制台，不影响服务启动。

#### 是否热更新？

| 修改方式 | 生效方式 |
|---|---|
| GUI 界面改 `log_level` / `log_file` → 点「保存并重启」 | ✅ **无需重启程序**，服务自动重建（中断约几十毫秒） |
| 直接编辑 `config.local.json` | ❌ 需重启进程 |
| 环境变量 `STREAMGUARD_LOG_LEVEL` / `STREAMGUARD_LOG_FILE` | ❌ 需重启进程 |

> 原理：GUI 的「保存并重启」会调用 `UpdateConfig` → `Stop()` + `Start()`，
> 重建 HTTP 服务，因此 `log_level`、`log_file` 等所有配置项都会按新值生效。
> 重启时会先关闭旧的日志文件句柄，避免句柄泄漏。

### 日志保留清理（`log_retain_days`）

服务启动时会扫描 `<当前目录>/logs/`，删除**文件名日期**早于保留天数的日志文件：

| 配置值 | 行为 |
|---|---|
| `7`（默认） | 保留最近 7 天，更早的 `streamguard-YYYYMMDD.log` 被删除 |
| `0` | 关闭清理，日志永久保留 |

> 只删除形如 `streamguard-YYYYMMDD.log` 的文件，**不会**误删目录中的其他文件。
> 日期取自文件名而非文件修改时间，避免复制/同步导致 mtime 失真。

### 上游熔断（`breaker_*`）

当上游持续故障时，继续转发只会让请求堆积、拖慢客户端。熔断器提供**快速失败**保护：

```
closed ──连续失败达阈值──> open ──冷却结束──> half-open
  ↑                                              │
  └──────────── 探测成功 ────────────────────────┘
                探测失败 ──> open（重新计时）
```

| 状态 | 行为 |
|---|---|
| `closed` | 正常转发（默认状态） |
| `open` | **直接返回 503**，不发起上游请求，响应头带 `Retry-After: 5` |
| `half-open` | 只放行**一个**探测请求；成功则闭合，失败则重新打开 |

**失败判定**：上游连接失败（`ErrorHandler`）或返回 5xx（`ModifyResponse`）。

**默认关闭**：`breaker_enabled` 默认 `false`，不改变既有行为。开启示例：

```json
{
  "breaker_enabled": true,
  "breaker_threshold": 5,
  "breaker_cooldown": "30s"
}
```

熔断状态可通过 `/stats` 的 `breaker_state` / `breaker_trips` / `breaker_rejected` 观察。

### 上游限流重试（`retry_*`）

上游（如网关）自身限流返回 429 时，客户端只能自己重试。开启重试后，代理会**自动等待并重试**，客户端无感知：

```
上游返回 429/503
  → 读取 Retry-After 头（若有，优先使用）
  → 否则指数退避：1s → 2s → 4s…（受 retry_max_wait 约束）
  → 等待后重试，最多 retry_max_attempts 次
  → 仍失败则透传最后一次响应
```

| 配置 | 说明 |
|---|---|
| `retry_enabled` | 总开关，默认 `false` |
| `retry_max_attempts` | 最大尝试次数（含首次），默认 `3` |
| `retry_initial_wait` | 指数退避起点，默认 `1s` |
| `retry_max_wait` | 单次等待上限，默认 `10s` |

**默认重试的状态码**：`429`（Too Many Requests）、`503`（Service Unavailable）。

**关键约束**：

- **只在响应头阶段重试**：一旦响应体开始传输（如 SSE 流已开始）就不再重试，避免破坏流式输出
- **请求体自动重放**：重试时请求体会被完整重放，不会丢失
- **尊重 `Retry-After`**：上游明确告知等待时长时优先使用（支持秒数与 HTTP 日期两种格式）
- **与熔断器联动**：重试耗尽仍失败时，会计入熔断失败计数

> ⚠️ **注意**：重试可能对**非幂等请求**（如 POST 创建类接口）造成重复执行。
> 默认只对 429/503 重试（上游明确未处理请求），但仍建议仅在确认上游语义安全时开启。

开启示例：

```json
{
  "retry_enabled": true,
  "retry_max_attempts": 3,
  "retry_initial_wait": "1s",
  "retry_max_wait": "10s"
}
```

### 按请求大小限制并发（`size_limit_*`）

大请求（长上下文）会长时间占用上游连接，容易拖垮上游。开启后，代理会**按请求的估算 token 数分档限制并发**：

```
请求到达
  → 估算输入 token 数（见下方「token 估算」）
  → token ≤ size_limit_threshold → 小请求档，上限 size_limit_small_concurrent
  → token >  size_limit_threshold → 大请求档，上限 size_limit_large_concurrent
  → 该档位有空闲槽位则立即通过，否则排队等待（受 max_wait 约束）
  → 请求结束（含 SSE 流结束）后释放槽位
```

| 配置 | 说明 |
|---|---|
| `size_limit_enabled` | 总开关，默认 `false` |
| `size_limit_threshold` | 大小请求分界（估算 token），默认 `10000` |
| `size_limit_small_concurrent` | 小请求并发上限，默认 `0`（不限制） |
| `size_limit_large_concurrent` | 大请求并发上限，默认 `0`（不限制） |

**典型场景**：小请求（≤ 10000 token）限 2 个并发，大请求限 1 个并发：

```json
{
  "size_limit_enabled": true,
  "size_limit_threshold": 10000,
  "size_limit_small_concurrent": 2,
  "size_limit_large_concurrent": 1
}
```

**关键约束**：

- **与速率限流串联**：先过 `rate`/`burst` 速率限流，再过大小并发限流，两者独立生效
- **共享等待预算**：两者共用同一个 `max_wait` deadline，总等待时长不会叠加
- **排队等速率的请求不占并发槽位**：槽位在速率限流通过后才获取
- **SSE 流全程持有槽位**：流式响应结束前不释放，避免大流并发挤占上游
- **默认关闭**：`size_limit_enabled: false` 时行为与之前完全一致

#### token 估算

精确 token 数需要引入 tokenizer（如 tiktoken），会破坏本项目「运行时零第三方依赖」的约束。因此采用**保守的启发式估算**：

- 优先解析 OpenAI 兼容的 `messages` 数组，累加各条 `content` 的字节数（支持多模态数组，只统计 `text` 部分）
- 解析失败或结构不符时，退化为对整个请求体做字节数估算
- 估算公式：`token ≈ 字节数 / 3`（向上取整）

> ℹ️ **为什么除以 3**：英文约 4 字节/token，中文约 3 字节/token（UTF-8）。取 3 对英文偏保守（高估），对中文接近准确。
> **高估是安全方向**：宁可把请求判为大请求（更严格限流），也不要低估导致大请求挤占上游。

> ⚠️ **注意**：估算值可能与上游实际计费 token 有偏差。可通过 `/stats` 的 `size_limit_*` 字段观察实际在途数与排队数，据此校准阈值。

## 环境变量覆盖

所有配置项均可用 `STREAMGUARD_` 前缀的环境变量覆盖，优先级高于配置文件：

| 环境变量 | 对应字段 |
|---|---|
| `STREAMGUARD_LISTEN` | `listen` |
| `STREAMGUARD_UPSTREAM` | `upstream` |
| `STREAMGUARD_PRESERVE_HOST` | `preserve_host` |
| `STREAMGUARD_RATE` | `rate` |
| `STREAMGUARD_BURST` | `burst` |
| `STREAMGUARD_MAX_WAIT` | `max_wait` |
| `STREAMGUARD_TIMEOUT` | `timeout` |
| `STREAMGUARD_LOG_LEVEL` | `log_level` |
| `STREAMGUARD_LOG_FILE` | `log_file` |
| `STREAMGUARD_LOG_RETAIN_DAYS` | `log_retain_days` |
| `STREAMGUARD_BREAKER_ENABLED` | `breaker_enabled` |
| `STREAMGUARD_BREAKER_THRESHOLD` | `breaker_threshold` |
| `STREAMGUARD_BREAKER_COOLDOWN` | `breaker_cooldown` |
| `STREAMGUARD_RETRY_ENABLED` | `retry_enabled` |
| `STREAMGUARD_RETRY_MAX_ATTEMPTS` | `retry_max_attempts` |
| `STREAMGUARD_RETRY_INITIAL_WAIT` | `retry_initial_wait` |
| `STREAMGUARD_RETRY_MAX_WAIT` | `retry_max_wait` |
| `STREAMGUARD_SIZE_LIMIT_ENABLED` | `size_limit_enabled` |
| `STREAMGUARD_SIZE_LIMIT_THRESHOLD` | `size_limit_threshold` |
| `STREAMGUARD_SIZE_LIMIT_SMALL_CONCURRENT` | `size_limit_small_concurrent` |
| `STREAMGUARD_SIZE_LIMIT_LARGE_CONCURRENT` | `size_limit_large_concurrent` |

## 当前开发环境配置

本项目当前对接的上游为内部模型网关（真实地址见本地 `config.local.json`，不提交 git）：

```json
{
  "listen": "127.0.0.1:8080",
  "upstream": "https://<your-upstream-host>/<base-path>",
  "preserve_host": false,
  "rate": 1,
  "burst": 1,
  "max_wait": "30s",
  "timeout": "120s",
  "log_level": "info",
  "log_retain_days": 7,
  "breaker_enabled": false,
  "breaker_threshold": 5,
  "breaker_cooldown": "30s",
  "retry_enabled": false,
  "retry_max_attempts": 3,
  "retry_initial_wait": "1s",
  "retry_max_wait": "10s"
}
```

- 上游完整地址：`<upstream>/<path>`（路径原样转发）
- 可用模型：详见 [upstream-models.md](./upstream-models.md)
- 限流策略：**1 秒 1 次**（`rate=1, burst=1`），超限请求排队等待，最长等 30 秒

## 使用方式

### GUI 模式（桌面）

```bash
streamguard-gui.exe

# 或显式指定配置文件
streamguard-gui.exe -config config.local.json
```

1. 启动后按[配置文件查找规则](#配置文件查找规则)自动加载配置
2. 在界面中修改配置
3. 点击「保存并重启」写入文件并重启服务

> 界面底部会显示当前使用的配置文件路径，便于确认加载来源。

### CLI 模式（命令行）

```bash
# 使用本地配置启动
streamguard.exe -config config.local.json

# 查看版本
streamguard.exe -version
```

### 配置优先级

```
环境变量  >  配置文件  >  内置默认值
```

## 配置示例

### 最小配置

```json
{
  "upstream": "https://your-upstream/base-path"
}
```

其余字段使用默认值（1 秒 1 次限流）。

### 高频场景（10 秒 1 次，宽松）

```json
{
  "upstream": "https://your-upstream/base-path",
  "rate": 0.1,
  "burst": 1,
  "max_wait": "60s"
}
```

### 快速响应场景（10 次/秒）

```json
{
  "upstream": "https://your-upstream/base-path",
  "rate": 10,
  "burst": 5,
  "max_wait": "5s"
}
```

### 长文本场景（大超时）

```json
{
  "upstream": "https://your-upstream/base-path",
  "rate": 1,
  "timeout": "600s"
}
```

> **超时行为说明**：`timeout` 只作用于**非 SSE 请求**（普通 JSON 响应）。
> 流式请求（`Accept: text/event-stream` 或请求体 `stream=true`）**自动豁免超时**，
> 因此长文本流式生成不会被中断，无需为 SSE 调大 `timeout`。
> 该超时用于兜底：上游挂起时避免请求无限等待。
