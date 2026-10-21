import 'package:flutter/material.dart';

import '../theme.dart';

class SectionCard extends StatelessWidget {
  const SectionCard({
    super.key,
    required this.title,
    this.subtitle,
    required this.child,
    this.actions,
  });

  final String title;
  final String? subtitle;
  final Widget child;
  final List<Widget>? actions;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final compact = constraints.maxWidth < 600;
        final heading = Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              title,
              style: Theme.of(
                context,
              ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w700),
            ),
            if (subtitle != null) ...[
              const SizedBox(height: 6),
              Text(
                subtitle!,
                style: const TextStyle(
                  color: WxAgentColors.textSecondary,
                  fontSize: 13,
                  height: 1.7,
                ),
              ),
            ],
          ],
        );
        final actionBar = Wrap(
          spacing: 8,
          runSpacing: 8,
          children: actions ?? [],
        );
        return Card(
          child: Padding(
            padding: EdgeInsets.all(compact ? 20 : 26),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (compact)
                  Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      heading,
                      if (actions?.isNotEmpty ?? false) ...[
                        const SizedBox(height: 12),
                        actionBar,
                      ],
                    ],
                  )
                else
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Expanded(child: heading),
                      if (actions?.isNotEmpty ?? false) ...[
                        const SizedBox(width: 20),
                        Flexible(child: actionBar),
                      ],
                    ],
                  ),
                const SizedBox(height: 22),
                child,
              ],
            ),
          ),
        );
      },
    );
  }
}
