# WXAgent

本地聊天记录索引库，CLI 优先，GUI 可选。

WXAgent 将已有聊天导出文件增量导入 SQLite，提供会话列表、原文检索、日期筛选和基础计数。记录保存在本机，命令行、桌面界面和本地 HTTP 接口使用同一个索引库。主应用不集成 AI 分析、AI 总结、提示词或模型服务配置。

[快速开始](QUICKSTART.md) · [CLI 与数据约定](docs/CLI.md) · [开发与检查](docs/DEVELOPMENT.md) · [macOS](docs/MACOS.md) · [调用 Skill](skills/wxagent-index/SKILL.md)

## 核心流程

```text
已有聊天 JSON ── import ── SQLite 索引库 ── CLI / 本地 HTTP / 可选 GUI
      ↑
Windows 微信采集（可选）
```

- **增量入库**：记录源文件指纹，重复导入跳过未变化文件；更新记录并保留历史。
- **原文查询**：按会话 ID、日期和关键词过滤，返回带发送者与时间的结构化消息。
- **本地存储**：SQLite 单文件数据库，不要求安装数据库服务或 Flutter。
- **脚本集成**：查询结果以 JSON 写入标准输出，错误写入标准错误，适合管道及 Skill 调用。
- **轻量 GUI**：管理索引、浏览记录、筛选原文，Windows 原生微信采集作为辅助入口。
- **数据完整性**：一次导入失败时事务回滚；源文件消失不会自动删除索引内的历史记录。

## 先用 CLI

仅需要 Go 1.24 或兼容版本。在仓库根目录执行：

```powershell
cd backend
go build -o wxagent.exe ./cmd/wxagent
./wxagent.exe --config config.local.json init
./wxagent.exe --config config.local.json --export-dir "D:/聊天导出" import
./wxagent.exe --config config.local.json sessions
./wxagent.exe --config config.local.json messages --keyword "项目" --limit 50
```

macOS / Linux 将构建产物改为 `wxagent`，运行 `./wxagent`。CLI 的导入、SQLite 索引和查询使用同一套 Go 实现；无需启动 GUI 或 HTTP 服务。

`init` 只创建新配置，不覆盖已有文件。默认数据库为配置目录下的 `data/wxagent.db`。已有配置中的旧模型字段不会生效，新配置不包含这些字段。

## 可选界面与接口

```powershell
./wxagent.exe --config config.local.json serve --host 127.0.0.1 --port 8000
```

然后在另一个终端进入 `frontend/`，运行 `flutter pub get --enforce-lockfile` 和 `flutter run -d windows`。Mac 使用 `flutter run -d macos`。

桌面界面通过本地 HTTP 连接索引库。默认监听 `127.0.0.1:8000`，接口没有公网认证，按本地工具使用。面向助手的 [Skill](skills/wxagent-index/SKILL.md) 只说明如何调用 CLI，不给应用增加模型依赖。

## 源码结构

| 目录 | 用途 |
| --- | --- |
| `backend/cmd/wxagent/` | 主要 CLI 入口 |
| `backend/internal/cli/` | 命令解析与 JSON 输出 |
| `backend/internal/dataset/` | JSON 解析、SQLite 索引、查询 |
| `backend/internal/server/` | GUI 使用的本地 HTTP 接口 |
| `frontend/` | 可选 Flutter 桌面界面 |
| `launcher/` | 桌面打包启动器 |
| `skills/wxagent-index/` | 外部助手调用约定 |
| `modules/` | 历史上游微信组件 |

`backend/cmd/wxagent_backend/` 保留为兼容入口，运行相同的 CLI。发布目录和上游模块不参与批量格式化。

## 平台与版本边界

Windows 保留已有微信采集入口。macOS / Linux 可处理已导出的 JSON 并查询索引库；Mac 微信目录识别、密钥读取和原始数据库采集仍需单独适配。macOS GUI 工程由 CI 检查构建，尚未完成签名、安装包和实机采集验收。

`main` 是当前源码主线，已有 `v1.0.0` Release 和安装包保留历史功能，本次源码改动不更新它们。新定位以本页及当前源码为准。

个人配置使用 `*.local.json`。聊天 JSON、SQLite 索引、密钥和本机配置不要提交到 Git；导入不会修改源聊天导出文件。
