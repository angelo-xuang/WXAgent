import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../app.dart';
import '../models/models.dart';
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class KeyAcquisitionPage extends StatefulWidget {
  const KeyAcquisitionPage({super.key, required this.controller});

  final AppController controller;

  @override
  State<KeyAcquisitionPage> createState() => _KeyAcquisitionPageState();
}

class _KeyAcquisitionPageState extends State<KeyAcquisitionPage> {
  bool _loading = false;
  bool _statusLoading = false;
  String? _error;
  KeyInfo? _keyInfo;
  bool _nativeWechat = defaultTargetPlatform == TargetPlatform.windows;

  @override
  void initState() {
    super.initState();
    _loadStatus();
  }

  Future<void> _loadStatus() async {
    _safeSetState(() {
      _statusLoading = true;
      _error = null;
    });
    try {
      final status = await widget.controller.api.getStatus();
      final keyData =
          (status['key'] as Map?)?.cast<String, dynamic>() ?? const {};
      _safeSetState(() {
        _keyInfo = KeyInfo.fromJson({
          'db_key': keyData['db_key'],
          'timestamp': keyData['timestamp'],
          'raw': keyData,
        });
        _nativeWechat =
            (status['capabilities'] as Map?)?['native_wechat'] as bool? ??
            (defaultTargetPlatform == TargetPlatform.windows);
      });
    } catch (err) {
      _safeSetState(() {
        _error = err.toString();
      });
    } finally {
      _safeSetState(() {
        _statusLoading = false;
      });
    }
  }

  Future<void> _refreshKey() async {
    _safeSetState(() {
      _loading = true;
      _error = null;
    });
    try {
      final info = await widget.controller.api.refreshKey(autoLaunch: true);
      _safeSetState(() {
        _keyInfo = info;
      });
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('密钥读取成功')));
    } catch (err) {
      _safeSetState(() {
        _error = err.toString();
      });
    } finally {
      _safeSetState(() {
        _loading = false;
      });
    }
  }

  Future<void> _copyKey() async {
    final key = _keyInfo?.dbKey;
    if (key == null || key.isEmpty) return;
    await Clipboard.setData(ClipboardData(text: key));
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('已复制数据库密钥')));
  }

  void _safeSetState(VoidCallback update) {
    if (mounted) setState(update);
  }

  @override
  Widget build(BuildContext context) {
    return PageContainer(
      title: '密钥获取',
      subtitle: 'WXAgent 通过注入方式本地读取微信数据库密钥，仅在你的设备上使用，不上传网络。',
      children: [
        SectionCard(
          title: '密钥读取说明',
          subtitle: '确保微信处于登录状态并打开桌面客户端，点击下方按钮即可自动注入与读取密钥。',
          actions: [
            TextButton.icon(
              onPressed: _statusLoading ? null : _loadStatus,
              icon: _statusLoading
                  ? const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.refresh_rounded),
              label: const Text('重新检测状态'),
            ),
          ],
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                '· WXAgent 会自动写入 shared_preferences.json，并解析出数据库密钥及图片密钥。',
              ),
              if (!_nativeWechat) ...[
                const SizedBox(height: 8),
                const Text('当前后端不支持本地密钥读取。可连接 Windows 后端，或使用已导出的聊天记录。'),
              ],
              const SizedBox(height: 8),
              Wrap(
                spacing: 12,
                runSpacing: 8,
                children: [
                  ElevatedButton.icon(
                    onPressed: _loading || !_nativeWechat ? null : _refreshKey,
                    icon: _loading
                        ? const SizedBox(
                            width: 18,
                            height: 18,
                            child: CircularProgressIndicator(
                              strokeWidth: 2,
                              valueColor: AlwaysStoppedAnimation(Colors.white),
                            ),
                          )
                        : const Icon(Icons.vpn_key_rounded),
                    label: const Text('开始获取密钥'),
                  ),
                ],
              ),
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!, style: const TextStyle(color: Colors.redAccent)),
              ],
            ],
          ),
        ),
        SectionCard(
          title: '密钥读取结果',
          subtitle: '复制密钥后可在其它模块使用，若读取失败请确认微信客户端处于登录状态。',
          actions: [
            TextButton.icon(
              onPressed: _keyInfo?.dbKey == null ? null : _copyKey,
              icon: const Icon(Icons.copy_all_rounded),
              label: const Text('复制密钥'),
            ),
          ],
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: Colors.grey.shade50,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: Colors.grey.shade200),
                ),
                child: SelectableText(
                  _keyInfo?.dbKey ?? '尚未读取到密钥',
                  style: const TextStyle(fontFamily: 'monospace', fontSize: 16),
                ),
              ),
              const SizedBox(height: 16),
              Wrap(
                spacing: 16,
                runSpacing: 12,
                children: [
                  _buildKeyTag('图片 XOR Key', _keyInfo?.imageXorKey?.toString()),
                  _buildKeyTag('图片 AES Key', _keyInfo?.imageAesKey),
                  _buildKeyTag('原始记录条目', '${_keyInfo?.raw.length ?? 0}'),
                ],
              ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildKeyTag(String label, String? value) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      decoration: BoxDecoration(
        color: Colors.grey.shade100,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: TextStyle(color: Colors.grey.shade600, fontSize: 12),
          ),
          const SizedBox(height: 4),
          Text(
            value == null || value.isEmpty ? '--' : value,
            style: const TextStyle(fontFamily: 'monospace'),
          ),
        ],
      ),
    );
  }
}
