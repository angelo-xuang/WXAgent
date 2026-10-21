# 快速开始

## 使用发布包（Windows）

从 [v1.1.0 Release](https://github.com/angelo-xuang/WXAgent/releases/tag/v1.1.0) 下载 Windows x64 ZIP，完整解压后双击 `Install.cmd`。安装完成后，从开始菜单打开 WXAgent，或重新打开终端运行：

```powershell
wxagent version
wxagent --export-dir "D:/聊天导出" import
wxagent sessions
wxagent messages --keyword "活动" --limit 50
```

安装版自动使用 `%LOCALAPPDATA%/WXAgent/config.json`，无需重复 `init`。程序、用户配置与数据库分开保存，升级和卸载保留用户数据。也可以不安装，在解压目录直接运行 `WXAgent.exe`；便携版和安装版的默认数据位置不同。

下文说明从源码编译与更详细的操作。已安装用户可直接用 `wxagent` 替换示例中的 `./wxagent.exe --config config.local.json`。

## 1. 编译命令行工具

只使用索引库时需要 Go 1.24 或兼容版本，不需要 Flutter。

Windows，在仓库根目录执行：

```powershell
cd backend
go build -o wxagent.exe ./cmd/wxagent
./wxagent.exe help
./wxagent.exe --config config.local.json init
```

macOS / Linux：

```bash
cd backend
go build -o wxagent ./cmd/wxagent
./wxagent help
./wxagent --config config.local.json init
```

后续示例用 Windows 名称；其他系统替换为 `./wxagent`。帮助与版本命令不依赖配置文件。`init` 遇到已存在的配置会报错并保留原内容。

## 2. 导入已有聊天记录

支持 WXAgent / EchoTrace 的会话 JSON。将 `--export-dir` 指向导出目录；如果其下存在包含 JSON 的 `chat_history/`，优先读取该目录。

```powershell
./wxagent.exe --config config.local.json --export-dir "D:/聊天导出" import
./wxagent.exe --config config.local.json status
```

导入结果显示扫描文件、变化文件和库内记录数量。重复执行只处理变化文件。导入和查询均不修改原 JSON。

默认数据库是配置文件旁的 `data/wxagent.db`。也可以直接指定独立数据库：

```powershell
./wxagent.exe --database "D:/聊天索引/records.db" --export-dir "D:/聊天导出" import
./wxagent.exe --database "D:/聊天索引/records.db" sessions
```

查询时始终使用同一个配置或 `--database` 路径。命令行传入的路径以当前工作目录为基准；配置内路径以配置文件所在目录为基准。

## 3. 查询原文

```powershell
./wxagent.exe --config config.local.json sessions
./wxagent.exe --config config.local.json messages --keyword "活动" --start-date 2026-10-01 --end-date 2026-10-22 --limit 50
./wxagent.exe --config config.local.json messages --session "群聊ID@chatroom" --offset 50 --limit 50
./wxagent.exe --config config.local.json stats
```

会话 ID 从 `sessions` 返回值中获取。日期按 UTC+8 计算，起止日期均包含整天。`messages` 返回 `messages` 和匹配总量 `total`。默认 `--limit 0` 返回全部；需要分页时显式传入 `--limit` 和 `--offset`。

查询结果是 JSON，可重定向为文件或交给其他工具读取。详见 [CLI 与数据约定](docs/CLI.md)。

## 4. 可选 GUI

需要 Flutter 3.35.7（Dart 3.9.2）及对应系统桌面构建工具链。

在后端目录启动本地服务：

```powershell
./wxagent.exe --config config.local.json serve
```

第二个终端，从仓库根目录启动前端：

```powershell
cd frontend
flutter pub get --enforce-lockfile
flutter run -d windows
```

macOS 改为 `flutter run -d macos`。默认服务地址为 `http://127.0.0.1:8000`。也可通过 `WXAGENT_BACKEND_URL` 指定地址；界面设置提供连接入口。

GUI 可查看索引状态、导入 JSON、筛选和分页浏览记录。配置中的目录是后端所在机器的路径。Windows 微信采集需要已有原生组件；已有 JSON 的索引和查询不需要这些组件。

## 5. 可选 Windows 微信采集

在本机配置中设置 `wechat_data_path`、`wx_key_shared_prefs` 及必要的原生组件路径，然后：

```powershell
./wxagent.exe --config config.local.json refresh-key
./wxagent.exe --config config.local.json export
```

`export` 从微信原始数据库采集到 JSON，成功后自动入库；`import` 用于单独导入已有 JSON。`refresh-key --ensure` 可启动已配置的 Windows 密钥工具。CLI 构建不会自动构建上游组件。

## 排查

- **索引为空**：先运行 `import`，检查数据库路径是否一致。只读查询不会自动导入。
- **JSON 格式错误**：错误会带文件位置；修正来源或选择正确目录后重试。整个导入事务回滚，已有索引保留。
- **重复导入数量不变**：未变更文件被跳过属于正常行为。
- **连接失败**：确认服务已启动，界面地址和端口一致。可先用 CLI `status` 检查索引。
- **Mac 无法采集微信**：该平台目前使用已有 JSON 入库，原始微信接入仍待适配。
- **旧 AI 命令不可用**：主线源码已移除这些命令和 HTTP 接口，历史报告文件保留在原位置。

源码检查见 [开发说明](docs/DEVELOPMENT.md)。本流程不生成或修改 GitHub Release。
