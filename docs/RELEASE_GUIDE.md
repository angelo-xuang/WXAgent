# WXAgent 发布指南

WXAgent 以本地 SQLite 聊天记录索引库为核心，提供独立 CLI、可选桌面界面和本地 HTTP 接口。Windows 发布包包含现有的微信连接工具；不包含 AI 分析、模型配置或 EchoTrace 桌面应用。

## 构建环境

- Windows 10/11 x64。
- Go 1.24 或更高版本；以 `backend/go.mod` 为准。
- Flutter 3.35.7，Dart 3.9.2，与源码检查环境保持一致。
- Visual Studio 2022 的“使用 C++ 的桌面开发”工作负载、Windows SDK 和 VC143 x64 可再发行运行库。
- Go、Flutter、Git 已加入 PATH。

构建使用现有 Go 和 Flutter 依赖锁定文件，不升级依赖。运行发布包不需要 Go、Flutter 或 Visual Studio；包内为桌面组件附带 VC143 x64 运行库。

## 发布前检查

1. 更新 `frontend/pubspec.yaml`、根目录 `CHANGELOG.md`、README 和快速开始中的版本说明。
2. 执行源码检查，检查安装脚本及发布配置，不使用真实聊天记录作为测试材料。
3. 提交全部发布源码。构建要求已跟踪文件没有未提交改动，产物记录对应的 Git commit。

```powershell
.\scripts\dev.ps1 check
git status --short
```

## 构建 v1.1.0

在仓库根目录执行：

```powershell
.\scripts\build_release.ps1 -Version 1.1.0 -BuildNumber 2
```

也可通过批处理调用同一流程：

```bat
scripts\build_release.bat -Version 1.1.0 -BuildNumber 2
```

默认生成三个全新产物：

```text
dist/
├── WXAgent-v1.1.0-windows-x64/
├── WXAgent-v1.1.0-windows-x64.zip
└── WXAgent-v1.1.0-windows-x64.zip.sha256
```

任何目标已存在时，构建直接停止。脚本不会清空旧 `release/`、`dist/`，不会关闭运行中的应用，也不会读取或复制本地数据库、导出数据和开发配置。失败后的唯一临时目录保留供诊断。需要重试时，可指定新的 `-OutputDir dist/WXAgent-v1.1.0-windows-x64-retry`；输出只能位于仓库的 `dist/` 或 `release/` 子目录，且不能经过目录链接。

## 发布包结构

```text
WXAgent-v1.1.0-windows-x64/
├── Install.cmd                  # 双击注册当前用户安装
├── install_windows.ps1
├── uninstall_windows.ps1
├── WXAgent.exe                  # 可选桌面启动器
├── config.json                  # 不含账号或密钥的相对路径模板
├── backend/wxagent.exe           # 独立 CLI 与 HTTP 服务
├── frontend/                    # Flutter 界面与运行库
├── modules/wx_key/              # 现有 Windows 微信连接工具与运行库
├── licenses/wx_key.txt
├── README.md
├── QUICKSTART.md
├── CHANGELOG.md
├── docs/
├── skills/wxagent-index/SKILL.md
├── runtime-dependencies.json    # 构建时检查的 DLL 依赖
└── release-manifest.json        # 版本、源码 commit 与逐文件 SHA256
```

CLI 位于 `backend/`，避免 Windows 上 `wxagent.exe` 与根目录 `WXAgent.exe` 大小写不敏感导致冲突。所有用户数据库和导出目录由运行配置决定，发布包不预置数据。

## 验收发布包

先检查 ZIP 的 SHA256 与同名 `.sha256` 文件一致，并检查清单版本及源码 commit。

```powershell
Get-FileHash .\dist\WXAgent-v1.1.0-windows-x64.zip -Algorithm SHA256
.\dist\WXAgent-v1.1.0-windows-x64\backend\wxagent.exe version
.\dist\WXAgent-v1.1.0-windows-x64\backend\wxagent.exe help
```

CLI 版本输出应包含 `version: 1.1.0`、`platform: windows` 和 `arch: amd64`。GUI 文件版本应为 `1.1.0.2`。构建脚本检查每个 EXE/DLL 的直接依赖，并验证 Visual C++ 运行库已放在应用目录。

使用独立临时目录和模拟 JSON 验收导入、重复导入、检索、筛选及导入失败回滚；不要对现有用户数据库试错。安装后验证新终端的 `wxagent version`、开始菜单、桌面快捷方式及“已安装的应用”条目，检查 GUI 与 CLI 指向同一份配置。升级验收须确认原数据库和用户配置保留。

## 安装与更新

解压 ZIP 后双击 `Install.cmd`，或运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install_windows.ps1
```

安装面向当前 Windows 用户，不需要管理员权限。安装器注册 CLI、桌面和开始菜单入口，并将程序与用户数据分开保存；更新保留已有配置和数据库。命令行注册完成后，重新打开终端。卸载入口位于 Windows“已安装的应用”，具体数据保留行为见安装说明。

`-ExecutionPolicy Bypass` 只作用于该脚本进程，不修改全局 PowerShell 策略。直接双击包内 `WXAgent.exe` 也可使用便携模式，此时数据路径以包内 `config.json` 为准。

## GitHub Release

完成源码与二进制验收后，给清单对应的 commit 创建 `v1.1.0` 标签，并上传 ZIP 和 SHA256 文件。发布说明应说明 CLI 优先、本地 SQLite 索引、安装方法和平台边界。历史 `v1.0.0` 的标签与资产保持不变。

本构建脚本只生成包，不自动发布 GitHub Release、不安装到本机，也不会生成 macOS 安装包。macOS 当前支持通过 CLI 索引已有导出记录；原生微信采集能力另行适配。
