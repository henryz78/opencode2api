# Changelog

## v1.2.0 — 2026-08-20

### Added

- Railway 单公网端口模式：WebUI、管理 API、健康检查和 `/v1/*` API 共用 Railway 注入的 `PORT`，按路径分流。
- 三协议 Playground 与诊断能力：Chat Completions、Responses、Anthropic Messages 均通过真实 Gateway 路由。
- models.dev 价格与弃用信息的每日刷新、磁盘缓存、过期状态和保留旧缓存回退。
- OpenCode Zen 匿名模式的免费模型判断、Zen/Go Key Tier 回退和模型路由诊断。
- 跨协议 `user` / `safety_identifier` 保留，以及上游明确要求时的稳定匿名标识兼容重试。
- thinking/reasoning、工具调用、工具结果、SSE 分片和跨协议转换的兼容处理。
- GitHub Actions 分支镜像构建与版本 Release 工作流，支持多架构 GHCR 镜像和 Linux、Windows、macOS 二进制包。

### Compatibility notes

- Railway Domain Target Port 应选择 Railway 注入的 `$PORT`；本版本不再将 `8081` 作为 Railway 公网 API 端口。
- 工具调用优先使用 `tool_choice: "auto"`。部分免费模型不接受指定工具名或强制工具选择，但可能仍接受工具定义并自主决定是否调用。
- 普通请求不会无条件增加 `safety_identifier` 或 `user` 字段；只有上游明确返回缺少终端用户标识时才触发兼容重试。

### Validation

- 7 个匿名免费模型的三协议普通非流式请求：21/21 通过。
- 7 个匿名免费模型的三协议普通流式请求：21/21 通过。
- 受工具选择限制的模型改用 `auto` 后，三协议非流式与流式工具请求：18/18 返回成功。
