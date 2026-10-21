import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app.dart';
import '../models/models.dart';
import '../theme.dart';
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class RecordsPage extends StatefulWidget {
  const RecordsPage({super.key, required this.controller});
  final AppController controller;
  @override
  State<RecordsPage> createState() => _RecordsPageState();
}

class _RecordsPageState extends State<RecordsPage> {
  final _keyword = TextEditingController();
  final _start = TextEditingController();
  final _end = TextEditingController();
  List<SessionMeta> _sessions = [];
  String? _session;
  MessagePage? _page;
  bool _loading = true;
  bool _indexExists = false;
  String? _error;
  Map<String, String?> _applied = {};
  static const _pageSize = 50;
  int _request = 0;

  int _seenRevision = 0;

  @override
  void didUpdateWidget(covariant RecordsPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_seenRevision != widget.controller.dataRevision) {
      _seenRevision = widget.controller.dataRevision;
      _load();
    }
  }

  @override
  void initState() {
    super.initState();
    _seenRevision = widget.controller.dataRevision;
    _load();
  }

  @override
  void dispose() {
    _request++;
    _keyword.dispose();
    _start.dispose();
    _end.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final request = ++_request;
    setState(() {
      _loading = true;
      _page = null;
      _error = null;
    });
    try {
      final api = widget.controller.api;
      final results = await Future.wait<Object>([
        api.listSessions(),
        api.indexStatus(),
      ]);
      if (!mounted || request != _request) return;
      setState(() {
        _sessions = results[0] as List<SessionMeta>;
        _indexExists = (results[1] as Map)['exists'] == true;
        if (!_sessions.any((item) => item.sessionId == _session)) {
          _session = null;
        }
      });
      await _search();
    } catch (error) {
      if (mounted && request == _request) {
        setState(() {
          _loading = false;
          _error = error.toString();
        });
      }
    }
  }

  Future<void> _search() async {
    _applied = {
      'session_id': _session,
      'keyword': _keyword.text.trim(),
      'start_date': _start.text.trim(),
      'end_date': _end.text.trim(),
    };
    await _query(0);
  }

  Future<void> _query(int offset) async {
    final request = ++_request;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final page = await widget.controller.api.queryMessages(
        sessionId: _applied['session_id'],
        keyword: _applied['keyword'],
        startDate: _applied['start_date'],
        endDate: _applied['end_date'],
        offset: offset,
        limit: _pageSize,
      );
      if (!mounted || request != _request) return;
      setState(() => _page = page);
    } catch (error) {
      if (mounted && request == _request) {
        setState(() => _error = error.toString());
      }
    } finally {
      if (mounted && request == _request) setState(() => _loading = false);
    }
  }

  void _reset() {
    _keyword.clear();
    _start.clear();
    _end.clear();
    setState(() => _session = null);
    _search();
  }

  @override
  Widget build(BuildContext context) => PageContainer(
    title: '聊天记录',
    subtitle: '查阅索引库中的原始消息。日期筛选包含当天，按北京时间计算。',
    children: [
      SectionCard(
        title: '检索原文',
        actions: [
          TextButton.icon(
            onPressed: _loading ? null : _load,
            icon: const Icon(Icons.refresh_rounded),
            label: const Text('刷新索引状态'),
          ),
        ],
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            LayoutBuilder(
              builder: (context, constraints) {
                final width = constraints.maxWidth >= 620
                    ? (constraints.maxWidth - 16) / 2
                    : constraints.maxWidth;
                return Wrap(
                  spacing: 16,
                  runSpacing: 18,
                  children: [
                    SizedBox(
                      width: width,
                      child: TextField(
                        key: const ValueKey('records-keyword'),
                        controller: _keyword,
                        enabled: !_loading,
                        onSubmitted: (_) => _search(),
                        decoration: const InputDecoration(
                          labelText: '关键词',
                          hintText: '搜索消息原文',
                          prefixIcon: Icon(Icons.search_rounded),
                        ),
                      ),
                    ),
                    SizedBox(
                      width: width,
                      child: DropdownButtonFormField<String>(
                        key: ValueKey('records-session-$_session'),
                        initialValue: _session ?? '',
                        isExpanded: true,
                        decoration: const InputDecoration(labelText: '会话'),
                        items: [
                          const DropdownMenuItem(
                            value: '',
                            child: Text('全部会话'),
                          ),
                          ..._sessions.map(
                            (session) => DropdownMenuItem(
                              value: session.sessionId,
                              child: Text(
                                '${session.displayName} (${session.messages})',
                                overflow: TextOverflow.ellipsis,
                              ),
                            ),
                          ),
                        ],
                        onChanged: _loading
                            ? null
                            : (value) => setState(
                                () => _session = value == '' ? null : value,
                              ),
                      ),
                    ),
                    SizedBox(
                      width: width,
                      child: TextField(
                        key: const ValueKey('records-start'),
                        controller: _start,
                        enabled: !_loading,
                        decoration: const InputDecoration(
                          labelText: '开始日期（可选）',
                          hintText: 'YYYY-MM-DD',
                        ),
                      ),
                    ),
                    SizedBox(
                      width: width,
                      child: TextField(
                        key: const ValueKey('records-end'),
                        controller: _end,
                        enabled: !_loading,
                        decoration: const InputDecoration(
                          labelText: '结束日期（可选）',
                          hintText: 'YYYY-MM-DD',
                        ),
                      ),
                    ),
                  ],
                );
              },
            ),
            const SizedBox(height: 20),
            Wrap(
              spacing: 12,
              runSpacing: 8,
              children: [
                ElevatedButton.icon(
                  key: const ValueKey('records-search'),
                  onPressed: _loading ? null : _search,
                  icon: const Icon(Icons.search_rounded),
                  label: const Text('查询'),
                ),
                TextButton(
                  onPressed: _loading ? null : _reset,
                  child: const Text('清空筛选'),
                ),
              ],
            ),
            if (_loading) ...[
              const SizedBox(height: 16),
              const LinearProgressIndicator(),
            ],
            if (_error != null) ...[
              const SizedBox(height: 16),
              SelectableText(
                _error!,
                style: const TextStyle(color: WxAgentColors.danger),
              ),
            ],
          ],
        ),
      ),
      if (!_loading && _error == null && !_indexExists)
        SectionCard(
          title: '还没有建立索引',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('先导入 JSON 聊天记录，便可以在这里检索原文。'),
              const SizedBox(height: 12),
              TextButton.icon(
                onPressed: () => context.read<NavigationController>().goTo(
                  WxAgentPage.library,
                ),
                icon: const Icon(Icons.storage_rounded),
                label: const Text('前往索引库'),
              ),
            ],
          ),
        ),
      if (_page != null && _indexExists && _error == null)
        SectionCard(
          title: '查询结果',
          subtitle: '共 ${_page!.total} 条 · 每页 $_pageSize 条 · 按消息时间升序',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _pagination(),
              const SizedBox(height: 16),
              if (_page!.messages.isEmpty)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 24),
                  child: Text(
                    '没有符合条件的记录，可以调整关键词或日期范围。',
                    style: TextStyle(color: WxAgentColors.textSecondary),
                  ),
                )
              else
                ..._page!.messages.map((record) => _RecordTile(record: record)),
              if (_page!.messages.length > 5) ...[
                const SizedBox(height: 16),
                _pagination(),
              ],
            ],
          ),
        ),
    ],
  );

  Widget _pagination() {
    final page = _page!;
    final first = page.messages.isEmpty ? 0 : page.offset + 1;
    final last = page.offset + page.messages.length;
    return Wrap(
      spacing: 12,
      runSpacing: 8,
      crossAxisAlignment: WrapCrossAlignment.center,
      children: [
        Text(
          '显示 $first–$last / ${page.total}',
          style: const TextStyle(color: WxAgentColors.textSecondary),
        ),
        OutlinedButton(
          onPressed: _loading || page.offset == 0
              ? null
              : () => _query((page.offset - _pageSize).clamp(0, page.total)),
          child: const Text('上一页'),
        ),
        OutlinedButton(
          onPressed: _loading || last >= page.total
              ? null
              : () => _query(page.offset + _pageSize),
          child: const Text('下一页'),
        ),
      ],
    );
  }
}

