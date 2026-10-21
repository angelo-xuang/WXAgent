import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../app.dart';
import '../theme.dart';
import '../widgets/page_container.dart';
import '../widgets/section_card.dart';

class WelcomePage extends StatelessWidget {
  const WelcomePage({super.key, required this.controller});
  final AppController controller;

  @override
  Widget build(BuildContext context) {
    final navigate = context.read<NavigationController>().goTo;
    return PageContainer(
      title: '让每一条记录，都能被找到',
      subtitle: 'WXAgent · 本地聊天记录索引库',
      children: [
        Container(
          padding: const EdgeInsets.all(28),
          decoration: BoxDecoration(
            gradient: const LinearGradient(
              colors: [Color(0xFFE1EEE7), Color(0xFFF0F5EC)],
            ),
            borderRadius: BorderRadius.circular(20),
            border: Border.all(color: const Color(0xFFD4E5DA)),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Icon(
                Icons.storage_rounded,
                size: 32,
                color: WxAgentColors.primary,
              ),
              const SizedBox(height: 18),
              const Text(
                '保存原文，建立长期可查的索引',
                style: TextStyle(
                  fontSize: 24,
                  fontWeight: FontWeight.w700,
                  height: 1.45,
                ),
              ),
              const SizedBox(height: 12),
              const Text(
                '把已导出的聊天记录导入本地 SQLite 数据库。\n按会话、日期和关键词检索，随时回到原始记录。',
                style: TextStyle(
                  height: 1.8,
                  color: WxAgentColors.textSecondary,
                ),
              ),
              const SizedBox(height: 24),
              Wrap(
                spacing: 12,
                runSpacing: 10,
                children: [
                  ElevatedButton.icon(
                    key: const ValueKey('welcome-index'),
                    onPressed: () => navigate(WxAgentPage.library),
                    icon: const Icon(Icons.add_rounded),
                    label: const Text('管理索引库'),
                  ),
                  OutlinedButton.icon(
                    onPressed: () => navigate(WxAgentPage.records),
                    icon: const Icon(Icons.search_rounded),
                    label: const Text('检索聊天记录'),
                  ),
                ],
              ),
            ],
          ),
        ),
        SectionCard(
          title: 'CLI 优先，按自己的方式使用',
          subtitle: '独立命令行可完成导入和查询；这个界面是同一份数据库的可视入口。',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const _Command(label: '导入记录', command: 'wxagent import'),
              const SizedBox(height: 14),
              const _Command(
                label: '查找原文',
                command: 'wxagent messages --keyword "会议" --limit 50',
              ),
              const SizedBox(height: 14),
              const _Command(label: '启动界面连接服务', command: 'wxagent serve'),
              const SizedBox(height: 16),
              const Text(
                '命令行输出结构化 JSON，可供脚本与 Skill 读取。查询不会修改原始聊天文件。',
                style: TextStyle(
                  color: WxAgentColors.textSecondary,
                  height: 1.7,
                ),
              ),
            ],
          ),
        ),
        SectionCard(
          title: '先导入，再检索',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                '已有 JSON 导出文件：在“索引库”选择目录并导入。重复导入会去重，历史记录保留。',
                style: TextStyle(height: 1.7),
              ),
              const SizedBox(height: 10),
              const Text(
                '需要采集微信记录：Windows 后端可连接微信并导出，完成后自动更新索引库。其他平台可导入已有文件。',
                style: TextStyle(height: 1.7),
              ),
              const SizedBox(height: 12),
              TextButton.icon(
                key: const ValueKey('welcome-connect'),
                onPressed: () => navigate(WxAgentPage.data),
                icon: const Icon(Icons.folder_open_rounded),
                label: const Text('前往微信采集'),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _Command extends StatelessWidget {
  const _Command({required this.label, required this.command});
  final String label;
  final String command;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        label,
        style: const TextStyle(
          fontSize: 12,
          color: WxAgentColors.textSecondary,
        ),
      ),
      const SizedBox(height: 5),
      Container(
        width: double.infinity,
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: WxAgentColors.background,
          borderRadius: BorderRadius.circular(8),
        ),
        child: SelectableText(
          command,
          style: const TextStyle(
            fontFamily: 'monospace',
            fontSize: 13,
            height: 1.6,
          ),
        ),
      ),
    ],
  );
}
