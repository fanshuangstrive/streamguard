# StreamGuard 文档索引

本目录收录 StreamGuard 的设计、开发与运维文档。建议按以下顺序阅读。

## 入门

| 文档 | 说明 |
| --- | --- |
| [../README.md](../README.md) | 项目简介、快速开始、配置示例 |
| [configuration.md](./configuration.md) | 全部配置项说明（JSON 字段 / 环境变量 / 默认值） |
| [request-flow.md](./request-flow.md) | 请求处理全流程（限流 → 转发 → 响应） |

## 开发规范

| 文档 | 说明 |
| --- | --- |
| [../AGENTS.md](../AGENTS.md) | AI 协作入口规范（目录结构、核心红线） |
| [development-guide.md](./development-guide.md) | 功能开发规范（架构分层、代码规范、配置九处同步、TDD、Git 规范、自查清单） |
| [test-rules.md](./test-rules.md) | R1–R8 链路规则（改动不得破坏） |
| [testing.md](./testing.md) | 测试清单与覆盖统计 |

## 设计与运维

| 文档 | 说明 |
| --- | --- |
| [ui-design.md](./ui-design.md) | GUI 界面设计说明 |
| [upstream-models.md](./upstream-models.md) | 上游模型服务对接说明 |
| [development-log.md](./development-log.md) | 历史决策与踩坑记录 |

## 内部笔记

`docs/local/` 存放个人临时笔记，**不提交到仓库**（见 `.gitignore`）。
