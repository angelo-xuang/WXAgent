import 'dart:convert';
import 'dart:async';

import 'package:http/http.dart' as http;

import '../models/models.dart';

/// SSE 流式事件
class StreamEvent {
  StreamEvent({
    required this.type,
    required this.content,
    this.step,
    this.total,
    this.payload,
  });

  factory StreamEvent.fromJson(Map<String, dynamic> json) {
    final data = Map<String, dynamic>.from(json);
    final type = data.remove('type') as String? ?? '';
    final content = data.remove('content')?.toString() ?? '';
    final step = (data.remove('step') as num?)?.toInt();
    final total = (data.remove('total') as num?)?.toInt();
    return StreamEvent(
      type: type,
      content: content,
      step: step,
      total: total,
      payload: data.isEmpty ? null : data,
    );
  }

  final String type; // log, progress, done, error, result
  final String content;
  final int? step;
  final int? total;
  final Map<String, dynamic>? payload;
}

class WxAgentApiClient {
  WxAgentApiClient({
    required this.baseUrl,
    http.Client? httpClient,
    this.requestTimeout = const Duration(minutes: 5),
    this.connectTimeout = const Duration(seconds: 15),
    this.streamIdleTimeout = const Duration(minutes: 5),
  }) : _client = httpClient ?? http.Client();

  final String baseUrl;
  final http.Client _client;
  final Duration requestTimeout;
  final Duration connectTimeout;
  final Duration streamIdleTimeout;
  final Set<Completer<void>> _pendingRequests = {};
  bool _disposed = false;

  Future<KeyInfo> refreshKey({
    bool autoLaunch = false,
    int waitSeconds = 90,
    int pollInterval = 3,
  }) async {
    final response = await post(
      '/refresh-key',
      body: {
        'auto_launch': autoLaunch,
        'wait_seconds': waitSeconds,
        'poll_interval': pollInterval,
      },
    );
    return KeyInfo.fromJson(response);
  }

  Future<List<SessionMeta>> listSessions() async {
    final response = await get('/sessions');
    final list = response['data'] as List? ?? const [];
    return list
        .map(
          (item) => SessionMeta.fromJson((item as Map).cast<String, dynamic>()),
        )
        .toList();
  }

  Future<bool> triggerExport({
    List<String>? extraArgs,
    bool silent = true,
  }) async {
    final response = await post(
      '/export',
      body: {'extra_args': extraArgs, 'silent': silent},
    );
    return response['success'] == true;
  }

  Future<Map<String, dynamic>> runFullRefresh({
    bool autoLaunchKey = true,
    int waitSeconds = 90,
    int pollInterval = 3,
    List<String>? exportArgs,
  }) {
    return post(
      '/full-refresh',
      body: {
        'auto_launch_key': autoLaunchKey,
        'wait_seconds': waitSeconds,
        'poll_interval': pollInterval,
        'export_args': exportArgs,
      },
    );
  }

  /// SSE 流式导出，实时返回日志
  Stream<StreamEvent> runFullRefreshStream({
    bool autoLaunchKey = true,
    int waitSeconds = 90,
    int pollInterval = 3,
    List<String>? exportArgs,
  }) {
    final body = <String, dynamic>{
      'auto_launch_key': autoLaunchKey,
      'wait_seconds': waitSeconds,
      'poll_interval': pollInterval,
      'export_args': exportArgs,
    };

    return _streamEvents('/full-refresh/stream', body);
  }

  Future<Map<String, dynamic>> indexStatus() => get('/index/status');

  Future<Map<String, dynamic>> importIndex({String? exportDir}) => post(
    '/index/import',
    body: {
      if (exportDir != null && exportDir.trim().isNotEmpty)
        'export_dir': exportDir.trim(),
    },
  );

  Future<MessagePage> queryMessages({
    String? sessionId,
    String? startDate,
    String? endDate,
    String? keyword,
    int offset = 0,
    int limit = 50,
  }) async => MessagePage.fromJson(
    await get(
      '/messages',
      query: {
        'session_id': sessionId,
        'start_date': startDate,
        'end_date': endDate,
        'keyword': keyword,
        'offset': offset,
        'limit': limit,
      },
    ),
  );

  Future<Map<String, dynamic>> fetchConfig() => get('/config');

  Future<Map<String, dynamic>> updateConfig(Map<String, dynamic> values) =>
      put('/config', body: {'values': values});

  Future<Map<String, dynamic>> reloadConfig() => post('/config/reload');

  Future<Map<String, dynamic>> getStatus() => get('/status');

  Uri _uri(String path, [Map<String, dynamic>? query]) {
    final normalizedBase = baseUrl.endsWith('/')
        ? baseUrl.substring(0, baseUrl.length - 1)
        : baseUrl;
    final normalizedPath = path.startsWith('/') ? path : '/$path';
    final uri = Uri.parse('$normalizedBase$normalizedPath');
    if (query == null || query.isEmpty) {
      return uri;
    }
    final sanitized = <String, String>{};
    query.forEach((key, value) {
      if (value == null) return;
      if (value is Iterable) {
        final items = value
            .where((element) => element != null)
            .map((element) => element.toString())
            .where((element) => element.isNotEmpty)
            .toList();
        if (items.isEmpty) return;
        sanitized[key] = items.join(',');
        return;
      }
      final text = value.toString();
      if (text.isEmpty) {
        return;
      }
      sanitized[key] = text;
    });
    return uri.replace(queryParameters: sanitized);
  }

  Future<Map<String, dynamic>> get(
    String path, {
    Map<String, dynamic>? query,
  }) => _request('GET', path, query: query);

