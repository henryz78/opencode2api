# Design direction approved

## 用户选择

用户原话：`我选第二个`

已选择：**Field Manual / 运维手册**。

## 三版初稿记录

- Signal Room：`design-demos/signal-room.html`，深色值守台；用户通过本地 HTML 预览查看。
- Field Manual：`design-demos/field-manual.html`，暖白出版物式运维手册；用户在当前会话的 in-app browser 中打开并据此选择。
- Command Atlas：`design-demos/command-atlas.html`，命令拓扑图谱；用户通过本地 HTML 预览查看。

本次方向选择允许进入生产 WebUI 实施。实施范围：保留现有后端 API、字段、认证/CSRF、安全确认和 SSE 日志行为；重做 `webui/index.html` 的信息架构、组件层级、视觉系统和响应式布局。未选择的方向仅作为设计探索保留，不合并到生产界面。
