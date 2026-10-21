import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../app.dart';
import '../models/models.dart';
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class AnalysisPage extends StatefulWidget {
  const AnalysisPage({super.key, required this.controller});

  final AppController controller;

  @override
  State<AnalysisPage> createState() => _AnalysisPageState();
}

class _AnalysisPageState extends State<AnalysisPage> {
  String? _startDate;
  String? _endDate;
  String _sessionType = 'all';
  int _topN = 10;
  bool _loading = false;
  String? _error;
  AnalysisResult? _result;

  @override
  void initState() {
    super.initState();
    _runAnalysis();
  }

  Future<void> _pickDate({required bool isStart}) async {
    final initial = DateTime.now();
    final picked = await showDatePicker(
      context: context,
      initialDate: initial,
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

  Future<void> _runAnalysis() async {
    _safeSetState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await widget.controller.api.analyzeSessions(
        startDate: _startDate,
        endDate: _endDate,
        topN: _topN,
        sessionTypes: _sessionType == 'all' ? null : [_sessionType],
      );
      _safeSetState(() {
        _result = result;
      });
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

  void _safeSetState(VoidCallback update) {
    if (mounted) setState(update);
  }

  @override
  Widget build(BuildContext context) {
    return PageContainer(
      title: '聊天记录分析',
      subtitle: '以统计视图拆分群聊 / 私聊，辅助定位高频互动会话。',
      children: [
        SectionCard(
          title: '统计筛选条件',
          subtitle: '选择分析时间范围、会话类别以及 Top N 数量，支持全量或增量导出的数据集。',
          actions: [
            FilledButton.icon(
              onPressed: _loading ? null : _runAnalysis,
              icon: _loading
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        valueColor: AlwaysStoppedAnimation(Colors.white),
                      ),
                    )
                  : const Icon(Icons.auto_graph_rounded),
              label: const Text('开始分析'),
            ),
          ],
          child: Column(
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
                        child: _DateSelector(
                          label: '开始日期',
                          value: _startDate,
                          onPick: () => _pickDate(isStart: true),
                          onClear: () => _clearDate(true),
                        ),
                      ),
                      SizedBox(
                        width: width,
                        child: _DateSelector(
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
                spacing: 16,
                runSpacing: 16,
                children: [
                  Wrap(
                    spacing: 12,
                    children: [
                      ChoiceChip(
                        label: const Text('全部'),
                        selected: _sessionType == 'all',
                        onSelected: (_) =>
                            _safeSetState(() => _sessionType = 'all'),
                      ),
                      ChoiceChip(
                        label: const Text('群聊'),
                        selected: _sessionType == 'group',
                        onSelected: (_) =>
                            _safeSetState(() => _sessionType = 'group'),
                      ),
                      ChoiceChip(
                        label: const Text('私聊'),
                        selected: _sessionType == 'private',
                        onSelected: (_) =>
                            _safeSetState(() => _sessionType = 'private'),
                      ),
                    ],
                  ),
                  SizedBox(
                    width: 160,
                    child: DropdownButtonFormField<int>(
                      initialValue: _topN,
                      decoration: const InputDecoration(labelText: 'Top N'),
                      items: const [5, 10, 20, 50].map((n) {
                        return DropdownMenuItem(value: n, child: Text('$n'));
                      }).toList(),
                      onChanged: (value) {
                        if (value != null) {
                          _safeSetState(() => _topN = value);
                        }
                      },
                    ),
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
          title: '分析结果',
          child: _loading
              ? const Center(
                  child: Padding(
                    padding: EdgeInsets.all(32),
                    child: CircularProgressIndicator(),
                  ),
                )
              : _result == null
              ? const Text('尚未生成统计结果。')
              : _AnalysisResultView(result: _result!),
        ),
      ],
    );
  }
}

class _DateSelector extends StatelessWidget {
  const _DateSelector({
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
      borderRadius: BorderRadius.circular(12),
      child: InputDecorator(
        decoration: InputDecoration(
          labelText: label,
          suffixIconConstraints: const BoxConstraints(
            minHeight: 32,
            maxHeight: 36,
            minWidth: 72,
            maxWidth: 90,
          ),
          suffixIcon: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              _iconButton(
                icon: Icons.calendar_today_rounded,
                onPressed: onPick,
                tooltip: '选择日期',
              ),
              const SizedBox(width: 4),
              _iconButton(
                icon: Icons.close,
                onPressed: value == null ? null : onClear,
                tooltip: '清除选择',
              ),
            ],
          ),
        ),
        child: Text(value ?? '未选择'),
      ),
    );
  }

  Widget _iconButton({
    required IconData icon,
    required VoidCallback? onPressed,
    String? tooltip,
  }) {
    return IconButton(
      icon: Icon(icon, size: 18),
      onPressed: onPressed,
      tooltip: tooltip,
      splashRadius: 18,
      padding: EdgeInsets.zero,
      visualDensity: VisualDensity.compact,
      constraints: const BoxConstraints.tightFor(width: 32, height: 32),
    );
  }
}

class _AnalysisResultView extends StatelessWidget {
  const _AnalysisResultView({required this.result});

  final AnalysisResult result;

  @override
  Widget build(BuildContext context) {
    final breakdown = result.breakdown;
    // breakdown 格式: {"group": 5, "private": 10} 表示会话数量
    final groupCount = breakdown['group'] ?? 0;
    final privateCount = breakdown['private'] ?? 0;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Wrap(
          spacing: 16,
          runSpacing: 12,
          children: [
            _StatCard(label: '总消息数', value: '${result.totalMessages}'),
            _StatCard(label: '覆盖会话', value: '${result.sessionCount}'),
            _StatCard(label: '群聊', value: '$groupCount 个'),
            _StatCard(label: '私聊', value: '$privateCount 个'),
          ],
        ),
        const SizedBox(height: 24),
        Text(
          '活跃时间段分布',
          style: Theme.of(
            context,
          ).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 12),
        SizedBox(
          height: 220, // 增加高度以容纳数字标签
          child: _HourlyDistributionChart(data: result.hourlyDistribution),
        ),
        const SizedBox(height: 20),
        Text(
          '高频会话 Top ${result.top.length}',
          style: Theme.of(
            context,
          ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 12),
        _buildTopTable(),
      ],
    );
  }

  Widget _buildTopTable() {
    if (result.top.isEmpty) {
      return const Text('暂无数据');
    }
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      child: DataTable(
        columns: const [
          DataColumn(label: Text('会话')),
          DataColumn(label: Text('类别')),
          DataColumn(label: Text('消息统计')),
          DataColumn(label: Text('消息类型')),
          DataColumn(label: Text('活跃天数')),
        ],
        rows: result.top.map((item) {
          final category = item.sessionType ?? '--';
          final categoryLabel = category == 'group'
              ? '群聊'
              : category == 'private'
              ? '私聊'
              : category;
          final sent = item.sentMessages ?? 0;
          final received = item.receivedMessages ?? 0;
          final messageText = '${item.messages} 条\n发送 $sent / 接收 $received';

          // 消息类型统计
          final textCount = item.textMessages ?? 0;
          final imageCount = item.imageMessages ?? 0;
          final voiceCount = item.voiceMessages ?? 0;
          final videoCount = item.videoMessages ?? 0;
          final otherCount = item.otherMessages ?? 0;
          final typeText =
              '文本 $textCount\n图片 $imageCount / 语音 $voiceCount / 视频 $videoCount / 其他 $otherCount';

          // 活跃天数
          final activeDays = item.activeDays ?? 0;

          return DataRow(
            cells: [
              DataCell(
                SizedBox(
                  width: 180,
                  child: Text(
                    item.displayName,
                    overflow: TextOverflow.ellipsis,
                    maxLines: 2,
                  ),
                ),
              ),
              DataCell(
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 4,
                  ),
                  decoration: BoxDecoration(
                    color: categoryLabel == '群聊'
                        ? Colors.blue.shade100
                        : Colors.green.shade100,
                    borderRadius: BorderRadius.circular(4),
                  ),
                  child: Text(
                    categoryLabel,
                    style: TextStyle(
                      color: categoryLabel == '群聊'
                          ? Colors.blue.shade800
                          : Colors.green.shade800,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ),
              ),
              DataCell(Text(messageText)),
              DataCell(Text(typeText, style: const TextStyle(fontSize: 12))),
              DataCell(Text('$activeDays 天')),
            ],
          );
        }).toList(),
      ),
    );
  }
}

class _StatCard extends StatelessWidget {
  const _StatCard({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 16),
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
          const SizedBox(height: 6),
          Text(
            value,
            style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16),
          ),
        ],
      ),
    );
  }
}

