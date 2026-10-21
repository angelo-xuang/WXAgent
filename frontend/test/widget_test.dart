import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:wx_agent_app/src/app.dart';
import 'package:wx_agent_app/src/services/wx_agent_api.dart';

void main() {
  final requests = <http.Request>[];
  late AppController controller;
  var failImport = false;
  var failQuery = false;
  var indexExists = true;

  setUpAll(() async {
    // Use the repository's Chinese font for realistic desktop layout coverage.
    final font = File(
      '../modules/echotrace/assets/HarmonyOS_SansSC/HarmonyOS_SansSC_Regular.ttf',
    );
    final fontBytes = ByteData.sublistView(await font.readAsBytes());
    for (final family in ['Microsoft YaHei', 'Roboto', 'monospace']) {
      final loader = FontLoader(family);
      loader.addFont(Future.value(fontBytes));
      await loader.load();
    }
    final icons = FontLoader('MaterialIcons');
    icons.addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'));
    await icons.load();
  });

  setUp(() {
    requests.clear();
    failImport = false;
    failQuery = false;
    indexExists = true;
    controller = AppController(
      autoStartBackend: false,
      apiClientFactory: (url) => WxAgentApiClient(
        baseUrl: url,
        httpClient: MockClient((request) async {
          requests.add(request);
          if ((failImport && request.url.path == '/index/import') ||
              (failQuery && request.url.path == '/messages')) {
            return http.Response(
              jsonEncode({'error': '模拟错误：读取失败'}),
              400,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }
          final offset =
              int.tryParse(request.url.queryParameters['offset'] ?? '') ?? 0;
          final total =
              request.url.queryParameters['keyword'] == '不存在' || !indexExists
              ? 0
              : 75;
          final Object data = switch (request.url.path) {
            '/config' => {
              'data': {
                'wechat_data_path': 'C:/preview/wechat',
                'export_dir': 'C:/preview/exports',
                'database_path': 'C:/preview/data/wxagent.db',
              },
              'path': 'preview/config.json',
            },
            '/status' => {
              'platform': 'windows',
              'capabilities': {
                'native_wechat': true,
                'query_messages': true,
                'stats': true,
              },
            },
            '/index/status' => {
              'exists': indexExists,
              'database_path': 'C:/preview/data/wxagent.db',
              'messages': indexExists ? 75 : 0,
              'sessions': indexExists ? 1 : 0,
              'sources': indexExists ? 2 : 0,
            },
            '/index/import' => {
              'files': 2,
              'changed_files': 1,
              'unchanged_files': 1,
              'messages': 75,
              'sessions': 1,
              'database_path': 'C:/preview/data/wxagent.db',
            },
            '/sessions' =>
              indexExists
                  ? [
                      {
                        'session_id': 'preview-group',
                        'display_name': '测试群聊（模拟数据）',
                        'messages': 75,
                      },
                    ]
                  : [],
            '/messages' => {
              'total': total,
              'offset': offset,
              'limit': 50,
              'messages': [
                for (var i = offset; i < offset + 50 && i < total; i++)
                  {
                    'session_id': 'preview-group',
                    'display_name': '测试群聊（模拟数据）',
                    'local_id': i + 1,
                    'formatted_time': '2026-10-20 10:30:00',
                    'type': 'text',
                    'content': '会议原文第 ${i + 1} 条（模拟数据）',
                    'sender_name': '示例成员',
                    'sender': 'preview-user',
                    'is_send': 0,
                  },
              ],
            },
            _ => {'data': []},
          };
          return http.Response(
            jsonEncode(data),
            200,
            headers: {'content-type': 'application/json; charset=utf-8'},
          );
        }),
      ),
    );
  });
  tearDown(() => controller.dispose());

  Future<void> openPage(WidgetTester tester, String page) async {
    if (tester.view.physicalSize.width < 760) {
      await tester.tap(find.byTooltip('打开导航'));
      await tester.pumpAndSettle();
    }
    await tester.tap(find.byKey(ValueKey('nav-$page')));
    await tester.pumpAndSettle();
  }

  Future<void> capture(WidgetTester tester, GlobalKey key, String name) async {
    final directory = Platform.environment['WX_AGENT_SCREENSHOT_DIR'];
    if (directory == null || directory.isEmpty) return;
    await tester.runAsync(() async {
      final boundary =
          key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final image = await boundary.toImage();
      final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
      await Directory(directory).create(recursive: true);
      await File(
        '$directory/$name.png',
      ).writeAsBytes(bytes!.buffer.asUint8List());
      image.dispose();
    });
  }

  for (final width in [390.0, 800.0, 1024.0, 1440.0]) {
    testWidgets('all pages work at ${width.toInt()}px without overflow', (
      tester,
    ) async {
      tester.view.devicePixelRatio = 1;
      tester.view.physicalSize = Size(width, 1000);
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final boundaryKey = GlobalKey();
      await tester.pumpWidget(
        RepaintBoundary(
          key: boundaryKey,
          child: WxAgentRoot(controller: controller),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('让每一条记录，都能被找到'), findsOneWidget);
      expect(
        requests,
        isEmpty,
        reason: 'Home must not read other pages or start processes',
      );
      expect(tester.takeException(), isNull);
      await capture(tester, boundaryKey, 'home-${width.toInt()}');

      for (final page in ['library', 'records', 'data', 'key', 'settings']) {
        await openPage(tester, page);
        expect(tester.takeException(), isNull, reason: '$page at $width');
        if (width == 1440 || width == 390) {
          await capture(tester, boundaryKey, '$page-${width.toInt()}');
        }
      }
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('home action opens native capture page', (tester) async {
    tester.view.physicalSize = const Size(1440, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(WxAgentRoot(controller: controller));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byKey(const ValueKey('welcome-connect')));
    await tester.tap(find.byKey(const ValueKey('welcome-connect')));
    await tester.pumpAndSettle();
    expect(requests.any((request) => request.url.path == '/status'), isTrue);
    expect(tester.takeException(), isNull);
  });

  testWidgets('unsaved settings survive page navigation', (tester) async {
    tester.view.physicalSize = const Size(1440, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(WxAgentRoot(controller: controller));
    await tester.pumpAndSettle();
    await openPage(tester, 'settings');
    await tester.enterText(
      find.byType(TextField).first,
      'http://127.0.0.1:9000',
    );
    final before = requests
        .where((request) => request.url.path == '/config')
        .length;
    await openPage(tester, 'welcome');
    await openPage(tester, 'settings');
    expect(find.text('http://127.0.0.1:9000'), findsOneWidget);
    expect(
      requests.where((request) => request.url.path == '/config').length,
      before,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('larger system text keeps home usable', (tester) async {
    tester.view.physicalSize = const Size(800, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    tester.platformDispatcher.textScaleFactorTestValue = 1.5;
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    await tester.pumpWidget(WxAgentRoot(controller: controller));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('welcome-index')), findsOneWidget);
    expect(tester.takeException(), isNull);
    for (final page in ['library', 'records', 'settings']) {
      await openPage(tester, page);
      expect(
        tester.takeException(),
        isNull,
        reason: '$page with large system text',
      );
    }
  });
  Future<void> startDesktop(WidgetTester tester) async {
    tester.view.physicalSize = const Size(1440, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(WxAgentRoot(controller: controller));
    await tester.pumpAndSettle();
  }

  testWidgets(
    'import sends selected directory and reports deduplicated result',
    (tester) async {
      await startDesktop(tester);
      await openPage(tester, 'library');
      await tester.enterText(
        find.byKey(const ValueKey('index-directory')),
        'C:/preview/new-records',
      );
      await tester.tap(find.byKey(const ValueKey('index-import')));
      await tester.pumpAndSettle();
      final imported = requests.lastWhere(
        (item) => item.url.path == '/index/import',
      );
      expect(jsonDecode(imported.body), {
        'export_dir': 'C:/preview/new-records',
      });
      expect(find.textContaining('更新 1 个，跳过 1 个'), findsOneWidget);
      expect(find.text('索引库共 75 条记录、1 个会话。'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('import errors are visible without a false success message', (
    tester,
  ) async {
    failImport = true;
    await startDesktop(tester);
    await openPage(tester, 'library');
    await tester.tap(find.byKey(const ValueKey('index-import')));
    await tester.pumpAndSettle();
    expect(find.textContaining('模拟错误'), findsOneWidget);
    expect(find.textContaining('导入完成：'), findsNothing);
  });

  testWidgets('query preserves applied filters across explicit pages', (
    tester,
  ) async {
    await startDesktop(tester);
    await openPage(tester, 'records');
    await tester.tap(find.text('全部会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('测试群聊（模拟数据） (75)').last);
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const ValueKey('records-keyword')), '会议');
    await tester.enterText(
      find.byKey(const ValueKey('records-start')),
      '2026-10-01',
    );
    await tester.enterText(
      find.byKey(const ValueKey('records-end')),
      '2026-10-22',
    );
    await tester.tap(find.byKey(const ValueKey('records-search')));
    await tester.pumpAndSettle();
    expect(find.text('会议原文第 1 条（模拟数据）'), findsOneWidget);
    expect(find.text('显示 1–50 / 75'), findsNWidgets(2));
    await tester.enterText(
      find.byKey(const ValueKey('records-keyword')),
      '尚未提交的修改',
    );
    await tester.ensureVisible(find.text('下一页').first);
    await tester.tap(find.text('下一页').first);
    await tester.pumpAndSettle();
    final request = requests.lastWhere((item) => item.url.path == '/messages');
    expect(request.url.queryParameters, {
      'keyword': '会议',
      'session_id': 'preview-group',
      'start_date': '2026-10-01',
      'end_date': '2026-10-22',
      'offset': '50',
      'limit': '50',
    });
    expect(find.text('显示 51–75 / 75'), findsNWidgets(2));
    expect(find.text('会议原文第 51 条（模拟数据）'), findsOneWidget);
    expect(find.text('会议原文第 1 条（模拟数据）'), findsNothing);
  });

  testWidgets('empty query and query failure show distinct states', (
    tester,
  ) async {
    await startDesktop(tester);
    await openPage(tester, 'records');
    await tester.enterText(
      find.byKey(const ValueKey('records-keyword')),
      '不存在',
    );
    await tester.tap(find.byKey(const ValueKey('records-search')));
    await tester.pumpAndSettle();
    expect(find.textContaining('没有符合条件的记录'), findsOneWidget);
    failQuery = true;
    await tester.tap(find.byKey(const ValueKey('records-search')));
    await tester.pumpAndSettle();
    expect(find.textContaining('模拟错误'), findsOneWidget);
    expect(find.textContaining('没有符合条件的记录'), findsNothing);
  });

  testWidgets('missing index guides users to import', (tester) async {
    indexExists = false;
    await startDesktop(tester);
    await openPage(tester, 'records');
    expect(find.text('还没有建立索引'), findsOneWidget);
    expect(find.text('前往索引库'), findsOneWidget);
  });

  testWidgets('visited records reload after import or database change', (
    tester,
  ) async {
    await startDesktop(tester);
    await openPage(tester, 'records');
    final before = requests
        .where((item) => item.url.path == '/messages')
        .length;
    await openPage(tester, 'library');
    await tester.tap(find.byKey(const ValueKey('index-import')));
    await tester.pumpAndSettle();
    expect(
      requests.where((item) => item.url.path == '/messages').length,
      before + 1,
    );
    await openPage(tester, 'settings');
    await tester.enterText(
      find.byKey(const ValueKey('settings-database')),
      'C:/preview/other.db',
    );
    await tester.ensureVisible(find.text('保存设置'));
    await tester.tap(find.text('保存设置'));
    await tester.pumpAndSettle();
    expect(
      requests.where((item) => item.url.path == '/messages').length,
      before + 2,
    );
    final saved = requests.lastWhere((item) => item.method == 'PUT');
    expect((jsonDecode(saved.body) as Map)['values'], {
      'database_path': 'C:/preview/other.db',
      'export_dir': 'C:/preview/exports',
    });
    expect(tester.takeException(), isNull);
  });
}
