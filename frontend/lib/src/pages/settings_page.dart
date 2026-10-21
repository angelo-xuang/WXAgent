import 'package:flutter/material.dart';

import '../app.dart';
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class SettingsPage extends StatefulWidget {
  const SettingsPage({super.key, required this.controller});

  final AppController controller;

  @override
  State<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends State<SettingsPage> {
  final TextEditingController _llmBaseCtrl = TextEditingController();
  final TextEditingController _llmModelCtrl = TextEditingController();
  final TextEditingController _llmKeyCtrl = TextEditingController();

  static const double _fixedTemperature = 0.7;
  bool _loading = false;
  bool _savingLlm = false;
  String? _configPath;
  String? _message;

  @override
  void initState() {
    super.initState();
    _loadConfig();
  }

  @override
  void dispose() {
    _llmBaseCtrl.dispose();
    _llmModelCtrl.dispose();
    _llmKeyCtrl.dispose();
    super.dispose();
  }

  Future<void> _loadConfig() async {
    _safeSetState(() {
      _loading = true;
      _message = null;
    });
    try {
      final response = await widget.controller.api.fetchConfig();
      final data =
          (response['data'] as Map?)?.cast<String, dynamic>() ?? const {};
      final llm = data['llm'] as Map? ?? const {};
      _safeSetState(() {
        _configPath = response['path'] as String?;
        _llmBaseCtrl.text = llm['base_url'] as String? ?? '';
        _llmModelCtrl.text = llm['model'] as String? ?? '';
        _llmKeyCtrl.text = llm['api_key'] as String? ?? '';
      });
    } catch (err) {
      _safeSetState(() {
        _message = err.toString();
      });
    } finally {
      _safeSetState(() {
        _loading = false;
      });
    }
  }

  Future<void> _updateLlm() async {
    _safeSetState(() => _savingLlm = true);
    try {
      await widget.controller.api.updateConfig({
        'llm': {
          'base_url': _llmBaseCtrl.text,
          'model': _llmModelCtrl.text,
          'api_key': _llmKeyCtrl.text,
          'temperature': _fixedTemperature,
        },
      });
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('LLM 设置已保存')));
    } catch (err) {
      _safeSetState(() => _message = err.toString());
    } finally {
      _safeSetState(() => _savingLlm = false);
    }
  }

  Future<void> _testLlm() async {
    _safeSetState(() {
      _savingLlm = true;
      _message = null;
    });
    try {
      final result = await widget.controller.api.testLlm(
        baseUrl: _llmBaseCtrl.text,
        model: _llmModelCtrl.text,
        apiKey: _llmKeyCtrl.text,
      );
      _safeSetState(() {
        _message =
            result['message'] as String? ??
            (result['success'] == true ? 'LLM 接口可用' : '测试失败');
      });
    } catch (err) {
      _safeSetState(() => _message = err.toString());
    } finally {
      _safeSetState(() => _savingLlm = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return PageContainer(
      title: '设置',
      subtitle: '配置大模型参数，所有配置写入 config.json（${_configPath ?? '--'}）。',
      children: [
        if (_loading)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 8),
            child: LinearProgressIndicator(),
          ),
        SectionCard(
          title: '大模型 API',
          actions: [
            TextButton.icon(
              onPressed: _loading || _savingLlm ? null : _testLlm,
              icon: const Icon(Icons.bolt),
              label: const Text('测试连接'),
            ),
          ],
          child: Column(
            children: [
              TextField(
                controller: _llmBaseCtrl,
                decoration: const InputDecoration(labelText: 'Base URL'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _llmModelCtrl,
                decoration: const InputDecoration(labelText: '模型名称'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _llmKeyCtrl,
                decoration: const InputDecoration(labelText: 'API Key'),
                obscureText: true,
              ),
              const SizedBox(height: 12),
              Align(
                alignment: Alignment.centerLeft,
                child: Text(
                  'Temperature 固定为 $_fixedTemperature，用于保证输出稳定。',
                  style: TextStyle(color: Colors.grey.shade700),
                ),
              ),
              Align(
                alignment: Alignment.centerRight,
                child: ElevatedButton(
                  onPressed: _loading || _savingLlm ? null : _updateLlm,
                  child: _savingLlm
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            valueColor: AlwaysStoppedAnimation(Colors.white),
                          ),
                        )
                      : const Text('保存 LLM 设置'),
                ),
              ),
              if (_message != null) ...[
                const SizedBox(height: 12),
                Text(
                  _message!,
                  style: TextStyle(
                    color: _message!.contains('可用')
                        ? Colors.green
                        : Colors.redAccent,
                  ),
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }

  void _safeSetState(VoidCallback fn) {
    if (!mounted) return;
    setState(fn);
  }
}