class _HourlyDistributionChart extends StatelessWidget {
  const _HourlyDistributionChart({required this.data});

  final List<int> data;

  @override
  Widget build(BuildContext context) {
    final normalized = List<int>.generate(
      24,
      (index) => index < data.length ? data[index] : 0,
    );
    final maxValue = normalized.fold<int>(
      0,
      (prev, value) => math.max(prev, value),
    );
    if (maxValue == 0) {
      return Container(
        decoration: BoxDecoration(
          color: Colors.grey.shade100,
          borderRadius: BorderRadius.circular(12),
        ),
        alignment: Alignment.center,
        child: const Text('暂无活跃数据'),
      );
    }
    return LayoutBuilder(
      builder: (context, constraints) {
        // 预留空间：底部标签24px，顶部数字16px
        final chartHeight = constraints.maxHeight - 44;
        final barMaxHeight = chartHeight.clamp(50.0, double.infinity);
        return Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Expanded(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  for (var i = 0; i < normalized.length; i++)
                    Expanded(
                      child: Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 1),
                        child: Column(
                          mainAxisAlignment: MainAxisAlignment.end,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            if (normalized[i] > 0)
                              Text(
                                '${normalized[i]}',
                                style: const TextStyle(fontSize: 9),
                                overflow: TextOverflow.visible,
                              ),
                            const SizedBox(height: 2),
                            Container(
                              height:
                                  barMaxHeight *
                                  (normalized[i] / maxValue).clamp(0.0, 1.0),
                              decoration: BoxDecoration(
                                color: Theme.of(
                                  context,
                                ).colorScheme.primary.withValues(alpha: 0.75),
                                borderRadius: const BorderRadius.vertical(
                                  top: Radius.circular(4),
                                ),
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                ],
              ),
            ),
            const SizedBox(height: 4),
            SizedBox(
              height: 20,
              child: Row(
                children: [
                  for (var i = 0; i < normalized.length; i++)
                    Expanded(
                      child: Text(
                        i % 3 == 0 ? '${i.toString().padLeft(2, '0')}时' : '',
                        style: const TextStyle(
                          fontSize: 10,
                          color: Colors.grey,
                        ),
                        textAlign: TextAlign.center,
                      ),
                    ),
                ],
              ),
            ),
          ],
        );
      },
    );
  }
}