  Future<Map<String, dynamic>> post(
    String path, {
    Map<String, dynamic>? query,
    Object? body,
  }) => _request('POST', path, query: query, body: body);

  Future<Map<String, dynamic>> put(
    String path, {
    Map<String, dynamic>? query,
    Object? body,
  }) => _request('PUT', path, query: query, body: body);

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, dynamic>? query,
    Object? body,
  }) async {
    if (_disposed) throw StateError('API client has been disposed');
    final abort = Completer<void>();
    _pendingRequests.add(abort);
    final request = http.AbortableRequest(
      method,
      _uri(path, query),
      abortTrigger: abort.future,
    );
    if (body != null) {
      request.headers['Content-Type'] = 'application/json';
      request.body = jsonEncode(body);
    }
    try {
      final response = await _client
          .send(request)
          .then(http.Response.fromStream)
          .timeout(requestTimeout);
      return _decode(response);
    } finally {
      _pendingRequests.remove(abort);
      if (!abort.isCompleted) abort.complete();
    }
  }

  Stream<StreamEvent> _streamEvents(String path, Object body) {
    final abort = Completer<void>();
    StreamSubscription<StreamEvent>? subscription;
    late StreamController<StreamEvent> controller;
    var cancelled = false;
    var receivedCompletion = false;

    Future<void> start() async {
      try {
        if (_disposed) throw StateError('API client has been disposed');
        _pendingRequests.add(abort);
        final request = http.AbortableRequest(
          'POST',
          _uri(path),
          abortTrigger: abort.future,
        );
        request.headers.addAll({
          'Content-Type': 'application/json',
          'Accept': 'text/event-stream',
        });
        request.body = jsonEncode(body);
        final response = await _client.send(request).timeout(connectTimeout);
        if (cancelled) return;
        if (response.statusCode >= 400) {
          final text = await response.stream.bytesToString().timeout(
            requestTimeout,
          );
          throw HttpException(response.statusCode, text);
        }
        subscription = _parseEvents(response.stream.timeout(streamIdleTimeout))
            .listen(
              (event) {
                // The backend emits done before saving and sending its result.
                // Only result/error proves that this operation reached its end.
                if (const ['result', 'error'].contains(event.type)) {
                  receivedCompletion = true;
                }
                controller.add(event);
              },
              onError: (Object error, StackTrace stack) {
                if (!cancelled) controller.addError(error, stack);
                unawaited(controller.close());
                _pendingRequests.remove(abort);
                if (!abort.isCompleted) abort.complete();
              },
              onDone: () {
                if (!cancelled && !receivedCompletion) {
                  controller.addError(
                    const FormatException('服务连接提前结束，未收到完成结果，请重试。'),
                  );
                }
                _pendingRequests.remove(abort);
                if (!abort.isCompleted) abort.complete();
                unawaited(controller.close());
              },
              cancelOnError: true,
            );
      } catch (error, stack) {
        if (!cancelled) controller.addError(error, stack);
        _pendingRequests.remove(abort);
        if (!abort.isCompleted) abort.complete();
        unawaited(controller.close());
      }
    }

    controller = StreamController<StreamEvent>(
      onListen: () => unawaited(start()),
      onPause: () => subscription?.pause(),
      onResume: () => subscription?.resume(),
      onCancel: () async {
        cancelled = true;
        _pendingRequests.remove(abort);
        if (!abort.isCompleted) abort.complete();
        await subscription?.cancel();
      },
    );
    return controller.stream;
  }

  /// LineSplitter handles LF/CRLF and network chunks split within a line or UTF-8.
  /// Malformed events surface as errors so an incomplete operation cannot look successful.
  Stream<StreamEvent> _parseEvents(Stream<List<int>> bytes) {
    String? type;
    final data = <String>[];

    void emit(EventSink<StreamEvent> sink) {
      if (data.isEmpty) return;
      try {
        final decoded = jsonDecode(data.join('\n'));
        if (decoded is! Map<String, dynamic>) {
          throw const FormatException('SSE event data must be a JSON object');
        }
        if (type != null && type!.isNotEmpty) decoded['type'] = type;
        sink.add(StreamEvent.fromJson(decoded));
      } catch (error, stack) {
        sink.addError(error, stack);
      }
    }

    return bytes
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .transform(
          StreamTransformer<String, StreamEvent>.fromHandlers(
            handleData: (line, sink) {
              if (line.isEmpty) {
                emit(sink);
                data.clear();
                type = null;
                return;
              }
              if (line.startsWith(':')) return;
              final separator = line.indexOf(':');
              final field = separator < 0 ? line : line.substring(0, separator);
              var value = separator < 0 ? '' : line.substring(separator + 1);
              if (value.startsWith(' ')) value = value.substring(1);
              if (field == 'event') type = value;
              if (field == 'data') data.add(value);
            },
            handleDone: (sink) {
              emit(sink);
              sink.close();
            },
          ),
        );
  }

  Map<String, dynamic> _decode(http.Response response) {
    if (response.statusCode >= 400) {
      throw HttpException(response.statusCode, response.body);
    }
    if (response.body.isEmpty) {
      return const {};
    }
    final decoded = jsonDecode(utf8.decode(response.bodyBytes));
    if (decoded is Map<String, dynamic>) {
      return decoded;
    }
    return {'data': decoded};
  }

  void dispose() {
    if (_disposed) return;
    _disposed = true;
    for (final abort in _pendingRequests.toList()) {
      if (!abort.isCompleted) abort.complete();
    }
    _pendingRequests.clear();
    _client.close();
  }
}

class HttpException implements Exception {
  HttpException(this.statusCode, this.body);

  final int statusCode;
  final String body;

  @override
  String toString() => 'HTTP $statusCode: $body';
}
