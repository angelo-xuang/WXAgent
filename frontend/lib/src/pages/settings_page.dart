import 'package:flutter/material.dart';

import '../app.dart';
import '../theme.dart';
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class SettingsPage extends StatefulWidget {
  const SettingsPage({super.key, required this.controller});
  final AppController controller;
  @override
  State<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends State<SettingsPage> {
  final _address = TextEditingController();
  final _database = TextEditingController();
  final _export = TextEditingController();
  bool _loading = true;
  bool _saving = false;
  String? _error;
  String? _message;

  @override
  void initState() {
    super.initState();
    _address.text = widget.controller.baseUrl;
    _load();
  }

  @override
  void dispose() {
    _address.dispose();
    _database.dispose();
    _export.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final response = await widget.controller.api.fetchConfig();
      if (!mounted) return;
      final data = response['data'] as Map? ?? {};
      _database.text = data['database_path']?.toString() ?? '';
      _export.text = data['export_dir']?.toString() ?? '';
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _connect() async {
    final address = _address.text.trim();
    final uri = Uri.tryParse(address);
    if (uri == null ||
        !const ['http', 'https'].contains(uri.scheme) ||
        uri.host.isEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        uri.userInfo.isNotEmpty ||
        (uri.path.isNotEmpty && uri.path != '/')) {
      setState(() => _error = '请填写有效的后端地址，例如 http://127.0.0.1:8000。');
      return;
    }
    widget.controller.setBaseUrl(address);
    await widget.controller.ensureBackendReady();
    if (!mounted) return;
    await _load();
  }

  Future<void> _save() async {
    if (_database.text.trim().isEmpty || _export.text.trim().isEmpty) {
      setState(() => _error = '数据库位置和导出目录不能为空。');
      return;
    }
    setState(() {
      _saving = true;
      _error = null;
      _message = null;
    });
    try {
      await widget.controller.api.updateConfig({
        'database_path': _database.text.trim(),
        'export_dir': _export.text.trim(),
      });
      widget.controller.notifyDataChanged();
      if (mounted) setState(() => _message = '已保存。索引库和聊天记录页面可刷新查看当前数据库。');
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) => PageContainer(
    title: '设置',
    subtitle: '连接后端服务，选择用于导入和检索的本地数据库。',
    children: [
      SectionCard(
        title: '后端连接',
        subtitle: '先运行 wxagent serve，再连接该服务。地址仅用于当前界面会话。',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              key: const ValueKey('settings-address'),
              controller: _address,
              enabled: !_loading && !_saving,
              decoration: const InputDecoration(
                labelText: '服务地址',
                hintText: 'http://127.0.0.1:8000',
              ),
            ),
            const SizedBox(height: 16),
            OutlinedButton.icon(
              onPressed: _loading || _saving ? null : _connect,
              icon: const Icon(Icons.link_rounded),
              label: const Text('连接服务'),
            ),
          ],
        ),
      ),
      SectionCard(
        title: '存储位置',
        subtitle: '路径位于后端电脑。更换数据库位置会切换索引库，不会搬迁或删除已有数据库。',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (_loading) const LinearProgressIndicator(),
            const SizedBox(height: 12),
            TextField(
              key: const ValueKey('settings-database'),
              controller: _database,
              enabled: !_loading && !_saving,
              decoration: const InputDecoration(labelText: 'SQLite 数据库文件'),
            ),
            const SizedBox(height: 18),
            TextField(
              key: const ValueKey('settings-export'),
              controller: _export,
              enabled: !_loading && !_saving,
              decoration: const InputDecoration(labelText: '默认 JSON 导出目录'),
            ),
            const SizedBox(height: 18),
            Wrap(
              spacing: 12,
              runSpacing: 8,
              children: [
                ElevatedButton.icon(
                  onPressed: _loading || _saving ? null : _save,
                  icon: const Icon(Icons.save_outlined),
                  label: Text(_saving ? '保存中…' : '保存设置'),
                ),
                TextButton(
                  onPressed: _loading || _saving ? null : _load,
                  child: const Text('重新读取'),
                ),
              ],
            ),
            if (_message != null) ...[
              const SizedBox(height: 16),
              Text(
                _message!,
                style: const TextStyle(color: WxAgentColors.success),
              ),
            ],
          ],
        ),
      ),
      if (_error != null)
        SelectableText(
          _error!,
          style: const TextStyle(color: WxAgentColors.danger),
        ),
    ],
  );
}
