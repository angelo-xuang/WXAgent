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
    controller = AppController(
      autoStartBackend: false,
      apiClientFactory: (url) => WxAgentApiClient(
        baseUrl: url,
        httpClient: MockClient((request) async {
          requests.add(request);
          final Object data = switch (request.url.path) {
            '/config' => {
              'data': {
                'wechat_data_path': 'C:/preview/wechat',
                'export_dir': 'C:/preview/exports',
                'llm': {
                  'base_url': 'https://example.invalid/v1',
                  'model': 'demo-model',
                },
              },
              'path': 'preview/config.json',
            },
            '/status' => {
              'platform': 'windows',
              'capabilities': {
                'native_wechat': true,
                'analyze_exports': true,
                'filtered_export': false,
              },
              'export': {'sessions': 0, 'messages': 0},
            },
            '/analysis/sessions' => {
              'top': [
                {
                  'session_id': 'preview-group',
                  'display_name': '测试群聊（模拟数据）',
                  'session_type': 'group',
                  'messages': 30,
                  'text_messages': 25,
                  'active_days': 3,
                },
                {
                  'session_id': 'preview-private',
                  'display_name': '测试会话（模拟数据）',
                  'session_type': 'private',
                  'messages': 12,
                  'text_messages': 12,
                  'active_days': 2,
                },
              ],
              'total_messages': 42,
              'session_count': 2,
              'breakdown': {'group': 1, 'private': 1},
              'hourly_distribution': [
                0,
                0,
                0,
                0,
                0,
                0,
                0,
                0,
                2,
                4,
                8,
                5,
                2,
                0,
                3,
                2,
                4,
                2,
                5,
                3,
                2,
                0,
                0,
                0,
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
      expect(find.text('把聊天，整理成有价值的信息'), findsOneWidget);
      expect(
        requests,
        isEmpty,
        reason: 'Home must not read other pages or start processes',
      );
      expect(tester.takeException(), isNull);
      await capture(tester, boundaryKey, 'home-${width.toInt()}');

      for (final page in ['key', 'data', 'analysis', 'summary', 'settings']) {
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

  testWidgets('home action opens connection page', (tester) async {
    tester.view.physicalSize = const Size(1440, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(WxAgentRoot(controller: controller));
    await tester.pumpAndSettle();
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
      'https://my-model.invalid/v1',
    );
    final before = requests
        .where((request) => request.url.path == '/config')
        .length;
    await openPage(tester, 'welcome');
    await openPage(tester, 'settings');
    expect(find.text('https://my-model.invalid/v1'), findsOneWidget);
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
    expect(find.byKey(const ValueKey('welcome-connect')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
