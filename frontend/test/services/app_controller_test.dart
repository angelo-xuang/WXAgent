import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';
import 'package:http/http.dart' as http;
import 'package:wx_agent_app/src/services/app_controller.dart';
import 'package:wx_agent_app/src/services/wx_agent_api.dart';

class FakeProcess implements Process {
  final exited = Completer<int>();
  var killCount = 0;
  @override
  Future<int> get exitCode => exited.future;
  @override
  Stream<List<int>> get stdout => const Stream.empty();
  @override
  Stream<List<int>> get stderr => const Stream.empty();
  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    killCount++;
    if (!exited.isCompleted) exited.complete(0);
    return true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class FakeRuntime extends BackendRuntime {
  @override
  final Map<String, String> environment = {};
  @override
  bool supportsEmbeddedBackend = true;
  final process = FakeProcess();
  Future<bool> Function(Uri)? onProbe;
  var probes = 0;
  var starts = 0;
  List<String>? lastArguments;
  String? lastWorkingDirectory;
  File? configuration = File('test-fixtures/config.json').absolute;
  @override
  Future<bool> probe(Uri uri) async {
    probes++;
    return onProbe == null ? starts > 0 : onProbe!(uri);
  }

  @override
  Future<File?> locateExecutable() async => File('wxagent_backend.exe');
  @override
  Future<File?> locateConfig(File executable) async => configuration;
  @override
  Future<Process> start(
    File executable,
    List<String> arguments, {
    String? workingDirectory,
  }) async {
    starts++;
    lastArguments = List.of(arguments);
    lastWorkingDirectory = workingDirectory;
    return process;
  }
}

class DirectoryRuntime extends BackendRuntime {
  DirectoryRuntime(this.environment);
  @override
  final Map<String, String> environment;
}

AppController controllerFor(FakeRuntime runtime, {bool autoStart = false}) =>
    AppController(
      backendRuntime: runtime,
      autoStartBackend: autoStart,
      backendReadyTimeout: const Duration(milliseconds: 100),
      backendPollInterval: const Duration(milliseconds: 1),
      apiClientFactory: (url) => WxAgentApiClient(
        baseUrl: url,
        httpClient: MockClient((_) async => http.Response('{}', 200)),
      ),
    );

void main() {
  test('autoStartBackend false prevents startup during UI tests', () async {
    final runtime = FakeRuntime();
    final controller = controllerFor(runtime);
    addTearDown(controller.dispose);
    await Future<void>.delayed(Duration.zero);
    expect(runtime.probes, 0);
    expect(runtime.starts, 0);
    expect(controller.backendState, BackendState.idle);
  });

  test('launcher backend URL is used, explicit URL takes precedence', () {
    final runtime = FakeRuntime()
      ..environment['WXAGENT_BACKEND_URL'] = 'http://127.0.0.1:9200';
    final controller = controllerFor(runtime);
    expect(controller.baseUrl, 'http://127.0.0.1:9200');
    controller.dispose();
    final explicit = AppController(
      baseUrl: 'http://example.invalid:8001',
      autoStartBackend: false,
      backendRuntime: runtime,
    );
    expect(explicit.baseUrl, 'http://example.invalid:8001');
    explicit.dispose();
  });

  test(
    'concurrent readiness checks share work and supervise process exit',
    () async {
      final runtime = FakeRuntime();
      final controller = controllerFor(runtime);
      addTearDown(controller.dispose);
      final first = controller.ensureBackendReady();
      expect(identical(first, controller.ensureBackendReady()), isTrue);
      await first;
      expect(runtime.starts, 1);
      expect(runtime.lastArguments, [
        '--config',
        runtime.configuration!.path,
        'server',
        '--host',
        '127.0.0.1',
        '--port',
        '8000',
      ]);
      expect(runtime.lastWorkingDirectory, runtime.configuration!.parent.path);
      expect(controller.backendState, BackendState.running);
      runtime.process.exited.complete(12);
      await Future<void>.delayed(Duration.zero);
      expect(controller.backendState, BackendState.failed);
      expect(controller.backendMessage, contains('12'));
    },
  );

  test(
    'restart never duplicates an externally managed healthy service',
    () async {
      final runtime = FakeRuntime()..onProbe = (_) async => true;
      final controller = controllerFor(runtime);
      await controller.restartBackend();
      expect(controller.backendState, BackendState.running);
      expect(runtime.starts, 0);
      controller.dispose();
      expect(runtime.process.killCount, 0);
    },
  );

  test('unsupported platform can use an existing local backend', () async {
    final runtime = FakeRuntime()
      ..supportsEmbeddedBackend = false
      ..onProbe = (_) async => true;
    final controller = controllerFor(runtime);
    addTearDown(controller.dispose);
    await controller.ensureBackendReady();
    expect(controller.backendState, BackendState.running);
    expect(runtime.starts, 0);
  });

  test(
    'dispose during a pending probe does not start processes or notify',
    () async {
      final probe = Completer<bool>();
      final runtime = FakeRuntime()..onProbe = (_) => probe.future;
      final controller = controllerFor(runtime);
      var notifications = 0;
      controller.addListener(() => notifications++);
      final ready = controller.ensureBackendReady();
      await Future<void>.delayed(Duration.zero);
      controller.dispose();
      final countBeforeCompletion = notifications;
      probe.complete(false);
      await ready;
      expect(runtime.starts, 0);
      expect(notifications, countBeforeCompletion);
    },
  );

  test('dispose stops only a backend launched by this controller', () async {
    final runtime = FakeRuntime();
    final controller = controllerFor(runtime);
    await controller.ensureBackendReady();
    controller.dispose();
    await Future<void>.delayed(Duration.zero);
    expect(runtime.process.killCount, 1);
  });

  test(
    'locator recognizes the executable name emitted by the packager',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'wxagent-locator-',
      );
      final name = Platform.isWindows
          ? 'wxagent_backend.exe'
          : 'wxagent_backend';
      final executable = await File(
        '${directory.path}${Platform.pathSeparator}$name',
      ).writeAsString('test fixture');
      addTearDown(() async {
        await executable.delete();
        await directory.delete();
      });
      final runtime = DirectoryRuntime({
        'WX_AGENT_BACKEND_DIR': directory.path,
      });
      expect((await runtime.locateExecutable())?.path, executable.path);
    },
  );

  test('locator skips root launcher and discovers backend CLI', () async {
    final root = await Directory.systemTemp.createTemp('wxagent-cli-locator-');
    final previous = Directory.current;
    final launcher = await File(
      '${root.path}${Platform.pathSeparator}${Platform.isWindows ? 'WXAgent.exe' : 'wxagent'}',
    ).writeAsString('GUI launcher fixture');
    final backend = await Directory(
      '${root.path}${Platform.pathSeparator}backend',
    ).create();
    final cli = File(
      '${backend.path}${Platform.pathSeparator}${Platform.isWindows ? 'wxagent.exe' : 'wxagent'}',
    );
    addTearDown(() async {
      Directory.current = previous;
      if (await cli.exists()) await cli.delete();
      await launcher.delete();
      await backend.delete();
      await root.delete();
    });
    Directory.current = root;
    final runtime = DirectoryRuntime({});
    expect(await runtime.locateExecutable(), isNull);
    await cli.writeAsString('CLI fixture');
    expect((await runtime.locateExecutable())?.path, cli.path);
  });

  test(
    'package-root config is found when the executable is in backend',
    () async {
      final root = await Directory.systemTemp.createTemp('wxagent-config-');
      final backend = await Directory(
        '${root.path}${Platform.pathSeparator}backend',
      ).create();
      final executable = File(
        '${backend.path}${Platform.pathSeparator}wxagent_backend.exe',
      );
      final config = await File(
        '${root.path}${Platform.pathSeparator}config.json',
      ).writeAsString('{}');
      addTearDown(() async {
        await config.delete();
        await backend.delete();
        await root.delete();
      });
      final runtime = DirectoryRuntime({});
      expect((await runtime.locateConfig(executable))?.path, config.path);
    },
  );

  test(
    'config next to executable takes priority over package-root config',
    () async {
      final root = await Directory.systemTemp.createTemp('wxagent-config-');
      final backend = await Directory(
        '${root.path}${Platform.pathSeparator}backend',
      ).create();
      final rootConfig = await File(
        '${root.path}${Platform.pathSeparator}config.json',
      ).writeAsString('{}');
      final nearbyConfig = await File(
        '${backend.path}${Platform.pathSeparator}config.json',
      ).writeAsString('{}');
      addTearDown(() async {
        await nearbyConfig.delete();
        await rootConfig.delete();
        await backend.delete();
        await root.delete();
      });
      final runtime = DirectoryRuntime({});
      final executable = File(
        '${backend.path}${Platform.pathSeparator}wxagent_backend.exe',
      );
      expect((await runtime.locateConfig(executable))?.path, nearbyConfig.path);
    },
  );

  test(
    'explicit config wins and a missing override never falls back silently',
    () async {
      final root = await Directory.systemTemp.createTemp('wxagent-config-');
      final rootConfig = await File(
        '${root.path}${Platform.pathSeparator}config.json',
      ).writeAsString('{}');
      final overrideConfig = await File(
        '${root.path}${Platform.pathSeparator}custom.json',
      ).writeAsString('{}');
      addTearDown(() async {
        await rootConfig.delete();
        await root.delete();
      });
      final runtime = DirectoryRuntime({
        'WX_AGENT_CONFIG': overrideConfig.path,
      });
      final executable = File(
        '${root.path}${Platform.pathSeparator}wxagent_backend.exe',
      );
      expect(
        (await runtime.locateConfig(executable))?.path,
        overrideConfig.path,
      );
      await overrideConfig.delete();
      await expectLater(
        runtime.locateConfig(executable),
        throwsA(isA<FileSystemException>()),
      );
    },
  );

  test('missing configuration fails before starting a backend', () async {
    final runtime = FakeRuntime()..configuration = null;
    final controller = controllerFor(runtime);
    addTearDown(controller.dispose);
    await controller.ensureBackendReady();
    expect(runtime.starts, 0);
    expect(controller.backendState, BackendState.failed);
    expect(controller.backendMessage, contains('config.json'));
  });

  test('remote addresses never launch a local backend', () async {
    final runtime = FakeRuntime();
    final controller = controllerFor(runtime);
    addTearDown(controller.dispose);
    controller.setBaseUrl('https://example.invalid');
    await controller.ensureBackendReady();
    expect(controller.backendState, BackendState.skipped);
    expect(runtime.probes, 0);
    expect(runtime.starts, 0);
  });
}
