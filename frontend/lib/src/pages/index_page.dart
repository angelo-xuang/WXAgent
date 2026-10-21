import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app.dart';
import '../theme.dart';
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class IndexPage extends StatefulWidget {
  const IndexPage({super.key, required this.controller});
  final AppController controller;
  @override
  State<IndexPage> createState() => _IndexPageState();
}

class _IndexPageState extends State<IndexPage> {
  final _directory = TextEditingController();
  Map<String, dynamic>? _status;
  Map<String, dynamic>? _result;
  bool _loading = true;
  bool _importing = false;
  String? _error;

  int _seenRevision = 0;

  @override
  void didUpdateWidget(covariant IndexPage oldWidget) {
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
    _directory.dispose();
    super.dispose();
  }

  Future<void> _load({bool loadConfig = true}) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final status = await widget.controller.api.indexStatus();
      final config = loadConfig
          ? await widget.controller.api.fetchConfig()
          : null;
      if (!mounted) return;
      setState(() {
        _status = status;
        if (config != null) {
          _directory.text =
              (config['data'] as Map?)?['export_dir']?.toString() ?? '';
        }
      });
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _browse() async {
    final selected = await getDirectoryPath(confirmButtonText: '选择导出目录');
    if (mounted && selected != null) _directory.text = selected;
  }

  Future<void> _import() async {
    if (_importing) return;
    final path = _directory.text.trim();
    if (path.isEmpty) {
      setState(() => _error = '请填写后端电脑上的 JSON 导出目录。');
      return;
    }
    setState(() {
      _importing = true;
      _error = null;
      _result = null;
    });
    try {
      final result = await widget.controller.api.importIndex(exportDir: path);
      if (!mounted) return;
      setState(() => _result = result);
      _seenRevision = widget.controller.dataRevision + 1;
      widget.controller.notifyDataChanged();
      await _load(loadConfig: false);
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _importing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final local = const [
      '127.0.0.1',
      'localhost',
      '::1',
    ].contains(Uri.tryParse(widget.controller.baseUrl)?.host);
    final exists = _status?['exists'] == true;
    return PageContainer(
      title: '索引库',
      subtitle: '原始记录留在导出目录，索引保存在本地数据库中。增量导入会去重并保留已有历史。',
      children: [
        SectionCard(
          title: exists ? '数据库已就绪' : '建立你的记录索引',
          subtitle: exists ? '以下数量来自完整索引库。' : '选择已有聊天 JSON 目录，首次导入后即可检索。',
          actions: [
            TextButton.icon(
              onPressed: _loading || _importing
                  ? null
                  : () => _load(loadConfig: false),
              icon: const Icon(Icons.refresh_rounded),
              label: const Text('刷新'),
            ),
          ],
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (_loading) const LinearProgressIndicator(),
              const SizedBox(height: 10),
              Wrap(
                spacing: 40,
                runSpacing: 20,
                children: [
                  _Count(
                    label: '聊天记录',
                    value: _status?['messages']?.toString() ?? '—',
                  ),
                  _Count(
                    label: '会话',
                    value: _status?['sessions']?.toString() ?? '—',
                  ),
                  _Count(
                    label: '来源文件',
                    value: _status?['sources']?.toString() ?? '—',
                  ),
                ],
              ),
              const SizedBox(height: 24),
              const Text(
                '数据库位置',
                style: TextStyle(
                  color: WxAgentColors.textSecondary,
                  fontSize: 12,
                ),
              ),
              const SizedBox(height: 6),
              SelectableText(_status?['database_path']?.toString() ?? '连接后显示'),
            ],
          ),
        ),
        SectionCard(
          title: '导入 JSON 记录',
          subtitle: '支持 WXAgent / EchoTrace 导出文件。路径以当前连接的后端电脑为准。',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              TextField(
                key: const ValueKey('index-directory'),
                controller: _directory,
                enabled: !_importing,
                decoration: InputDecoration(
                  labelText: '聊天导出目录',
                  suffixIcon: local
                      ? IconButton(
                          tooltip: '选择目录',
                          onPressed: _importing ? null : _browse,
                          icon: const Icon(Icons.folder_open_rounded),
                        )
                      : null,
                ),
              ),
              const SizedBox(height: 18),
              Wrap(
                spacing: 12,
                runSpacing: 10,
                children: [
                  ElevatedButton.icon(
                    key: const ValueKey('index-import'),
                    onPressed: _loading || _importing ? null : _import,
                    icon: _importing
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.input_rounded),
                    label: Text(_importing ? '正在导入…' : '增量导入'),
                  ),
                  OutlinedButton.icon(
                    onPressed: () => context.read<NavigationController>().goTo(
                      WxAgentPage.records,
                    ),
                    icon: const Icon(Icons.search_rounded),
                    label: const Text('检索记录'),
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
                  '导入完成：扫描 ${_result!['files']} 个文件，更新 ${_result!['changed_files']} 个，跳过 ${_result!['unchanged_files']} 个未变化文件。',
                  style: const TextStyle(
                    color: WxAgentColors.success,
                    height: 1.7,
                  ),
                ),
                const SizedBox(height: 6),
                Text(
                  '索引库共 ${_result!['messages']} 条记录、${_result!['sessions']} 个会话。',
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }
}

class _Count extends StatelessWidget {
  const _Count({required this.label, required this.value});
  final String label;
  final String value;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        value,
        style: const TextStyle(fontSize: 30, fontWeight: FontWeight.w700),
      ),
      const SizedBox(height: 5),
      Text(label, style: const TextStyle(color: WxAgentColors.textSecondary)),
    ],
  );
}
