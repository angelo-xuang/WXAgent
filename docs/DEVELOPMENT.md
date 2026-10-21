# 源码开发与质量检查

WXAgent 的主应用由 `frontend/`（Flutter 界面）、`backend/`（Go 数据服务）和 `launcher/`（桌面启动器）组成。`modules/` 保存独立的上游应用，不纳入本项目的批量格式化范围。

## 开发环境

- Flutter **3.35.7**（包含 Dart **3.9.2**），与当前工程及 CI 保持一致。
- Go **1.24.x** 或兼容版本；CI 使用 1.24 系列的最新补丁版本，满足 `backend/go.mod`。
- Windows 使用 PowerShell；macOS / Linux 使用 Bash。
- 将 Flutter 的 `bin` 和 Go 工具加入 `PATH`。
- 原生桌面构建需要对应系统工具链：Windows 使用 Visual Studio C++ 桌面开发组件；macOS 使用 Xcode 和 CocoaPods。

升级 SDK 时同时更新 CI、运行格式检查及回归测试。`frontend/pubspec.lock` 应提交版本控制，日常检查使用锁定依赖，不自动升级。

## 格式化与验收

从仓库根目录执行。`format` 会修改自有源码；`check` 检查格式、静态分析和测试，失败会以非零状态退出。

Windows：

```powershell
./scripts/dev.ps1 format
./scripts/dev.ps1 check
```

macOS / Linux：

```bash
bash scripts/dev.sh format
bash scripts/dev.sh check
```

第二个参数可以选择 `go` 或 `flutter`，省略则检查两者，例如：

```powershell
./scripts/dev.ps1 check go
./scripts/dev.ps1 check flutter
```

```bash
bash scripts/dev.sh check go
bash scripts/dev.sh check flutter
```

检查内容：

| 范围 | 格式化 | 检查 |
| --- | --- | --- |
| `backend/`、`launcher/` | `gofmt`，跳过 `vendor/` | `go test ./...`、`go vet ./...` |
| `frontend/lib/`、`frontend/test/` | `dart format` | 格式差异、`flutter analyze`、`flutter test` |

检查前会通过 `flutter pub get --enforce-lockfile` 准备依赖缓存。缓存和 Flutter 平台生成文件可能更新，但源码格式不会由 `check` 自动改写。

`.editorconfig` 统一编辑器的缩进及文件末尾处理；`.gitattributes` 统一自有源码换行，并保留 Windows 批处理的 CRLF。无需对整个仓库运行换行重写或递归格式化命令。

## 本地调试

先启动后端，在另一个终端启动界面：

```bash
cd backend
go run ./cmd/wxagent_backend server
```

```bash
cd frontend
flutter pub get --enforce-lockfile
flutter run -d windows
```

macOS 使用 `flutter run -d macos`；当前功能边界和准备步骤见 [macOS 开发说明](MACOS.md)。默认后端地址为 `http://127.0.0.1:8000`。

个人配置保存为 `backend/config.local.json`，不要把 API Key 或实际聊天路径写回公共示例配置。需要本地配置时，以 `go run ./cmd/wxagent_backend --config config.local.json server` 启动。不得提交聊天数据、导出内容、密钥、`.env` 或本机配置。

## GitHub 自动检查

`.github/workflows/source-checks.yml` 在推送 `main`、提交 Pull Request 或手动触发时运行：

1. 在 Windows、Linux、macOS 检查 Go 格式、测试和 `vet`。
2. 检查 Flutter 格式、静态分析和测试。
3. 在 macOS 编译 Debug 界面工程，及时发现原生工程或插件接入错误。

该工作流只有仓库读取权限，不运行发布脚本、不创建标签、不上传安装包、不修改 GitHub Release。macOS Debug 编译通过仅表示界面工程能够构建，不代表微信读取、权限、签名或分发流程已经验收。
