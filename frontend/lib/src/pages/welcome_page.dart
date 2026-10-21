import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app.dart';
import '../theme.dart';
import '../widgets/page_container.dart';

class WelcomePage extends StatelessWidget {
  const WelcomePage({super.key, required this.controller});
  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final navigate = context.read<NavigationController>().goTo;
    return PageContainer(
      title: '把聊天，整理成有价值的信息',
      subtitle: '从本地记录出发，回顾讨论、发现重点，让每一次对话都有迹可循。',
      children: [
        _GettingStarted(onSelect: navigate),
        const SizedBox(height: 4),
        const Text(
          '你的工作流程',
          style: TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
        ),
        LayoutBuilder(
          builder: (context, constraints) {
            final columns = constraints.maxWidth >= 820
                ? 3
                : constraints.maxWidth >= 520
                ? 2
                : 1;
            final width = (constraints.maxWidth - (columns - 1) * 16) / columns;
            return Wrap(
              spacing: 16,
              runSpacing: 16,
              children: [
                _WorkflowCard(
                  width: width,
                  number: '01',
                  icon: Icons.folder_open_rounded,
                  title: '整理聊天数据',
                  description: '选择微信数据目录，将聊天记录导出到本地，方便归档与检索。',
                  action: '查看聊天数据',
                  onTap: () => navigate(WxAgentPage.data),
                ),
                _WorkflowCard(
                  width: width,
                  number: '02',
                  icon: Icons.bar_chart_rounded,
                  title: '看见对话中的规律',
                  description: '按日期和会话筛选，查看消息分布、活跃时段与高频联系人。',
                  action: '打开数据分析',
                  onTap: () => navigate(WxAgentPage.analysis),
                ),
                _WorkflowCard(
                  width: width,
                  number: '03',
                  icon: Icons.auto_awesome_outlined,
                  title: '提炼值得留下的内容',
                  description: '选择需要的聊天，用 AI 整理重要通知、讨论结论和待办事项。',
                  action: '开始 AI 总结',
                  onTap: () => navigate(WxAgentPage.summary),
                ),
              ],
            );
          },
        ),
        Container(
          padding: const EdgeInsets.all(22),
          decoration: BoxDecoration(
            color: const Color(0xFFEEF1F5),
            borderRadius: BorderRadius.circular(16),
          ),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Icon(
                Icons.shield_outlined,
                size: 22,
                color: WxAgentColors.textSecondary,
              ),
              const SizedBox(width: 14),
              const Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '清楚知道，数据去了哪里',
                      style: TextStyle(fontWeight: FontWeight.w600),
                    ),
                    SizedBox(height: 6),
                    Text(
                      '读取、导出与统计在本地完成。运行 AI 总结时，所选聊天内容会发送给你在设置中配置的模型服务商。',
                      style: TextStyle(
                        color: WxAgentColors.textSecondary,
                        fontSize: 13,
                        height: 1.7,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
            onPressed: () => navigate(WxAgentPage.settings),
            icon: const Icon(Icons.tune_rounded, size: 18),
            label: const Text('配置模型服务'),
          ),
        ),
      ],
    );
  }
}

class _GettingStarted extends StatelessWidget {
  const _GettingStarted({required this.onSelect});
  final ValueChanged<WxAgentPage> onSelect;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final compact = constraints.maxWidth < 650;
        return Container(
          padding: EdgeInsets.all(compact ? 24 : 32),
          decoration: BoxDecoration(
            gradient: const LinearGradient(
              colors: [Color(0xFFE1EEE7), Color(0xFFF0F5EC)],
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
            ),
            borderRadius: BorderRadius.circular(20),
            border: Border.all(color: const Color(0xFFD4E5DA)),
          ),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text(
                      '从这里开始',
                      style: TextStyle(
                        color: WxAgentColors.primary,
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 12),
                    Text(
                      '连接微信，建立你的本地资料库',
                      style: TextStyle(
                        fontSize: compact ? 21 : 25,
                        fontWeight: FontWeight.w700,
                        height: 1.4,
                      ),
                    ),
                    const SizedBox(height: 12),
                    const Text(
                      '首次使用先连接微信，再导出聊天记录。\n已经有导出数据？可以直接进入分析。',
                      style: TextStyle(
                        color: WxAgentColors.textSecondary,
                        height: 1.8,
                      ),
                    ),
                    const SizedBox(height: 24),
                    Wrap(
                      spacing: 12,
                      runSpacing: 10,
                      children: [
                        ElevatedButton.icon(
                          key: const ValueKey('welcome-connect'),
                          onPressed: () => onSelect(WxAgentPage.key),
                          icon: const Icon(Icons.add_rounded, size: 19),
                          label: const Text('连接微信'),
                        ),
                        TextButton.icon(
                          onPressed: () => onSelect(WxAgentPage.analysis),
                          icon: const Icon(
                            Icons.arrow_forward_rounded,
                            size: 18,
                          ),
                          label: const Text('查看已有数据'),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
              if (constraints.maxWidth >= 850) ...[
                const SizedBox(width: 36),
                Container(
                  width: 112,
                  height: 112,
                  decoration: BoxDecoration(
                    color: Colors.white.withValues(alpha: 0.65),
                    borderRadius: BorderRadius.circular(28),
                    border: Border.all(color: Colors.white),
                  ),
                  child: const Icon(
                    Icons.forum_outlined,
                    color: WxAgentColors.primary,
                    size: 48,
                  ),
                ),
                const SizedBox(width: 8),
              ],
            ],
          ),
        );
      },
    );
  }
}

class _WorkflowCard extends StatelessWidget {
  const _WorkflowCard({
    required this.width,
    required this.number,
    required this.icon,
    required this.title,
    required this.description,
    required this.action,
    required this.onTap,
  });
  final double width;
  final String number;
  final IconData icon;
  final String title;
  final String description;
  final String action;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: width,
      child: Card(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Container(
                    padding: const EdgeInsets.all(10),
                    decoration: BoxDecoration(
                      color: const Color(0xFFF0F5F2),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Icon(icon, color: WxAgentColors.primary, size: 24),
                  ),
                  const Spacer(),
                  Text(
                    number,
                    style: const TextStyle(
                      color: Color(0xFFADB9B3),
                      fontSize: 20,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 20),
              Text(
                title,
                style: const TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(height: 10),
              Text(
                description,
                style: const TextStyle(
                  color: WxAgentColors.textSecondary,
                  fontSize: 13,
                  height: 1.8,
                ),
              ),
              const SizedBox(height: 16),
              TextButton(onPressed: onTap, child: Text('$action →')),
            ],
          ),
        ),
      ),
    );
  }
}
