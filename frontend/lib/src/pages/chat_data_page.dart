import 'dart:async';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app.dart';
import '../services/wx_agent_api.dart';
import '../theme.dart';
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class ChatDataPage extends StatefulWidget {
  const ChatDataPage({super.key, required this.controller});
  final AppController controller;
  @override
  State<ChatDataPage> createState() => _ChatDataPageState();
}

class _ChatDataPageState extends State<ChatDataPage> {
  final _wechatPath = TextEditingController();
  final _exportDir = TextEditingController();
  bool _loading = true;
  bool _running = false;
  bool _native = false;
  String? _error;
  String? _message;
  final _logs = <String>[];
  Map<String, dynamic>? _result;
  StreamSubscription<StreamEvent>? _subscription;
  int _runId = 0;

  int _seenRevision = 0;

  @override
  void didUpdateWidget(covariant ChatDataPage oldWidget) {
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
    _runId++;
    unawaited(_subscription?.cancel());
    _wechatPath.dispose();
    _exportDir.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final api = widget.controller.api;
      final values = await Future.wait([api.fetchConfig(), api.getStatus()]);
      if (!mounted) return;
      final config = values[0]['data'] as Map? ?? {};
      setState(() {
        _wechatPath.text = config['wechat_data_path']?.toString() ?? '';
        _exportDir.text = config['export_dir']?.toString() ?? '';
        _native = (values[1]['capabilities'] as Map?)?['native_wechat'] == true;
        if (values[1]['index_error'] != null) {
          _error = '索引库读取失败：${values[1]['index_error']}';
        }
      });
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _browse(TextEditingController target) async {
    final path = await getDirectoryPath(confirmButtonText: '选择目录');
    if (mounted && path != null) target.text = path;
  }

  Future<void> _save() async {
    setState(() {
      _loading = true;
      _error = null;
      _message = null;
    });
    try {
      await widget.controller.api.updateConfig({
        'wechat_data_path': _wechatPath.text.trim(),
        'export_dir': _exportDir.text.trim(),
      });
      if (mounted) setState(() => _message = '采集目录已保存。');
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _run() async {
    if (_running) return;
    final runId = ++_runId;
    await _subscription?.cancel();
    if (!mounted || runId != _runId) return;
    await _save();
    if (!mounted || _error != null) return;
    setState(() {
      _running = true;
      _result = null;
      _message = null;
      _logs.clear();
    });
    _subscription = widget.controller.api
        .runFullRefreshStream(autoLaunchKey: true)
        .listen(
          (event) {
            if (!mounted || runId != _runId) return;
            setState(() {
              if (event.type == 'log') _logs.add(event.content);
              if (event.type == 'error') {
                _error = event.content.isNotEmpty
                    ? event.content
                    : '采集失败，请查看日志。';
                _running = false;
              }
              if (event.type == 'result') {
                _result = event.payload ?? {};
                _running = false;
              }
            });
            if (event.type == 'result') {
              _seenRevision = widget.controller.dataRevision + 1;
              widget.controller.notifyDataChanged();
            }
          },
          onError: (Object error) {
            if (mounted && runId == _runId) {
              setState(() {
                _error = error.toString();
                _running = false;
              });
            }
          },
          onDone: () {
            if (mounted && runId == _runId) setState(() => _running = false);
          },
        );
  }

  @override
  Widget build(BuildContext context) {
    final local = const [
      '127.0.0.1',
      'localhost',
      '::1',
    ].contains(Uri.tryParse(widget.controller.baseUrl)?.host);
    return PageContainer(
      title: '微信采集',
      subtitle: 'Windows 后端可读取微信本地记录，导出 JSON 并自动导入索引库。已有文件可直接前往“索引库”导入。',
      children: [
        SectionCard(
          title: '采集目录',
          subtitle: '填写后端电脑上的路径。微信数据目录可从微信设置中的存储位置找到。',
          actions: [
            TextButton.icon(
              onPressed: _loading || _running ? null : _load,
              icon: const Icon(Icons.refresh_rounded),
              label: const Text('重新读取'),
            ),
          ],
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (_loading) const LinearProgressIndicator(),
              const SizedBox(height: 12),
              TextField(
                controller: _wechatPath,
                enabled: !_loading && !_running,
                decoration: InputDecoration(
                  labelText: '微信数据目录',
                  suffixIcon: local
                      ? IconButton(
                          tooltip: '选择微信目录',
                          onPressed: _loading || _running
                              ? null
                              : () => _browse(_wechatPath),
                          icon: const Icon(Icons.folder_open_rounded),
                        )
                      : null,
                ),
              ),
              const SizedBox(height: 18),
              TextField(
                controller: _exportDir,
                enabled: !_loading && !_running,
                decoration: InputDecoration(
                  labelText: 'JSON 导出目录',
                  suffixIcon: local
                      ? IconButton(
                          tooltip: '选择导出目录',
                          onPressed: _loading || _running
                              ? null
                              : () => _browse(_exportDir),
                          icon: const Icon(Icons.folder_open_rounded),
                        )
                      : null,
                ),
              ),
              const SizedBox(height: 18),
              OutlinedButton.icon(
                onPressed: _loading || _running ? null : _save,
                icon: const Icon(Icons.save_outlined),
                label: const Text('保存目录'),
              ),
              if (_message != null) ...[
                const SizedBox(height: 12),
                Text(
                  _message!,
                  style: const TextStyle(color: WxAgentColors.success),
                ),
              ],
            ],
          ),
        ),
        SectionCard(
          title: '采集并建立索引',
          subtitle: '请确保微信已登录。执行时会连接微信、导出记录，然后更新本地数据库。',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (!_native && !_loading) ...[
                const Text(
                  '当前后端不支持本地微信采集。可连接 Windows 后端，或导入已有的 JSON 文件。',
                  style: TextStyle(height: 1.7),
                ),
                const SizedBox(height: 16),
              ],
              Wrap(
                spacing: 12,
                runSpacing: 10,
                children: [
                  ElevatedButton.icon(
                    onPressed: _loading || _running || !_native ? null : _run,
                    icon: const Icon(Icons.download_rounded),
                    label: Text(_running ? '正在采集…' : '采集并更新索引'),
                  ),
                  TextButton.icon(
                    onPressed: () => context.read<NavigationController>().goTo(
                      WxAgentPage.library,
                    ),
                    icon: const Icon(Icons.storage_rounded),
                    label: const Text('查看索引库'),
                  ),
                ],
              ),
              if (_error != null) ...[
                const SizedBox(height: 16),
                SelectableText(
                  _error!,
                  style: const TextStyle(color: WxAgentColors.danger),
                ),
              ],
              if (_result != null) ...[
                const SizedBox(height: 18),
                Text(
                  '采集完成。索引库现有 ${_result!['messages'] ?? 0} 条记录、${_result!['sessions'] ?? 0} 个会话。',
                  style: const TextStyle(
                    color: WxAgentColors.success,
                    height: 1.7,
                  ),
                ),
              ],
              if (_logs.isNotEmpty || _running) ...[
                const SizedBox(height: 18),
                if (_running) const LinearProgressIndicator(),
                const SizedBox(height: 8),
                Container(
                  width: double.infinity,
                  constraints: const BoxConstraints(maxHeight: 280),
                  padding: const EdgeInsets.all(16),
                  decoration: BoxDecoration(
                    color: WxAgentColors.background,
                    borderRadius: BorderRadius.circular(10),
                  ),
                  child: SingleChildScrollView(
                    child: SelectableText(
                      _logs.isEmpty ? '正在连接微信…' : _logs.join('\n'),
                      style: const TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 12,
                        height: 1.6,
                      ),
                    ),
                  ),
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }
}
