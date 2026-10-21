import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'pages/pages.dart';
import 'services/app_controller.dart';
import 'theme.dart';

export 'services/app_controller.dart';

enum WxAgentPage { welcome, library, records, data, key, settings }

class NavigationController extends ChangeNotifier {
  WxAgentPage _page = WxAgentPage.welcome;
  WxAgentPage get page => _page;

  void goTo(WxAgentPage target) {
    if (target == _page) return;
    _page = target;
    notifyListeners();
  }
}

class WxAgentRoot extends StatelessWidget {
  const WxAgentRoot({super.key, this.controller});

  /// External controllers are owned by the caller (also useful for previews).
  final AppController? controller;

  @override
  Widget build(BuildContext context) {
    return MultiProvider(
      providers: [
        if (controller == null)
          ChangeNotifierProvider(create: (_) => AppController())
        else
          ChangeNotifierProvider<AppController>.value(value: controller!),
        ChangeNotifierProvider(create: (_) => NavigationController()),
      ],
      child: MaterialApp(
        title: 'WXAgent',
        theme: buildWxAgentTheme(),
        debugShowCheckedModeBanner: false,
        home: const WxAgentHome(),
      ),
    );
  }
}

class WxAgentHome extends StatefulWidget {
  const WxAgentHome({super.key});

  @override
  State<WxAgentHome> createState() => _WxAgentHomeState();
}

class _WxAgentHomeState extends State<WxAgentHome> {
  final _visited = <WxAgentPage>{WxAgentPage.welcome};

  Widget _page(WxAgentPage page, AppController controller) => switch (page) {
    WxAgentPage.welcome => WelcomePage(controller: controller),
    WxAgentPage.key => KeyAcquisitionPage(controller: controller),
    WxAgentPage.data => ChatDataPage(controller: controller),
    WxAgentPage.library => IndexPage(controller: controller),
    WxAgentPage.records => RecordsPage(controller: controller),
    WxAgentPage.settings => SettingsPage(controller: controller),
  };

