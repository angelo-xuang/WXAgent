import 'package:flutter/material.dart';

import '../theme.dart';

class PageContainer extends StatelessWidget {
  const PageContainer({
    super.key,
    required this.title,
    this.subtitle,
    required this.children,
  });

  final String title;
  final String? subtitle;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final compact = constraints.maxWidth < 650;
        return SingleChildScrollView(
          key: PageStorageKey(title),
          padding: EdgeInsets.symmetric(
            horizontal: compact ? 20 : 36,
            vertical: compact ? 24 : 32,
          ),
          child: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 1120),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    title,
                    style: Theme.of(context).textTheme.headlineMedium,
                  ),
                  if (subtitle != null) ...[
                    const SizedBox(height: 8),
                    Text(
                      subtitle!,
                      style: const TextStyle(
                        color: WxAgentColors.textSecondary,
                        height: 1.7,
                      ),
                    ),
                  ],
                  const SizedBox(height: 28),
                  ...children.map(
                    (child) => Padding(
                      padding: const EdgeInsets.only(bottom: 20),
                      child: child,
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}