class _RecordTile extends StatelessWidget {
  const _RecordTile({required this.record});
  final ChatRecord record;
  @override
  Widget build(BuildContext context) {
    final sender = record.isSend
        ? '我'
        : record.senderName.isNotEmpty
        ? record.senderName
        : record.sender.isNotEmpty
        ? record.sender
        : '未知发送者';
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 18),
      decoration: const BoxDecoration(
        border: Border(top: BorderSide(color: WxAgentColors.border)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(
            spacing: 12,
            runSpacing: 6,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              Text(
                record.displayName.isEmpty
                    ? record.sessionId
                    : record.displayName,
                style: const TextStyle(
                  color: WxAgentColors.primary,
                  fontWeight: FontWeight.w600,
                ),
              ),
              Text(sender, style: const TextStyle(fontWeight: FontWeight.w600)),
              Text(
                record.formattedTime,
                style: const TextStyle(
                  fontSize: 12,
                  color: WxAgentColors.textSecondary,
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          SelectableText(
            record.content.isEmpty
                ? '[${record.type.isEmpty ? '非文本消息' : record.type}]'
                : record.content,
            style: const TextStyle(height: 1.75),
          ),
          const SizedBox(height: 8),
          SelectableText(
            '${record.sessionId} · #${record.localId}${record.type.isEmpty ? '' : ' · ${record.type}'}',
            style: const TextStyle(
              fontSize: 11,
              color: WxAgentColors.textSecondary,
            ),
          ),
        ],
      ),
    );
  }
}