  @override
  Widget build(BuildContext context) {
    final nav = context.watch<NavigationController>();
    final controller = context.watch<AppController>();
    _visited.add(nav.page);
    final currentItem = _navItems[nav.page.index];

    return LayoutBuilder(
      builder: (context, constraints) {
        final useDrawer = constraints.maxWidth < 760;
        final compactRail = constraints.maxWidth < 1180;
        return Scaffold(
          drawer: useDrawer
              ? Drawer(
                  child: SafeArea(
                    child: NavigationSidebar(
                      current: nav.page,
                      onSelect: (page) {
                        nav.goTo(page);
                        Navigator.of(context).pop();
                      },
                    ),
                  ),
                )
              : null,
          body: SafeArea(
            child: Row(
              children: [
                if (!useDrawer)
                  NavigationSidebar(
                    current: nav.page,
                    onSelect: nav.goTo,
                    compact: compactRail,
                  ),
                Expanded(
                  child: Column(
                    children: [
                      Container(
                        height: 68,
                        padding: EdgeInsets.symmetric(
                          horizontal: useDrawer ? 12 : 32,
                        ),
                        decoration: const BoxDecoration(
                          color: WxAgentColors.surface,
                          border: Border(
                            bottom: BorderSide(color: WxAgentColors.border),
                          ),
                        ),
                        child: Row(
                          children: [
                            if (useDrawer)
                              Builder(
                                builder: (context) => IconButton(
                                  tooltip: '打开导航',
                                  icon: const Icon(Icons.menu_rounded),
                                  onPressed: () =>
                                      Scaffold.of(context).openDrawer(),
                                ),
                              )
                            else
                              Icon(
                                currentItem.icon,
                                size: 19,
                                color: WxAgentColors.textSecondary,
                              ),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Text(
                                currentItem.label,
                                style: const TextStyle(
                                  fontWeight: FontWeight.w600,
                                  fontSize: 14,
                                ),
                              ),
                            ),
                            const _ConnectionBadge(),
                          ],
                        ),
                      ),
                      const BackendStatusBanner(),
                      Expanded(
                        // Defer data requests until the page is opened, then preserve
                        // filters, forms and running tasks while navigating elsewhere.
                        child: IndexedStack(
                          sizing: StackFit.expand,
                          index: nav.page.index,
                          children: [
                            for (final page in WxAgentPage.values)
                              if (_visited.contains(page))
                                KeyedSubtree(
                                  key: ValueKey(page),
                                  child: _page(page, controller),
                                )
                              else
                                const SizedBox.shrink(),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

class NavigationSidebar extends StatelessWidget {
  const NavigationSidebar({
    super.key,
    required this.current,
    required this.onSelect,
    this.compact = false,
  });
  final WxAgentPage current;
  final ValueChanged<WxAgentPage> onSelect;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: compact ? 88 : 232,
      decoration: const BoxDecoration(
        color: WxAgentColors.sidebar,
        border: Border(right: BorderSide(color: WxAgentColors.border)),
      ),
      padding: EdgeInsets.symmetric(
        horizontal: compact ? 14 : 18,
        vertical: 24,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(horizontal: compact ? 8 : 6),
            child: Row(
              children: [
                ClipRRect(
                  borderRadius: BorderRadius.circular(12),
                  child: Image.asset(
                    'assets/images/wxagent_logo.png',
                    width: 40,
                    height: 40,
                    fit: BoxFit.cover,
                  ),
                ),
                if (!compact) ...[
                  const SizedBox(width: 12),
                  const Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        'WXAgent',
                        style: TextStyle(
                          fontSize: 20,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                      Text(
                        '本地聊天记录索引库',
                        style: TextStyle(
                          fontSize: 11,
                          color: WxAgentColors.textSecondary,
                        ),
                      ),
                    ],
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(height: 34),
          Expanded(
            child: ListView(
              children: [
                if (!compact)
                  const Padding(
                    padding: EdgeInsets.only(left: 14, bottom: 12),
                    child: Text(
                      '工作空间',
                      style: TextStyle(
                        fontSize: 11,
                        letterSpacing: 1.4,
                        color: WxAgentColors.textSecondary,
                      ),
                    ),
                  ),
                for (final item in _navItems.where(
                  (item) => item.page != WxAgentPage.settings,
                ))
                  Padding(
                    padding: const EdgeInsets.only(bottom: 6),
                    child: _navigationItem(item),
                  ),
              ],
            ),
          ),
          _navigationItem(_navItems.last),
          const SizedBox(height: 18),
          if (!compact)
            const Padding(
              padding: EdgeInsets.symmetric(horizontal: 12),
              child: Row(
                children: [
                  Icon(
                    Icons.folder_outlined,
                    size: 16,
                    color: WxAgentColors.textSecondary,
                  ),
                  SizedBox(width: 8),
                  Text(
                    '本地数据 · 自主掌控',
                    style: TextStyle(
                      fontSize: 11,
                      color: WxAgentColors.textSecondary,
                    ),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }

  Widget _navigationItem(_NavItem item) {
    final selected = item.page == current;
    final color = selected
        ? WxAgentColors.primary
        : WxAgentColors.textSecondary;
    final content = Semantics(
      selected: selected,
      button: true,
      label: compact ? item.label : null,
      child: Material(
        color: selected ? const Color(0xFFDDEBE5) : Colors.transparent,
        borderRadius: BorderRadius.circular(12),
        child: InkWell(
          key: ValueKey('nav-${item.page.name}'),
          onTap: () => onSelect(item.page),
          borderRadius: BorderRadius.circular(12),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 15),
            child: Row(
              mainAxisAlignment: compact
                  ? MainAxisAlignment.center
                  : MainAxisAlignment.start,
              children: [
                Icon(item.icon, color: color, size: 22),
                if (!compact) ...[
                  const SizedBox(width: 13),
                  Expanded(
                    child: Text(
                      item.label,
                      style: TextStyle(
                        color: color,
                        fontSize: 14,
                        fontWeight: selected
                            ? FontWeight.w700
                            : FontWeight.w500,
                      ),
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
    return compact ? Tooltip(message: item.label, child: content) : content;
  }
}

class _NavItem {
  const _NavItem(this.page, this.icon, this.label);
  final WxAgentPage page;
  final IconData icon;
  final String label;
}

const _navItems = [
  _NavItem(WxAgentPage.welcome, Icons.space_dashboard_outlined, '概览'),
  _NavItem(WxAgentPage.library, Icons.storage_rounded, '索引库'),
  _NavItem(WxAgentPage.records, Icons.manage_search_rounded, '聊天记录'),
  _NavItem(WxAgentPage.data, Icons.folder_open_rounded, '微信采集'),
  _NavItem(WxAgentPage.key, Icons.key_rounded, '连接微信'),
  _NavItem(WxAgentPage.settings, Icons.tune_rounded, '设置'),
];

class _ConnectionBadge extends StatelessWidget {
  const _ConnectionBadge();

  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppController>().backendState;
    final (label, color) = switch (state) {
      BackendState.running => ('服务已连接', WxAgentColors.success),
      BackendState.failed => ('连接待恢复', WxAgentColors.danger),
      BackendState.skipped => ('手动连接模式', WxAgentColors.textSecondary),
      BackendState.checking ||
      BackendState.starting => ('正在连接', WxAgentColors.warning),
      _ => ('准备连接', WxAgentColors.textSecondary),
    };
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 6,
          height: 6,
          decoration: BoxDecoration(color: color, shape: BoxShape.circle),
        ),
        const SizedBox(width: 8),
        Text(label, style: TextStyle(color: color, fontSize: 12)),
      ],
    );
  }
}

class BackendStatusBanner extends StatelessWidget {
  const BackendStatusBanner({super.key});

  @override
  Widget build(BuildContext context) {
    final controller = context.watch<AppController>();
    final state = controller.backendState;
    if (state == BackendState.running ||
        state == BackendState.skipped ||
        state == BackendState.idle) {
      return const SizedBox.shrink();
    }
    final failed = state == BackendState.failed;
    final color = failed ? WxAgentColors.danger : WxAgentColors.primary;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
      color: failed ? const Color(0xFFFFF1ED) : const Color(0xFFEAF3EF),
      child: Row(
        children: [
          Icon(
            failed ? Icons.info_outline : Icons.sync_rounded,
            color: color,
            size: 20,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              controller.backendMessage ??
                  (failed ? '服务暂时无法连接，请重试。' : '正在连接本地服务…'),
              style: TextStyle(color: color, fontSize: 13),
            ),
          ),
          const SizedBox(width: 8),
          if (failed)
            TextButton(
              onPressed: controller.restartBackend,
              child: const Text('重新连接'),
            )
          else
            SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2, color: color),
            ),
        ],
      ),
    );
  }
}
