# AGENTS.md（code-switch-R）

OUTPUT_LANGUAGE: 简体中文

## 项目结构

- 后端：Go（Wails）
- 前端：Vue 3 + Vite（`frontend/`）

## 常用命令（macOS sandbox 友好）

- Go 测试：`GOCACHE=/tmp/go-build-cache GOMODCACHE=/tmp/go-mod-cache go test ./...`
- 前端构建：`cd frontend && pnpm -s build`

## 环境要求

- Node.js：Vite 7 需要 `>=20.19` 或 `>=22.12`（Windows 上若报 Node 版本过低，请先升级再跑 `wails3 dev`）

## 版本与发布

- 版本号：`version_service.go` 的 `AppVersion`
- Release notes：`RELEASE_NOTES.md` 顶部版本需与 `AppVersion` 对齐
- 发布脚本：`scripts/publish_release.sh`

## 关键约定（避免回归）

- 日志：`enable_logging` 默认关闭；关闭时不要落盘任何日志/历史（request_log、MITM、console、监控、测速等）
- MCP：导入/编辑需支持 HTTP `headers` 与 `startup_timeout_sec`；保存后会同步到 Claude/Codex/Gemini 的用户配置文件
