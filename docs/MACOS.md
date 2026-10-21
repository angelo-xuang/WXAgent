# macOS 开发基础与适配边界

`frontend/macos/` 提供 Flutter 3.35.7 的标准 Runner、Xcode 工程和 CocoaPods 接入。窗口默认使用 1180 × 800 的内容区域，最小窗口为 800 × 600，标题为 WXAgent。当前产品完整的数据获取流程仍以 Windows 为主。

## 当前基础

| 部分 | 当前状态 |
| --- | --- |
| Flutter 界面工程 | 已加入 macOS Runner，应用包默认名称为 `wx_agent_app.app` |
| HTTP 请求 | Debug/Profile、Release 均声明网络客户端权限，可请求本机后端及配置的服务 |
| 用户选择目录 | 两种 entitlement 文件均声明用户选定文件的读写权限 |
| 原生构建检查 | GitHub CI 执行 `flutter build macos --debug`，不生成发布版本 |
| 微信目录识别、密钥获取、真实数据库接入 | 尚未完成 macOS 实机适配及验证 |
| 应用内后端打包、权限传递、签名、公证、安装包 | 尚未完成发布验收 |

当前 Windows 开发环境无法完成 Xcode 编译与 macOS 实机功能验收。CI 构建结果应以对应提交的 GitHub Actions 记录为准，不把工程文件已存在视为完整适配完成。

## 在 Mac 上调试

准备 Flutter 3.35.7、Go 1.24.x、Xcode 和 CocoaPods；通过 `flutter doctor -v` 检查本机工具链。macOS 应用必须在 macOS 上构建。

先在仓库根目录执行源码检查：

```bash
bash scripts/dev.sh check
```

随后单独运行本地后端：

```bash
cd backend
[ -e config.local.json ] || cp config.json config.local.json
go run ./cmd/wxagent_backend --config config.local.json server
```

`config.local.json` 是本机副本，按实际环境设置数据输出路径和模型配置；勿覆盖已有副本。示例配置中的 Windows 微信组件路径不能直接用于 Mac。当前应先验收后端连接、界面和已导出数据的处理，不依赖 Windows 的密钥程序或自动导出组件。

在第二个终端启动前端：

```bash
cd frontend
flutter pub get --enforce-lockfile
flutter run -d macos
```

需要打开 Xcode 工程时，使用 `frontend/macos/Runner.xcworkspace`。命令行只验证构建可执行：

```bash
cd frontend
flutter build macos --debug --no-pub
```

## 权限设计

两个 entitlement 文件均保留 App Sandbox，并加入：

- `com.apple.security.network.client`：前端访问 HTTP 服务，包括 `127.0.0.1` 的本地后端。
- `com.apple.security.files.user-selected.read-write`：用户通过系统选择器授权的文件或目录，支持导入与输出目录选择。

Debug/Profile 额外保留 Flutter 默认的 JIT 和网络服务权限，以便调试器工作。Release 不开放网络服务权限，因为当前前端仅作为客户端。

用户选定文件的授权作用于应用沙盒，不等同于“可读取任意微信目录”，也不会仅因传递一个路径字符串就自动授权独立 Go 后端。后续需明确后端运行位置、沙盒与进程关系、目录授权持续时间，并在需要跨重启访问时实现和验证 security-scoped bookmarks。不要通过删除所有沙盒限制代替权限适配。

## 后续实机验收

1. 在目标 Apple Silicon Mac 上构建和启动，检查目录选择、中文显示、缩放、键盘焦点及窗口大小。
2. 验证本地后端启动、状态探测、重连、退出和孤儿进程回收。
3. 针对实际 macOS 微信版本验证数据目录、密钥获取和数据库格式；平台不可用时提供明确提示。
4. 在独立样本副本上验证导入、分析与输出，确认源数据库不被修改。
5. 明确分发渠道后，再配置正式 Bundle ID、应用图标、签名、公证与安装包，并复验权限。

以上验收通过前，不应将 macOS 标记为完整支持，也不应发布对应安装包。

## 官方参考

- [Flutter：设置 macOS 开发环境](https://docs.flutter.dev/platform-integration/macos/setup)
- [Flutter：macOS 构建、App Sandbox 与 entitlement](https://docs.flutter.dev/platform-integration/macos/building)
- [Flutter：桌面应用支持](https://docs.flutter.dev/platform-integration/desktop)
- [Apple：App Sandbox](https://developer.apple.com/documentation/security/app-sandbox)

当前工程固定使用 Flutter 3.35.7；官方文档可能对应更新版本。升级 SDK、目标系统或插件时，应重新确认最低系统版本与架构支持。
