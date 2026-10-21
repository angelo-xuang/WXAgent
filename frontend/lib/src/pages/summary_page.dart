import 'package:flutter/material.dart';
import 'dart:async';

import 'package:flutter/rendering.dart' show ScrollDirection;
import 'package:flutter/services.dart';
import 'package:intl/intl.dart';
import 'package:url_launcher/url_launcher.dart';

import '../app.dart';
import '../models/models.dart';
import '../services/wx_agent_api.dart' show StreamEvent;
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class SummaryPage extends StatefulWidget {
  const SummaryPage({super.key, required this.controller});

  final AppController controller;

  @override
  State<SummaryPage> createState() => _SummaryPageState();
}

class _SummaryPageState extends State<SummaryPage>
    with AutomaticKeepAliveClientMixin {
  static const String _friendlyApiConfigError = 'api错误，请先到设置页面配置并保存好ai接口';
  static const String _defaultChunkPrompt = '''
请分别总结以下微信群聊天记录的主要内容和讨论话题。
注意：以下文本包含来自不同群聊的消息，每个群聊用【群聊：群名】的格式标记，请按群聊分别总结。

{{content}}

要求：
1. 用中文总结
2. 分聊天对象进行总结，同一个群聊或者同一个聊天记录放在一起总结。
3. 总结出主要话题和讨论内容（用编号列出，并附上时间和讨论人（如果必要））
4. 提取出重要信息（如招聘信息、活动信息等），并注意引用原文！
5. 概括参与者的主要观点或反应
6. 标出最活跃的话题和讨论热度''';

  static const String _defaultFinalPrompt = '''
{{intro}}

{{summaries}}

要求：
1. 按群聊/话题重新组织内容，去掉重复叙述，但要保留所有关键细节、时间、人物与数量。
2. 识别跨批次连续的讨论并合并，补全上下文。
3. 输出 Markdown，包含：概览、按群聊的详细总结、关键行动项/待办/风险。''';

  final TextEditingController _chunkPromptCtrl = TextEditingController();
  final TextEditingController _finalPromptCtrl = TextEditingController();
  final ScrollController _streamScrollCtrl = ScrollController();

  bool _useCustomPrompt = false;
  String? _startDate;
  String? _endDate;
  final Set<String> _selectedSessions = {};
  List<SessionMeta> _sessions = const [];
  bool _loadingSessions = false;

  bool _running = false;
  String? _resultContent;
  String? _summaryPath;
  String? _error;

  int? _totalMessages;
  int? _totalSessions;
  int? _chunkCount;
  String? _summaryMode;

  String _streamLog = '';
  String _streamAiOutput = '';
  int _currentStep = 0;
  int _totalSteps = 6;
  bool _userScrolling = false;
  StreamSubscription<StreamEvent>? _summarySub;
  bool _cancelled = false;
  Completer<void>? _summaryCompletion;
  int _summaryRunId = 0;
  int _historyRequestId = 0;

  List<SummaryHistoryEntry> _history = const [];
  SummaryHistoryEntry? _selectedHistory;
  bool _historyLoading = false;

  @override
  void initState() {
    super.initState();
    _chunkPromptCtrl.text = _defaultChunkPrompt;
    _finalPromptCtrl.text = _defaultFinalPrompt;
    _setDefaultDateRange();
    _loadSessions();
    _loadHistory();
  }

  @override
  void dispose() {
    _summaryRunId++;
    _cancelled = true;
    _finishSummary();
    unawaited(_summarySub?.cancel());
    _chunkPromptCtrl.dispose();
    _finalPromptCtrl.dispose();
    _streamScrollCtrl.dispose();
    super.dispose();
  }

  Future<void> _pickDate({required bool isStart}) async {
    final picked = await showDatePicker(
      context: context,
      initialDate: DateTime.now(),
      firstDate: DateTime(2015),
      lastDate: DateTime.now().add(const Duration(days: 1)),
    );
    if (!mounted || picked == null) return;
    final text = DateFormat('yyyy-MM-dd').format(picked);
    _safeSetState(() {
      if (isStart) {
        _startDate = text;
      } else {
        _endDate = text;
      }
    });
  }

  void _clearDate(bool isStart) {
    _safeSetState(() {
      if (isStart) {
        _startDate = null;
      } else {
        _endDate = null;
      }
    });
  }

  void _setDefaultDateRange() {
    if (_startDate != null || _endDate != null) return;
    final now = DateTime.now();
    final formatter = DateFormat('yyyy-MM-dd');
    _startDate = formatter.format(now.subtract(const Duration(days: 2)));
    _endDate = formatter.format(now);
  }

  Future<void> _loadSessions() async {
    _safeSetState(() => _loadingSessions = true);
    try {
      final sessions = await widget.controller.api.listSessions();
      _safeSetState(() => _sessions = sessions);
    } catch (error) {
      _safeSetState(() => _error = '无法加载会话：$error');
    } finally {
      if (mounted) {
        _safeSetState(() => _loadingSessions = false);
      }
    }
  }

  Future<void> _loadHistory() async {
    _safeSetState(() => _historyLoading = true);
    try {
      final entries = await widget.controller.api.listSummaryHistory();
      // 去重后按修改时间倒序
      final dedup = <String, SummaryHistoryEntry>{};
      for (final item in entries) {
        dedup[item.path] = item;
      }
      final list = dedup.values.toList();
      list.sort((a, b) {
        final left = b.modified ?? '';
        final right = a.modified ?? '';
        return left.compareTo(right);
      });
      _safeSetState(() {
        _history = list;
        if (_selectedHistory == null && list.isNotEmpty) {
          _selectedHistory = list.first;
        } else if (_selectedHistory != null &&
            !_history.any((item) => item.path == _selectedHistory!.path)) {
          _selectedHistory = list.isNotEmpty ? list.first : null;
        }
      });
      if (mounted && _selectedHistory != null) {
        await _loadHistoryContent(_selectedHistory!);
      }
    } catch (error) {
      _safeSetState(() => _error = '无法加载历史总结：$error');
    } finally {
      if (mounted) {
        _safeSetState(() => _historyLoading = false);
      }
    }
  }

  Future<void> _loadHistoryContent(SummaryHistoryEntry entry) async {
    final requestId = ++_historyRequestId;
    _safeSetState(() => _historyLoading = true);
    try {
      final content = await widget.controller.api.readSummaryHistory(
        entry.path,
      );
      if (!mounted || requestId != _historyRequestId) return;
      _safeSetState(() {
        _resultContent = content;
        _summaryPath = entry.path;
        _selectedHistory = entry;
      });
    } catch (error) {
      if (requestId == _historyRequestId) {
        _safeSetState(() => _error = '无法读取历史总结：$error');
      }
    } finally {
      if (mounted) {
        _safeSetState(() => _historyLoading = false);
      }
    }
  }

  Future<void> _runSummary() async {
    if (_running) return;
    final runId = ++_summaryRunId;
    await _summarySub?.cancel();
    _summarySub = null;
    if (!mounted) return;

    if (_useCustomPrompt) {
      final chunk = _chunkPromptCtrl.text;
      final finalPrompt = _finalPromptCtrl.text;
      if (!chunk.contains('{{content}}')) {
        _safeSetState(() {
          _error = '分段提示词必须包含 {{content}} 占位符（用于放入分段内容）。';
        });
        return;
      }
      if (!finalPrompt.contains('{{intro}}') ||
          !finalPrompt.contains('{{summaries}}')) {
        _safeSetState(() {
          _error = '最终提示词必须同时包含 {{intro}} 与 {{summaries}} 占位符。';
        });
        return;
      }
    }

    // 如果 AI 接口未配置，直接给出更友好的提示，避免把底层 401/鉴权错误抛到 UI。
    _safeSetState(() => _running = true);
    final configured = await _ensureAiConfigured();
    if (!mounted || runId != _summaryRunId) return;
    if (!configured) {
      _safeSetState(() => _running = false);
      return;
    }

    _safeSetState(() {
      _running = true;
      _cancelled = false;
      _error = null;
      _totalMessages = null;
      _totalSessions = null;
      _chunkCount = null;
      _summaryMode = null;
      _streamLog = '';
      _streamAiOutput = '';
      _currentStep = 0;
      _totalSteps = 6;
      _resultContent = null;
      _summaryPath = null;
      _userScrolling = false;
    });

    String finalContent = '';
    final completion = Completer<void>();
    _summaryCompletion = completion;

    try {
      final stream = widget.controller.api.runSummaryStream(
        startDate: _startDate,
        endDate: _endDate,
        sessions: _selectedSessions.isEmpty ? null : _selectedSessions.toList(),
        chunkPrompt: _useCustomPrompt ? _chunkPromptCtrl.text : null,
        finalPrompt: _useCustomPrompt ? _finalPromptCtrl.text : null,
      );

      _summarySub = stream.listen(
        (event) {
          if (!mounted || runId != _summaryRunId) return;
          switch (event.type) {
            case 'log':
              _safeSetState(() {
                if (event.content.isNotEmpty) {
                  _streamLog += '${event.content}\n';
                }
                _currentStep = event.step ?? _currentStep;
                _totalSteps = event.total ?? _totalSteps;
              });
              _scrollToBottom();
              break;
            case 'ai_chunk':
              _safeSetState(() {
                _streamAiOutput += event.content;
                _currentStep = event.step ?? _currentStep;
              });
              _scrollToBottom();
              break;
            case 'progress':
              _safeSetState(() {
                _currentStep = event.step ?? _currentStep;
                _totalSteps = event.total ?? _totalSteps;
              });
              break;
            case 'error':
              final raw = event.content.isNotEmpty
                  ? event.content
                  : event.payload?['message']?.toString();
              _safeSetState(() {
                _error = _formatError(raw);
              });
              _cancelled = true;
              _finishSummary();
              unawaited(_summarySub?.cancel());
              break;
            case 'result':
              _safeSetState(() {
                finalContent = event.content.isNotEmpty
                    ? event.content
                    : _streamAiOutput;
                final payload = event.payload ?? const {};
                _summaryPath =
                    payload['output_path'] as String? ?? _summaryPath;
                _totalMessages = _asInt(payload['total_messages']);
                _totalSessions = _asInt(payload['total_sessions']);
                _chunkCount = _asInt(payload['chunk_count']);
                _summaryMode = payload['mode'] as String? ?? _summaryMode;
              });
              break;
            default:
              break;
          }
        },
        onError: (Object err, StackTrace stack) {
          if (!completion.isCompleted) completion.completeError(err, stack);
        },
        onDone: () {
          if (!completion.isCompleted) completion.complete();
        },
        cancelOnError: true,
      );

      await completion.future;

      if (!_cancelled && mounted && runId == _summaryRunId && _error == null) {
        _safeSetState(() {
          _resultContent = finalContent.isNotEmpty
              ? finalContent
              : _streamAiOutput;
          _summaryMode = _summaryMode ?? 'merged';
        });

        await _loadHistory();
        if (mounted) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(const SnackBar(content: Text('总结完成')));
        }
      }
    } catch (err) {
      if (mounted && runId == _summaryRunId) {
        _safeSetState(() => _error = _formatError(err.toString()));
      }
    } finally {
      if (runId == _summaryRunId) {
        _summarySub = null;
        _summaryCompletion = null;
        _safeSetState(() => _running = false);
      }
    }
  }

  void _finishSummary() {
    final completion = _summaryCompletion;
    if (completion != null && !completion.isCompleted) completion.complete();
  }

  Future<void> _stopSummary() async {
    _summaryRunId++;
    _cancelled = true;
    _finishSummary();
    await _summarySub?.cancel();
    _summarySub = null;
    if (mounted) {
      _safeSetState(() => _running = false);
    }
  }

  Future<bool> _ensureAiConfigured() async {
    try {
      final resp = await widget.controller.api.fetchConfig();
      final data = (resp['data'] as Map?)?.cast<String, dynamic>() ?? const {};
      final llm = (data['llm'] as Map?)?.cast<String, dynamic>() ?? const {};
      final apiKey = (llm['api_key'] as String?)?.trim() ?? '';
      final baseUrl = (llm['base_url'] as String?)?.trim() ?? '';

      // 本地 OpenAI-compat 服务（如 localhost）可能无需 key；其它情况未配置 key 直接提示。
      if (apiKey.isEmpty && !_isLocalBaseUrl(baseUrl)) {
        if (!mounted) return false;
        _safeSetState(() => _error = _friendlyApiConfigError);
        return false;
      }
    } catch (_) {
      // 配置读取失败时不阻断流程，让后续请求自然报错并由 _formatError 兜底。
    }
    return true;
  }

  bool _isLocalBaseUrl(String baseUrl) {
    if (baseUrl.trim().isEmpty) return false;
    final uri = Uri.tryParse(baseUrl.trim());
    if (uri == null) return false;
    final host = uri.host.toLowerCase();
    return host == 'localhost' ||
        host == '127.0.0.1' ||
        host == '0.0.0.0' ||
        host == '::1';
  }

  String _formatError(String? raw) {
    final msg = (raw ?? '').trim();
    if (msg.isEmpty) return msg;

    final lower = msg.toLowerCase();
    final looksLikeAuthError =
        lower.contains('status 401') ||
        lower.contains('status 403') ||
        lower.contains('unauthorized') ||
        lower.contains('authentication fails') ||
        lower.contains('invalid api key') ||
        (lower.contains('auth header') && lower.contains('bearer'));

    if (looksLikeAuthError) {
      return _friendlyApiConfigError;
    }

    return msg;
  }

  int? _asInt(dynamic value) {
    if (value is int) return value;
    if (value is double) return value.toInt();
    if (value is String) return int.tryParse(value);
    return null;
  }

  void _scrollToBottom() {
    if (_userScrolling) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && _streamScrollCtrl.hasClients) {
        _streamScrollCtrl.animateTo(
          _streamScrollCtrl.position.maxScrollExtent,
          duration: const Duration(milliseconds: 120),
          curve: Curves.easeOut,
        );
      }
    });
  }

  bool _isNearBottom() {
    if (!_streamScrollCtrl.hasClients) return true;
    final position = _streamScrollCtrl.position;
    return position.maxScrollExtent - position.pixels < 50;
  }

  Future<void> _selectSessions() async {
    if (_loadingSessions) return;
    final temp = Set<String>.from(_selectedSessions);
    await showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (context) {
        return DraggableScrollableSheet(
          expand: false,
          builder: (_, controller) {
            return StatefulBuilder(
              builder: (context, setModalState) {
                return Padding(
                  padding: const EdgeInsets.all(24),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Text(
                        'Select conversations',
                        style: TextStyle(
                          fontSize: 18,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const SizedBox(height: 12),
                      Expanded(
                        child: ListView.builder(
                          controller: controller,
                          itemCount: _sessions.length,
                          itemBuilder: (context, index) {
                            final session = _sessions[index];
                            final selected = temp.contains(session.sessionId);
                            return CheckboxListTile(
                              value: selected,
                              title: Text(session.displayName),
                              subtitle: Text(
                                session.category ?? session.sessionType ?? '--',
                              ),
                              onChanged: (value) {
                                setModalState(() {
                                  if (value == true) {
                                    temp.add(session.sessionId);
                                  } else {
                                    temp.remove(session.sessionId);
                                  }
                                });
                              },
                            );
                          },
                        ),
                      ),
                      const SizedBox(height: 12),
                      Row(
                        mainAxisAlignment: MainAxisAlignment.end,
                        children: [
                          TextButton(
                            onPressed: () => setModalState(temp.clear),
                            child: const Text('Clear'),
                          ),
                          const SizedBox(width: 12),
                          ElevatedButton(
                            onPressed: () {
                              _safeSetState(() {
                                _selectedSessions
                                  ..clear()
                                  ..addAll(temp);
                              });
                              Navigator.pop(context);
                            },
                            child: const Text('Apply'),
                          ),
                        ],
                      ),
                    ],
                  ),
                );
              },
            );
          },
        );
      },
    );
  }

  Future<void> _copyResult() async {
    final text = _resultContent;
    if (text == null || text.isEmpty) return;
    await Clipboard.setData(ClipboardData(text: text));
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('Copied to clipboard')));
  }

  Future<void> _openSummaryFile() async {
    if (_summaryPath == null) return;
    await launchUrl(Uri.file(_summaryPath!));
  }

  Widget _buildStatChip(String label, String value) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: Colors.blue.shade300),
      ),
      child: Text(
        '$label: $value',
        style: TextStyle(fontSize: 12, color: Colors.blue.shade700),
      ),
    );
  }

  @override
  bool get wantKeepAlive => true;

  void _safeSetState(VoidCallback update) {
    if (mounted) setState(update);
  }

  @override
  Widget build(BuildContext context) {
    super.build(context);
    SummaryHistoryEntry? validSelectedHistory;
    if (_selectedHistory != null) {
      for (final item in _history) {
        if (item.path == _selectedHistory!.path) {
          validSelectedHistory = item;
          break;
        }
      }
    }

    return PageContainer(
      title: '聊天记录 AI 总结',
      subtitle:
          '对导出的聊天记录生成 Markdown 报告，可使用默认提示词或自定义分段/最终提示词。在第一次使用本功能前，请先在设置页面配置好 AI 接口。',
      children: [
        SectionCard(
          title: '筛选与提示词',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              LayoutBuilder(
                builder: (context, constraints) {
                  final width = constraints.maxWidth < 560
                      ? constraints.maxWidth
                      : (constraints.maxWidth - 16) / 2;
                  return Wrap(
                    spacing: 16,
                    runSpacing: 16,
                    children: [
                      SizedBox(
                        width: width,
                        child: _DateField(
                          label: '开始日期',
                          value: _startDate,
                          onPick: () => _pickDate(isStart: true),
                          onClear: () => _clearDate(true),
                        ),
                      ),
                      SizedBox(
                        width: width,
                        child: _DateField(
                          label: '结束日期',
                          value: _endDate,
                          onPick: () => _pickDate(isStart: false),
                          onClear: () => _clearDate(false),
                        ),
                      ),
                    ],
                  );
                },
              ),
              const SizedBox(height: 16),
              Wrap(
                spacing: 12,
                runSpacing: 12,
                children: [
                  ElevatedButton.icon(
                    onPressed: _loadingSessions ? null : _selectSessions,
                    icon: _loadingSessions
                        ? const SizedBox(
                            width: 18,
                            height: 18,
                            child: CircularProgressIndicator(
                              strokeWidth: 2,
                              valueColor: AlwaysStoppedAnimation(Colors.white),
                            ),
                          )
                        : const Icon(Icons.checklist),
                    label: Text(
                      _selectedSessions.isEmpty
                          ? '选择需要总结的会话'
                          : '已选择 ${_selectedSessions.length} 个',
                    ),
                  ),
                  if (_selectedSessions.isNotEmpty)
                    TextButton(
                      onPressed: () =>
                          _safeSetState(() => _selectedSessions.clear()),
                      child: const Text('清空选择'),
                    ),
                ],
              ),
              const SizedBox(height: 8),
              Text(
                '不选择会话时将汇总全部可用聊天记录。默认使用近三天的聊天记录。',
                style: TextStyle(color: Colors.grey.shade600, fontSize: 12),
              ),
              const SizedBox(height: 16),
              const Text('提示词', style: TextStyle(fontWeight: FontWeight.w600)),
              RadioGroup<bool>(
                groupValue: _useCustomPrompt,
                onChanged: (value) =>
                    _safeSetState(() => _useCustomPrompt = value ?? false),
                child: const Column(
                  children: [
                    RadioListTile<bool>(value: false, title: Text('使用默认提示词')),
                    RadioListTile<bool>(value: true, title: Text('自定义提示词')),
                  ],
                ),
              ),
              AnimatedCrossFade(
                firstChild: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: const [
                    _PromptPreview(
                      title: '默认分段提示词',
                      content: _defaultChunkPrompt,
                    ),
                    SizedBox(height: 8),
                    _PromptPreview(
                      title: '默认最终提示词',
                      content: _defaultFinalPrompt,
                    ),
                  ],
                ),
                secondChild: Column(
                  children: [
                    TextField(
                      controller: _chunkPromptCtrl,
                      maxLines: 6,
                      decoration: const InputDecoration(
                        labelText: '分段提示词',
                        hintText:
                            '占位符：{{chunk_num}}、{{chunk_total}}、{{content}}',
                      ),
                    ),
                    const SizedBox(height: 12),
                    TextField(
                      controller: _finalPromptCtrl,
                      maxLines: 6,
                      decoration: const InputDecoration(
                        labelText: '最终提示词',
                        hintText:
                            '占位符：{{intro}}、{{summaries}}、{{summary_count}}',
                      ),
                    ),
                  ],
                ),
                crossFadeState: _useCustomPrompt
                    ? CrossFadeState.showSecond
                    : CrossFadeState.showFirst,
                duration: const Duration(milliseconds: 200),
              ),
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!, style: const TextStyle(color: Colors.redAccent)),
              ],
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  ElevatedButton.icon(
                    onPressed: _running ? null : _runSummary,
                    icon: _running
                        ? const SizedBox(
                            width: 18,
                            height: 18,
                            child: CircularProgressIndicator(
                              strokeWidth: 2,
                              valueColor: AlwaysStoppedAnimation(Colors.white),
                            ),
                          )
                        : const Icon(Icons.auto_awesome),
                    label: const Text('开始总结'),
                  ),
                  const SizedBox(width: 8),
                  OutlinedButton.icon(
                    onPressed: _running ? _stopSummary : null,
                    icon: const Icon(Icons.stop),
                    label: const Text('停止总结'),
                  ),
                ],
              ),
            ],
          ),
        ),
        SectionCard(
          title: '进度与历史',
          actions: [
            IconButton(
              tooltip: '刷新历史列表',
              onPressed: _historyLoading ? null : _loadHistory,
              icon: _historyLoading
                  ? const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.refresh),
            ),
            IconButton(
              tooltip: '复制结果',
              onPressed: _resultContent == null ? null : _copyResult,
              icon: const Icon(Icons.copy_all),
            ),
            IconButton(
              tooltip: '打开文件',
              onPressed: _summaryPath == null ? null : _openSummaryFile,
              icon: const Icon(Icons.folder_open),
            ),
          ],
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DropdownButtonFormField<SummaryHistoryEntry>(
                initialValue: validSelectedHistory,
                key: ValueKey(validSelectedHistory?.path),
                isExpanded: true,
                decoration: const InputDecoration(labelText: '历史总结'),
                items: _history
                    .map(
                      (entry) => DropdownMenuItem(
                        value: entry,
                        child: Text(
                          entry.name,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    )
                    .toList(),
                onChanged: (entry) {
                  if (entry != null) {
                    _loadHistoryContent(entry);
                  }
                },
              ),
              if (_totalMessages != null ||
                  _totalSessions != null ||
                  _chunkCount != null ||
                  _summaryMode != null) ...[
                const SizedBox(height: 12),
                Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: Colors.blue.shade50,
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(color: Colors.blue.shade200),
                  ),
                  child: Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    crossAxisAlignment: WrapCrossAlignment.center,
                    children: [
                      const Icon(Icons.analytics, size: 20, color: Colors.blue),
                      const SizedBox(width: 8),
                      Text(
                        '统计',
                        style: TextStyle(
                          fontWeight: FontWeight.w600,
                          color: Colors.blue.shade800,
                        ),
                      ),
                      if (_totalMessages != null) ...[
                        const SizedBox(width: 8),
                        _buildStatChip('消息数', '$_totalMessages'),
                      ],
                      if (_totalSessions != null) ...[
                        const SizedBox(width: 8),
                        _buildStatChip('会话数', '$_totalSessions'),
                      ],
                      if (_chunkCount != null && _chunkCount! > 0) ...[
                        const SizedBox(width: 8),
                        _buildStatChip('分段', '$_chunkCount'),
                      ],
                      if (_summaryMode != null) ...[
                        const SizedBox(width: 8),
                        _buildStatChip(
                          '模式',
                          _summaryMode == 'merged' ? '跨会话合并' : '逐会话',
                        ),
                      ],
                    ],
                  ),
                ),
              ],
              if (_running ||
                  _streamLog.isNotEmpty ||
                  _streamAiOutput.isNotEmpty) ...[
                const SizedBox(height: 12),
                Container(
                  decoration: BoxDecoration(
                    color: Colors.grey.shade900,
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(color: Colors.grey.shade700),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 12,
                          vertical: 8,
                        ),
                        decoration: BoxDecoration(
                          color: Colors.grey.shade800,
                          borderRadius: const BorderRadius.only(
                            topLeft: Radius.circular(7),
                            topRight: Radius.circular(7),
                          ),
                        ),
                        child: Row(
                          children: [
                            if (_running) ...[
                              const SizedBox(
                                width: 12,
                                height: 12,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                  color: Colors.green,
                                ),
                              ),
                              const SizedBox(width: 8),
                            ] else ...[
                              const Icon(
                                Icons.check_circle,
                                size: 14,
                                color: Colors.green,
                              ),
                              const SizedBox(width: 8),
                            ],
                            Text(
                              _running
                                  ? '处理中... ($_currentStep/$_totalSteps)'
                                  : '已完成',
                              style: const TextStyle(
                                color: Colors.white,
                                fontWeight: FontWeight.w500,
                                fontSize: 13,
                              ),
                            ),
                            const Spacer(),
                            if (_running)
                              TextButton.icon(
                                onPressed: _stopSummary,
                                style: TextButton.styleFrom(
                                  foregroundColor: Colors.orangeAccent,
                                ),
                                icon: const Icon(Icons.stop_circle, size: 16),
                                label: const Text('停止'),
                              ),
                            if (!_running &&
                                (_streamLog.isNotEmpty ||
                                    _streamAiOutput.isNotEmpty))
                              IconButton(
                                icon: const Icon(
                                  Icons.clear,
                                  size: 16,
                                  color: Colors.grey,
                                ),
                                padding: EdgeInsets.zero,
                                constraints: const BoxConstraints(),
                                tooltip: '清除日志',
                                onPressed: () {
                                  _safeSetState(() {
                                    _streamLog = '';
                                    _streamAiOutput = '';
                                  });
                                },
                              ),
                          ],
                        ),
                      ),
                      Stack(
                        children: [
                          Container(
                            height: 200,
                            padding: const EdgeInsets.all(12),
                            child: NotificationListener<ScrollNotification>(
                              onNotification: (notification) {
                                if (notification is UserScrollNotification &&
                                    notification.direction !=
                                        ScrollDirection.idle) {
                                  _safeSetState(() => _userScrolling = true);
                                } else if (notification
                                    is ScrollEndNotification) {
                                  if (_isNearBottom()) {
                                    _safeSetState(() => _userScrolling = false);
                                  }
                                }
                                return false;
                              },
                              child: SingleChildScrollView(
                                controller: _streamScrollCtrl,
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    if (_streamLog.isNotEmpty)
                                      SelectableText(
                                        _streamLog,
                                        style: TextStyle(
                                          fontFamily: 'monospace',
                                          fontSize: 11,
                                          color: Colors.grey.shade400,
                                        ),
                                      ),
                                    if (_streamAiOutput.isNotEmpty) ...[
                                      if (_streamLog.isNotEmpty)
                                        const SizedBox(height: 8),
                                      Container(
                                        padding: const EdgeInsets.all(8),
                                        decoration: BoxDecoration(
                                          color: Colors.grey.shade800,
                                          borderRadius: BorderRadius.circular(
                                            4,
                                          ),
                                        ),
                                        child: SelectableText(
                                          _streamAiOutput,
                                          style: const TextStyle(
                                            fontFamily: 'monospace',
                                            fontSize: 12,
                                            color: Colors.greenAccent,
                                          ),
                                        ),
                                      ),
                                    ],
                                  ],
                                ),
                              ),
                            ),
                          ),
                          if (_userScrolling && _running)
                            Positioned(
                              right: 8,
                              bottom: 8,
                              child: Material(
                                color: Colors.blue.shade600,
                                borderRadius: BorderRadius.circular(20),
                                child: InkWell(
                                  borderRadius: BorderRadius.circular(20),
                                  onTap: () {
                                    _safeSetState(() => _userScrolling = false);
                                    _scrollToBottom();
                                  },
                                  child: const Padding(
                                    padding: EdgeInsets.symmetric(
                                      horizontal: 12,
                                      vertical: 6,
                                    ),
                                    child: Row(
                                      mainAxisSize: MainAxisSize.min,
                                      children: [
                                        Icon(
                                          Icons.arrow_downward,
                                          size: 14,
                                          color: Colors.white,
                                        ),
                                        SizedBox(width: 4),
                                        Text(
                                          '回到底部',
                                          style: TextStyle(
                                            color: Colors.white,
                                            fontSize: 12,
                                          ),
                                        ),
                                      ],
                                    ),
                                  ),
                                ),
                              ),
                            ),
                        ],
                      ),
                    ],
                  ),
                ),
              ],
              const SizedBox(height: 16),
              Container(
                decoration: BoxDecoration(
                  color: Colors.grey.shade50,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: Colors.grey.shade300),
                ),
                padding: const EdgeInsets.all(16),
                child: _historyLoading && _resultContent == null
                    ? const Center(child: CircularProgressIndicator())
                    : SizedBox(
                        height: 320,
                        child: SingleChildScrollView(
                          child: SelectableText(
                            _resultContent ?? '暂无内容，请先运行总结或选择一条历史记录。',
                            style: const TextStyle(fontFamily: 'monospace'),
                          ),
                        ),
                      ),
              ),
              if (_summaryPath != null) ...[
                const SizedBox(height: 8),
                Text(
                  'Saved to: $_summaryPath',
                  style: TextStyle(color: Colors.grey.shade600, fontSize: 12),
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }
}

class _PromptPreview extends StatelessWidget {
  const _PromptPreview({required this.title, required this.content});

  final String title;
  final String content;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Colors.grey.shade100,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: Colors.grey.shade300),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: const TextStyle(fontWeight: FontWeight.w600)),
          const SizedBox(height: 6),
          SelectableText(
            content,
            style: const TextStyle(fontFamily: 'monospace', fontSize: 13),
          ),
        ],
      ),
    );
  }
}

class _DateField extends StatelessWidget {
  const _DateField({
    required this.label,
    required this.value,
    required this.onPick,
    required this.onClear,
  });

  final String label;
  final String? value;
  final VoidCallback onPick;
  final VoidCallback onClear;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onPick,
      child: InputDecorator(
        decoration: InputDecoration(
          labelText: label,
          suffixIcon: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              IconButton(
                icon: const Icon(Icons.calendar_today),
                onPressed: onPick,
              ),
              IconButton(
                icon: const Icon(Icons.close),
                onPressed: value == null ? null : onClear,
              ),
            ],
          ),
        ),
        child: Text(value ?? '未设置'),
      ),
    );
  }
}
