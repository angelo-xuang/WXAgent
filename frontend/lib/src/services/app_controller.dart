import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:path/path.dart' as p;

import 'wx_agent_api.dart';

enum BackendState { idle, checking, starting, running, failed, skipped }

/// Keeps process and filesystem access separate from application state.
/// Other platforms can connect to an existing backend without a bundled binary.
class BackendRuntime {
  const BackendRuntime();

  bool get supportsEmbeddedBackend => Platform.isWindows;
  Map<String, String> get environment => Platform.environment;

  Future<bool> probe(Uri uri) async {
    final client = http.Client();
    try {
      final response = await client
          .get(uri)
          .timeout(const Duration(seconds: 2));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    } finally {
      client.close();
    }
  }

  Future<File?> locateExecutable() async {
    final override = environment['WX_AGENT_BACKEND_EXE']?.trim();
    if (override != null && override.isNotEmpty) {
      final file = File(override);
      if (await file.exists()) return file.absolute;
    }
    final directories = <String>{
      File(Platform.resolvedExecutable).parent.path,
      File(Platform.resolvedExecutable).parent.parent.path,
      Directory.current.path,
    };
    if (Platform.script.scheme == 'file') {
      directories.add(File.fromUri(Platform.script).parent.path);
    }
    final overrideDir = environment['WX_AGENT_BACKEND_DIR']?.trim();
    if (overrideDir != null && overrideDir.isNotEmpty) {
      directories.add(overrideDir);
    }
    for (final directory in directories.toList()) {
      directories.addAll([
        p.join(directory, 'backend'),
        p.join(directory, 'bin'),
      ]);
    }
    final names = Platform.isWindows
        ? const ['wxagent_backend.exe', 'wx_agent_backend.exe']
        : const ['wxagent_backend', 'wx_agent_backend'];
    for (final directory in directories) {
      for (final name in names) {
        final file = File(p.join(directory, name));
        if (await file.exists()) return file.absolute;
      }
    }
    return null;
  }

  Future<File?> locateConfig(File executable) async {
    final override = environment['WX_AGENT_CONFIG']?.trim();
    if (override != null && override.isNotEmpty) {
      final file = File(override).absolute;
      if (!await file.exists()) {
        throw FileSystemException('WX_AGENT_CONFIG 指向的配置文件不存在', file.path);
      }
      return file;
    }
    final executableDirectory = executable.absolute.parent;
    for (final directory in [executableDirectory, executableDirectory.parent]) {
      final file = File(p.join(directory.path, 'config.json'));
      if (await file.exists()) return file;
    }
    return null;
  }

  Future<Process> start(
    File executable,
    List<String> arguments, {
    String? workingDirectory,
  }) => Process.start(
    executable.absolute.path,
    arguments,
    workingDirectory: workingDirectory ?? executable.absolute.parent.path,
    // Detached processes do not expose exitCode and cannot be supervised.
    mode: ProcessStartMode.normal,
  );
}

class AppController extends ChangeNotifier {
  AppController({
    String? baseUrl,
    bool autoStartBackend = true,
    WxAgentApiClient Function(String)? apiClientFactory,
    BackendRuntime backendRuntime = const BackendRuntime(),
    Duration backendReadyTimeout = const Duration(seconds: 30),
    Duration backendPollInterval = const Duration(seconds: 1),
  }) : _baseUrl = baseUrl?.trim().isNotEmpty == true
           ? baseUrl!.trim()
           : (backendRuntime.environment['WXAGENT_BACKEND_URL']
                         ?.trim()
                         .isNotEmpty ==
                     true
                 ? backendRuntime.environment['WXAGENT_BACKEND_URL']!.trim()
                 : 'http://127.0.0.1:8000'),
       _autoStartBackend = autoStartBackend,
       _apiClientFactory = apiClientFactory ?? _defaultApiClient,
       _runtime = backendRuntime,
       _readyTimeout = backendReadyTimeout,
       _pollInterval = backendPollInterval {
    _api = _apiClientFactory(_baseUrl);
    if (autoStartBackend) {
      Future.microtask(() => ensureBackendReady());
    }
  }

  static WxAgentApiClient _defaultApiClient(String url) =>
      WxAgentApiClient(baseUrl: url);

  final bool _autoStartBackend;
  final WxAgentApiClient Function(String) _apiClientFactory;
  final BackendRuntime _runtime;
  final Duration _readyTimeout;
  final Duration _pollInterval;
  late WxAgentApiClient _api;
  String _baseUrl;
  BackendState _backendState = BackendState.idle;
  String? _backendMessage;
  Process? _backendProcess;
  Future<void>? _backendFuture;
  bool _disposed = false;
  int _generation = 0;

  WxAgentApiClient get api => _api;
  String get baseUrl => _baseUrl;
  BackendState get backendState => _backendState;
  String? get backendMessage => _backendMessage;

  void setBaseUrl(String value) {
    final normalized = value.trim();
    if (_disposed || normalized.isEmpty || normalized == _baseUrl) return;
    _generation++;
    _baseUrl = normalized;
    _api.dispose();
    _api = _apiClientFactory(_baseUrl);
    if (_autoStartBackend) {
      unawaited(ensureBackendReady(forceRestart: true));
    } else {
      unawaited(_stopManagedBackend());
      _setBackendState(BackendState.idle, null);
    }
    notifyListeners();
  }

