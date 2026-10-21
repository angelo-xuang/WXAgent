# 开发与质量检查

主逻辑在 `backend/`，CLI 是主要产品入口；`frontend/` 是可选 Flutter GUI，`launcher/` 用于桌面进程管理。`modules/` 保存上游应用，不参与批量格式化。

## 环境

- CLI：Go 1.24 或兼容版本；CI 固定 Go 1.24 系列。
- GUI：Flutter 3.35.7 / Dart 3.9.2，按 `pubspec.lock` 安装依赖。
- Windows 桌面：Visual Studio C++ 桌面开发组件。
- macOS 桌面：Xcode 与 CocoaPods；见 [Mac 说明](MACOS.md)。

## 格式与检查

从仓库根目录执行：

```powershell
./scripts/dev.ps1 format
./scripts/dev.ps1 check
```

macOS / Linux：

```bash
bash scripts/dev.sh format
bash scripts/dev.sh check
```

第二个参数可选 `go` 或 `flutter`，仅使用 CLI 时运行 `./scripts/dev.ps1 check go` 或 `bash scripts/dev.sh check go` 即可。

| 范围 | 格式化 | 检查 |
| --- | --- | --- |
| backend、launcher | gofmt | go test、go vet |
| frontend/lib、frontend/test | dart format | flutter analyze、flutter test |

`check` 不自动改写源码。Flutter 检查使用 `flutter pub get --enforce-lockfile`，不主动升级依赖。`.editorconfig` 和 `.gitattributes` 统一自有源码风格。

## CLI 和服务调试

```bash
cd backend
go run ./cmd/wxagent help
go run ./cmd/wxagent --config config.local.json init
go run ./cmd/wxagent --config config.local.json --export-dir /path/to/exports import
go run ./cmd/wxagent --config config.local.json messages --limit 50
go run ./cmd/wxagent --config config.local.json serve
```

`init` 仅执行一次，已有配置不覆盖。接口和存储约定见 [CLI 文档](CLI.md)。

另开终端进入 `frontend/`，运行 `flutter run -d windows` 或 `flutter run -d macos`。默认后端地址 `http://127.0.0.1:8000`；可以通过 `WXAGENT_BACKEND_URL` 传入其他本地服务地址。

GUI 自动寻找已编译后端时可用 `WX_AGENT_BACKEND_EXE` 指定可执行文件、`WX_AGENT_CONFIG` 指定配置。桌面启动器优先使用 `backend/wxagent.exe`（其他系统无扩展名），也兼容历史后端名称。Windows 不要用 CLI 替换包根目录的 `WXAgent.exe` 启动器。

## 验收重点

使用临时目录和虚构聊天记录，禁止在测试中读取真实微信数据库或调用真实密钥组件。

- 导入：重复导入幂等、变化文件增量更新、坏文件整批回滚、源文件保持不变。
- 数据：同秒重复消息保留、会话和时间稳定排序、文字及日期筛选、全量与分页一致。
- 隔离：只读查询不隐式导入或创建数据库；损坏数据库明确报错。
- CLI：stdout 为可解析 JSON，stderr 为诊断，错误退出码正确；配置初始化不覆盖文件。
- HTTP：参数校验与 CLI 一致，已移除 AI 路由不可调用。
- GUI：多种窗口宽度、较大字体、过滤与分页、导入失败反馈及重连。
- 采集：原有 Windows 采集入口只做模拟及平台边界测试，实机验证单独进行。

需要排查并发时在 backend 运行 `go test -race ./...`。修改查询与持久化时优先测试数据不变量，不通过复制实现逻辑来构造断言。

## GitHub 检查与发布边界

`.github/workflows/source-checks.yml` 对 main 推送和 PR 执行：

1. Windows / Linux / macOS 上的 Go 格式、测试和 vet。
2. Flutter 格式、静态检查及组件测试。
3. macOS Debug GUI 构建。

工作流只有仓库读取权限，不发布包、不创建标签、不修改 Release。构建通过不等于完成 Mac 微信原始数据采集或签名分发验收。

个人配置、聊天 JSON、SQLite 文件、密钥、`.env` 和本地生成目录不得提交。旧发布脚本和配置属于历史发布流程，当前 CLI 源码说明不要求重新打包。
