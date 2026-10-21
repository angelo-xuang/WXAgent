# WXAgent 源码快速开始

当前完整微信接入流程面向 Windows。macOS 已有界面工程与平台边界，实际可用范围见 [macOS 开发说明](docs/MACOS.md)。本页命令用于源码开发，不生成或更新发布安装包。

## 1. 准备环境

安装 Flutter 3.35.7（Dart 3.9.2）、Go 1.24.x，以及 Visual Studio 的 C++ 桌面开发组件。将 Flutter 与 Go 加入 PATH。

在仓库根目录运行：

```powershell
./scripts/dev.ps1 check
```

该命令安装锁定的前端依赖并执行格式、静态分析和测试。格式需要调整时，运行 `./scripts/dev.ps1 format`。

## 2. 使用独立的本机配置

从仓库根目录执行：

```powershell
if (!(Test-Path backend/config.local.json)) {
    Copy-Item backend/config.json backend/config.local.json
}
```

编辑 `backend/config.local.json`，填写本机路径和模型服务配置。相对路径以配置文件所在目录为基准。该文件已被 Git 忽略；不要把 API Key、数据库密钥或聊天数据提交到仓库。

首次接入需要现有 Windows 微信组件；从源码编译主应用不会自动构建上游 `modules/` 中的所有原生组件。仅检查界面和已有导出记录时，可以先单独运行主服务。

## 3. 启动服务与界面

第一个终端：

```powershell
cd backend
go run ./cmd/wxagent_backend --config config.local.json server --host 127.0.0.1 --port 8000
```

第二个终端，从仓库根目录执行：

```powershell
cd frontend
flutter pub get --enforce-lockfile
flutter run -d windows
```

后端默认只监听本机。界面通过 `http://127.0.0.1:8000` 连接；其他地址可在启动前设置环境变量 `WXAGENT_BACKEND_URL`。由启动器启动时，该地址会自动传给界面。

直接从界面自动启动已编译后端时，可设置 `WX_AGENT_BACKEND_EXE` 指向后端程序、`WX_AGENT_CONFIG` 指向本机配置。未设置配置变量时，会依次查找后端程序所在目录与上一级目录的 `config.json`。

## 4. 使用流程

1. **连接微信**：准备本人有权访问的数据及所需组件。
2. **聊天数据**：选择本地数据路径与导出目录，执行导出。
3. **数据分析**：查看已有导出记录的统计、日期范围和会话分布。
4. **设置**：配置模型服务地址、模型名称与 API Key。
5. **AI 总结**：选择会话及日期，生成并查看总结。

读取、导出与统计在本地执行。AI 总结会将所选聊天内容发送给配置的模型服务商。

## 常见问题

- **连接失败**：先检查后端终端是否正常启动，再确认端口与界面地址一致。自动启动后端需要可执行文件及其配置；开发时建议分别启动。
- **没有聊天数据**：检查配置中的 `export_dir` 是否指向实际导出目录，勿误把微信数据库目录作为 JSON 导出目录。
- **AI 返回错误或超时**：检查模型配置、服务额度及网络。流中断会显示失败，不会当作成功；可在修复后重新运行。
- **旧状态首次总结重复处理**：新代码将导出与总结进度分开，首次会建立独立总结进度，避免漏掉此前仅导出的内容。
- **Mac 无法获取密钥**：原生微信接入尚未完成，当前应先调试界面、后端连接及已导出数据的处理。

详细的开发命令、测试范围与 CI 说明见 [源码开发与质量检查](docs/DEVELOPMENT.md)。
