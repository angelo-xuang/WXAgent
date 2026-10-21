# macOS 支持范围

索引库与原始微信采集是两个独立层次。

| 能力 | 当前范围 |
| --- | --- |
| Go CLI 导入既有 JSON、SQLite 查询及计数 | 共用跨平台实现；Go 测试在 Windows / Linux / macOS 执行 |
| 本地 HTTP 服务 | 共用 Go 实现，可供 GUI 使用 |
| Flutter GUI | 已有 macOS Runner，CI 执行 Debug 构建 |
| macOS 微信目录、密钥与原始数据库采集 | 尚未完成适配和实机验证 |
| GUI 内置后端打包、签名、公证及安装包 | 尚未完成发布验收 |

## 只使用索引库

需要 Go 1.24 或兼容版本，无需安装 Flutter：

```bash
cd backend
go build -o wxagent ./cmd/wxagent
./wxagent --config config.local.json init
./wxagent --config config.local.json --export-dir "$HOME/聊天导出" import
./wxagent --config config.local.json sessions
./wxagent --config config.local.json messages --keyword "项目" --limit 50
```

`init` 不覆盖已有配置。可以导入其他平台生成的 JSON；默认索引在配置目录的 `data/wxagent.db`。不需要 Windows 密钥工具，也不读取微信原始数据库。

## 调试 GUI

安装 Flutter 3.35.7、Xcode、CocoaPods。先运行后端：

```bash
cd backend
./wxagent --config config.local.json serve
```

另开终端：

```bash
cd frontend
flutter pub get --enforce-lockfile
flutter run -d macos
```

界面默认连接 `http://127.0.0.1:8000`。当前建议独立启动 CLI 服务，先验证索引导入、原文筛选、分页和中文显示。

打开 Xcode 使用 `frontend/macos/Runner.xcworkspace`。只检查编译：

```bash
flutter build macos --debug --no-pub
```

## 沙盒与权限

现有 Runner 保留 App Sandbox。Debug/Profile 与 Release 均声明网络客户端及用户所选文件读写权限，供界面连接本地服务和选择目录。Debug/Profile 额外保留 Flutter 调试权限。

GUI 获得的目录授权不会因为传递路径字符串就自动授予独立 Go 进程。当前后端由用户在终端启动并使用其权限。后续打包后端时需实机验证进程关系、目录权限与重启后授权；不得把取消沙盒作为适配完成。

## 后续验收

1. Apple Silicon 实机启动 CLI，导入样本两次，确认去重、筛选、源数据不变和数据库可备份。
2. 检查 GUI 中文字体、缩放、键盘焦点、窗口尺寸与目录选择。
3. 检查后端连接、断线重连、退出以及进程回收。
4. 单独实现和测试 Mac 微信数据获取，平台不支持时保持明确提示。
5. 分发前完成正式标识、签名、公证及安装包权限复验。

Windows 开发机的交叉编译和 CI 构建不能替代以上实机验收。当前不生成 macOS Release。
