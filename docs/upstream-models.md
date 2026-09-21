# 上游模型清单

StreamGuard 代理的上游模型服务，协议统一为 **OpenAI / V1**。

> 真实的上游地址与内部模型清单见 `docs/local/upstream-models.local.md`（不提交 git）。

## 服务信息

| 项 | 值 |
|---|---|
| 上游基础地址 | `<your-upstream-host>/<base-path>` |
| 代理路径 | `/v1/chat/completions` |
| 完整上游地址 | `<upstream>/v1/chat/completions` |
| 协议 | OpenAI / V1 |
| 认证方式 | `Authorization: Bearer <api_key>`（由客户端携带） |

## 可用模型

客户端在请求体的 `model` 字段中指定模型：

| 模型名称 | 上下文长度 | 适用场景 | 协议 |
|---|---|---|---|
| `<model-name-1>` | 200K | 文本生成 | OpenAI / V1 |
| `<model-name-2>` | 200K | 文本生成 | OpenAI / V1 |
| `<model-name-3>` | 200K | 文本生成 | OpenAI / V1 |

## 调用示例

### 通过 StreamGuard 代理调用（推荐）

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <api_key>" \
  -d '{
    "model": "<model-name>",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

StreamGuard 不修改请求内容，`Authorization` 头原样透传给上游。

### 切换模型

只需修改请求体中的 `model` 字段：

```json
{ "model": "<model-name-2>", "messages": [...] }
```

### 直连上游（对比参考）

```bash
curl <upstream>/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <api_key>" \
  -d '{"model":"<model-name>","messages":[{"role":"user","content":"你好"}]}'
```

## 模型选择建议

| 场景 | 推荐模型 | 理由 |
|---|---|---|
| 通用对话、快速响应 | Flash 系列 | 延迟低 |
| 中文理解、结构化输出 | GLM 系列 | 中文场景表现好 |
| 复杂推理、长文本 | 大参数量模型 | 推理能力更强 |

> 注：以上建议基于模型命名与常见定位推断，实际效果请以业务测试为准。
