# CLI 与数据约定

## 命令

`wxagent [全局选项] 命令 [命令选项]`。全局选项也可放在命令后。

| 命令 | 行为 |
| --- | --- |
| `help [命令]`、`--help` | 显示帮助，不读取配置 |
| `version`、`--version` | 输出版本信息 |
| `init` | 创建最小本机配置；已有文件拒绝覆盖 |
| `import` | 将导出 JSON 增量写入 SQLite 索引 |
| `status` | 查询后端路径、平台能力和索引状态 |
| `sessions` | 列出已入库的会话 |
| `messages` | 查询原文及匹配总量 |
| `stats` | 对筛选结果做确定性的消息、会话及类型计数 |
| `serve` | 启动本地 HTTP 服务；`server` 是兼容别名 |
| `export` | Windows 原生微信数据采集到 JSON，并自动入库 |
| `refresh-key` | 刷新 Windows 采集所需的密钥状态，不输出密钥 |

查询命令无需启动 HTTP 或 GUI。`analyze-chat`、`summarize`、`start` 不再提供。

## 配置与路径

- `--config PATH`：显式指定配置；不存在或内容无效时报错。
- 未指定配置时，尝试当前目录的 `config.json`，没有则使用当前目录下的安全默认路径。
- `--database PATH`：覆盖当前命令使用的 SQLite 路径。
- `--export-dir PATH`：覆盖 JSON 导入来源 / Windows 采集的 JSON 输出目录。
- 命令行路径相对当前工作目录解析；配置内路径相对配置文件目录解析。

新配置只包含本地路径，默认导出目录为 `exports/`，索引数据库为 `data/wxagent.db`。历史配置内的模型字段被忽略，配置 API 不返回或接受这些字段；更新本地路径不会擅自清空旧文件中的未知字段。

## 查询

```bash
wxagent --database ./records.db sessions
wxagent --database ./records.db messages --session group@chatroom --keyword "会议" --start-date 2026-10-01 --end-date 2026-10-22 --offset 0 --limit 50
wxagent --database ./records.db stats --session group@chatroom
```

`--session` 可重复，`--sessions` 接受逗号分隔的 ID。使用 `sessions` 返回的稳定 ID，不依赖显示名称匹配。日期为严格的 `YYYY-MM-DD`，按 UTC+8 解释，包含开始与结束两天；开始晚于结束、负分页值均报错。

关键词匹配消息正文。它是文字检索，不是语义搜索。查询返回稳定的时间顺序，匹配总数在分页前计算。默认 `limit=0` 返回全部；GUI 显式使用分页并显示总量。`stats` 统计完整筛选结果，不受分页影响。

`messages` 输出结构：

```json
{
  "messages": [
    {
      "session_id": "group@chatroom",
      "display_name": "项目组",
      "local_id": 0,
      "create_time": 1792544400,
      "formatted_time": "2026-10-21 09:00:00",
      "type": "文本消息",
      "content": "示例原文",
      "is_send": 0,
      "sender": "sample-user",
      "sender_name": "示例成员"
    }
  ],
  "total": 1,
  "offset": 0,
  "limit": 50
}
```

数据命令将 JSON 写到 stdout，诊断与采集日志写到 stderr。退出码：`0` 成功，`1` 操作失败，`2` 命令或参数不合法。调用方先检查退出码，再解析 stdout；不要将 stderr 拼进 JSON。

## 导入与持久化

支持两种现有导出结构：`session.wxid + messages`（camelCase 消息字段）及 `session_id + session_name + messages`（snake_case）。目录中包含 JSON 的 `chat_history/` 优先，否则读取指定目录下非隐藏的 JSON 文件。不会递归扫描任意文件夹。

导入逐文件读取并记录内容指纹。未变化文件跳过；变化文件在事务内更新消息与来源记录。格式错误会令整次导入失败，不留下部分消息提交。源 JSON 始终只读。

已有消息不会因源文件缩短或消失自动删除。索引用于长期归档，不是源目录的删除镜像。可为不同账号使用独立数据库，避免不同来源账号的会话 ID 冲突。

消息有本地 ID 时结合会话、ID 与时间标识；没有 ID 时使用稳定字段指纹和同文件重复序号。这样可以保留同秒同内容的多条记录，并使同一份快照重复导入保持幂等。无稳定 ID 的独立增量片段若包含完全相同的消息，无法凭现有字段绝对区分来源；这属于导出格式的信息限制。

数据库采用 SQLite，可通过标准 SQLite 工具只读检查。由应用维护 schema 和导入清单，不直接改库内结构。关闭写入进程后备份数据库，或使用 SQLite 自身的备份工具；如果有事务辅助文件，不要在写入时只复制单个主文件。

## 本地 HTTP

`wxagent serve --host 127.0.0.1 --port 8000`。默认本机使用，无公网认证。

| 方法与路径 | 用途 |
| --- | --- |
| `GET /health` | 服务健康检查 |
| `GET /status` | 平台能力、路径和索引状态 |
| `GET /index/status` | 索引是否存在、数据库路径、消息 / 会话 / 来源数量 |
| `POST /index/import` | 增量导入；可传 `{"export_dir":"..."}` |
| `GET /sessions` | 会话数组 |
| `GET /messages` | 原文查询，返回 `messages,total,offset,limit` |
| `GET /stats` | 完整筛选结果的基础计数 |
| `GET /config`、`PUT /config` | 查看 / 修改核心本地路径 |
| `POST /config/reload` | 重新读取配置 |

查询参数使用 `session_id`（可重复或逗号分隔）、`start_date`、`end_date`、`keyword`、`offset`、`limit`。参数错误为 HTTP 400。原生微信入口受平台能力控制；非 Windows 返回明确的不可用状态。

`/llm/*`、`/summary/*`、`/analysis/*` 已移除。GUI、CLI 和外部 Skill 以索引和原文接口为共同基础。
