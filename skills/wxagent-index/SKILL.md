---
name: wxagent-index
description: 使用 WXAgent CLI 导入本地聊天 JSON、查询 SQLite 聊天索引和定位原文。适用于已有聊天记录的检索与归档，不负责调用模型或自动采集微信。
---

# WXAgent 索引库

使用已编译的 `wxagent`（Windows 为 `wxagent.exe`）查询本地索引。项目源码可在 `backend/` 执行 `go run ./cmd/wxagent` 代替可执行文件。无需 GUI、HTTP 服务或模型配置。

## 连接目标

优先使用用户给出的可执行文件与 `--config` 或 `--database`。若路径未知，在当前项目配置中确认，不扫描其他账号的目录。先运行 `help` 和 `status` 检查当前版本与数据库位置；显式指定的配置不存在会失败，不另建一份来掩盖错误。

## 查询原文

```bash
wxagent --database /path/to/records.db sessions
wxagent --database /path/to/records.db messages --session group@chatroom --keyword "会议" --start-date 2026-10-01 --end-date 2026-10-22 --offset 0 --limit 100
wxagent --database /path/to/records.db stats --session group@chatroom
```

会话使用 `sessions` 返回的 ID；相同显示名称可能对应不同会话。日期按 UTC+8 计算并包含起止整天。关键词是正文文字匹配，不是语义搜索。

先检查退出码，再解析 stdout JSON；stderr 单独保留诊断。退出码 0 成功、1 操作失败、2 参数错误。`messages` 返回 `messages,total,offset,limit`。默认 limit=0 全量；显式分页时按 total 取完用户要求的范围，不把一页当全库结果。引用原文时保留会话、发送者、时间及内容，区分原文与外部助手的判断。

## 入库

仅在用户要求导入或更新索引时执行：

```bash
wxagent --database /path/to/records.db --export-dir /path/to/exports import
```

import 增量写入本地 SQLite，不改源 JSON。重复导入未变文件会跳过；坏文件导致整次事务回滚。源文件消失不会自动删除历史。无稳定消息 ID 时存在跨片段去重的信息边界，不声称能够恢复导出中不存在的信息。

只读查询不自动触发导入、密钥获取或微信采集。不要直接改数据库 schema；不要提交聊天数据、索引库或个人配置到 Git。WXAgent 本身不发送聊天内容到模型服务；外部工具的处理范围应遵循用户当前请求。