  Future<void> ensureBackendReady({bool forceRestart = false}) {
    if (_disposed) return Future.value();
    final pending = _backendFuture;
    if (pending != null) {
      if (!forceRestart) return pending;
      _generation++;
      return pending.then((_) => ensureBackendReady(forceRestart: true));
    }
    final generation = ++_generation;
    final future = Future<void>(
      () => _runEnsureBackendReady(generation, forceRestart: forceRestart),
    );
    _backendFuture = future;
    return future;
  }

  Future<void> restartBackend() => ensureBackendReady(forceRestart: true);

  bool _isCurrent(int generation) => !_disposed && generation == _generation;

  Future<void> _runEnsureBackendReady(
    int generation, {
    required bool forceRestart,
  }) async {
    try {
      if (!_isCurrent(generation)) return;
      if (forceRestart) await _stopManagedBackend();
      if (!_isCurrent(generation)) return;
      final uri = Uri.tryParse(_baseUrl);
      final local =
          uri != null &&
          (uri.scheme == 'http' || uri.scheme == 'https') &&
          const ['127.0.0.1', 'localhost', '::1'].contains(uri.host);
      final disabled =
          _runtime.environment['WX_AGENT_DISABLE_EMBEDDED_BACKEND']
              ?.toLowerCase() ==
          'true';
      if (!local || disabled) {
        await _stopManagedBackend();
        if (!_isCurrent(generation)) return;
        _setBackendState(BackendState.skipped, '使用配置的后端地址，已跳过内置服务启动。');
        return;
      }
      _setBackendState(BackendState.checking, '检测本地后端服务…');
      // A restart must not spawn a duplicate when the service is externally owned.
      if (await _runtime.probe(_statusUri)) {
        if (_isCurrent(generation)) {
          _setBackendState(BackendState.running, '检测到已有后端服务。');
        }
        return;
      }
      if (!_isCurrent(generation)) return;
      if (!_runtime.supportsEmbeddedBackend) {
        _setBackendState(BackendState.skipped, '此平台请先启动后端服务，或连接其他设备上的后端。');
        return;
      }
      final executable = await _runtime.locateExecutable();
      if (!_isCurrent(generation)) return;
      if (executable == null) {
        _setBackendState(
          BackendState.failed,
          '未找到 wxagent_backend.exe，请将其放在应用目录或设置 WX_AGENT_BACKEND_EXE。',
        );
        return;
      }
      final config = await _runtime.locateConfig(executable);
      if (!_isCurrent(generation)) return;
      if (config == null) {
        _setBackendState(
          BackendState.failed,
          '未找到 config.json，请将其放在后端目录或应用包根目录，或设置 WX_AGENT_CONFIG。',
        );
        return;
      }
      final host = uri.host;
      final port = uri.hasPort
          ? uri.port
          : (uri.scheme == 'https' ? 443 : 8000);
      final process = await _runtime.start(executable, [
        '--config',
        config.absolute.path,
        'server',
        '--host',
        host,
        '--port',
        '$port',
      ], workingDirectory: config.absolute.parent.path);
      if (!_isCurrent(generation)) {
        process.kill();
        unawaited(process.stdout.drain<void>().catchError((Object _) {}));
        unawaited(process.stderr.drain<void>().catchError((Object _) {}));
        return;
      }
      _backendProcess = process;
      // Draining both pipes prevents a verbose backend from blocking on a full pipe.
      unawaited(process.stdout.drain<void>().catchError((Object _) {}));
      unawaited(process.stderr.drain<void>().catchError((Object _) {}));
      _setBackendState(
        BackendState.starting,
        '已启动 ${p.basename(executable.path)}，等待服务就绪…',
      );
      unawaited(
        process.exitCode.then((code) {
          if (!_disposed && identical(_backendProcess, process)) {
            _backendProcess = null;
            _setBackendState(BackendState.failed, '内置后端进程意外退出（exit $code）。');
          }
        }),
      );
      final timer = Stopwatch()..start();
      while (_isCurrent(generation) && identical(_backendProcess, process)) {
        if (await _runtime.probe(_statusUri)) {
          if (_isCurrent(generation) && identical(_backendProcess, process)) {
            _setBackendState(BackendState.running, '后端已就绪。');
          }
          return;
        }
        if (timer.elapsed >= _readyTimeout) break;
        await Future<void>.delayed(_pollInterval);
      }
      if (!_isCurrent(generation) || !identical(_backendProcess, process)) {
        return;
      }
      _setBackendState(
        BackendState.failed,
        '后端在 ${_readyTimeout.inSeconds} 秒内未响应 /status。',
      );
      await _stopManagedBackend();
    } catch (error, stackTrace) {
      if (_isCurrent(generation)) {
        _setBackendState(BackendState.failed, '自动启动后端失败：$error');
        await _stopManagedBackend();
      }
      debugPrint('Backend bootstrap failed: $error\n$stackTrace');
    } finally {
      _backendFuture = null;
    }
  }

  Uri get _statusUri =>
      Uri.parse('${_baseUrl.replaceFirst(RegExp(r'/+$'), '')}/status');

  void _setBackendState(BackendState state, String? message) {
    if (_disposed || (_backendState == state && _backendMessage == message)) {
      return;
    }
    _backendState = state;
    _backendMessage = message;
    notifyListeners();
  }

  Future<void> _stopManagedBackend() async {
    final process = _backendProcess;
    _backendProcess = null;
    if (process == null) return;
    try {
      process.kill();
      await process.exitCode.timeout(const Duration(seconds: 5));
    } on TimeoutException {
      process.kill(ProcessSignal.sigkill);
    } catch (_) {
      // The OS may already have reaped the process.
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _generation++;
    unawaited(_stopManagedBackend());
    _api.dispose();
    super.dispose();
  }
}
