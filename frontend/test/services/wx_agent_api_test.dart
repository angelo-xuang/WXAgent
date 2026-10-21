import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:wx_agent_app/src/services/wx_agent_api.dart';

class StreamingClient extends http.BaseClient {
  StreamingClient(this.handler);
  final Future<http.StreamedResponse> Function(http.BaseRequest) handler;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) =>
      handler(request);
}

void main() {
  for (final summary in [true, false]) {
    test(
      '${summary ? 'summary' : 'refresh'} parses split UTF-8, CRLF and multiline SSE',
      () async {
        final bytes = utf8.encode(
          ': heartbeat\r\nevent:log\r\ndata:{"content":\r\ndata: "你好", "step": 1}\r\n\r\nevent: result\ndata: {"content":"完成"}',
        );
        final client = WxAgentApiClient(
          baseUrl: 'http://test.invalid',
          httpClient: StreamingClient((request) async {
            expect(request.headers['Accept'], 'text/event-stream');
            return http.StreamedResponse(
              Stream.fromIterable(bytes.map((byte) => [byte])),
              200,
            );
          }),
        );
        addTearDown(client.dispose);
        final events =
            await (summary
                    ? client.runSummaryStream()
                    : client.runFullRefreshStream())
                .toList();
        expect(events.map((event) => event.type), ['log', 'result']);
        expect(events.first.content, '你好');
        expect(events.first.step, 1);
        expect(events.last.content, '完成');
      },
    );
  }

  test(
    'malformed stream data surfaces an error instead of reporting success',
    () async {
      final client = WxAgentApiClient(
        baseUrl: 'http://test.invalid',
        httpClient: StreamingClient(
          (_) async => http.StreamedResponse(
            Stream.value(utf8.encode('event: result\ndata: {bad}\n\n')),
            200,
          ),
        ),
      );
      addTearDown(client.dispose);
      await expectLater(
        client.runSummaryStream(),
        emitsError(isA<FormatException>()),
      );
    },
  );

  test(
    'truncated stream cannot be mistaken for a completed operation',
    () async {
      final client = WxAgentApiClient(
        baseUrl: 'http://test.invalid',
        httpClient: StreamingClient(
          (_) async => http.StreamedResponse(
            Stream.value(
              utf8.encode('event: log\ndata: {"content":"处理中"}\n\n'),
            ),
            200,
          ),
        ),
      );
      addTearDown(client.dispose);
      await expectLater(
        client.runSummaryStream(),
        emitsInOrder([
          isA<StreamEvent>(),
          emitsError(isA<FormatException>()),
          emitsDone,
        ]),
      );
    },
  );

  for (final summary in [true, false]) {
    test(
      '${summary ? 'summary' : 'refresh'} done without result remains incomplete',
      () async {
        final client = WxAgentApiClient(
          baseUrl: 'http://test.invalid',
          httpClient: StreamingClient(
            (_) async => http.StreamedResponse(
              Stream.value(
                utf8.encode('event: done\ndata: {"content":"生成结束"}\n\n'),
              ),
              200,
            ),
          ),
        );
        addTearDown(client.dispose);
        final stream = summary
            ? client.runSummaryStream()
            : client.runFullRefreshStream();
        await expectLater(
          stream,
          emitsInOrder([
            isA<StreamEvent>().having((event) => event.type, 'type', 'done'),
            emitsError(isA<FormatException>()),
            emitsDone,
          ]),
        );
      },
    );
  }

  test('done followed by result completes successfully', () async {
    final client = WxAgentApiClient(
      baseUrl: 'http://test.invalid',
      httpClient: StreamingClient(
        (_) async => http.StreamedResponse(
          Stream.value(
            utf8.encode(
              'event: done\ndata: {}\n\nevent: result\ndata: {"content":"最终总结"}\n\n',
            ),
          ),
          200,
        ),
      ),
    );
    addTearDown(client.dispose);
    final events = await client.runSummaryStream().toList();
    expect(events.map((event) => event.type), ['done', 'result']);
    expect(events.last.content, '最终总结');
  });

  test(
    'cancellation aborts request even before response headers arrive',
    () async {
      final sent = Completer<http.AbortableRequest>();
      final response = Completer<http.StreamedResponse>();
      final client = WxAgentApiClient(
        baseUrl: 'http://test.invalid',
        httpClient: StreamingClient((request) {
          sent.complete(request as http.AbortableRequest);
          return response.future;
        }),
      );
      addTearDown(client.dispose);
      final subscription = client.runSummaryStream().listen((_) {});
      final request = await sent.future;
      await subscription.cancel();
      await request.abortTrigger;
      response.complete(http.StreamedResponse(const Stream.empty(), 200));
    },
  );

  test(
    'cancelling a running stream closes its response subscription',
    () async {
      var cancelled = false;
      final bytes = StreamController<List<int>>(
        onCancel: () => cancelled = true,
      );
      final first = Completer<void>();
      late http.AbortableRequest sent;
      final client = WxAgentApiClient(
        baseUrl: 'http://test.invalid',
        httpClient: StreamingClient((request) async {
          sent = request as http.AbortableRequest;
          return http.StreamedResponse(bytes.stream, 200);
        }),
      );
      addTearDown(client.dispose);
      final subscription = client.runSummaryStream().listen(
        (_) => first.complete(),
      );
      bytes.add(utf8.encode('event: log\ndata: {"content":"开始"}\n\n'));
      await first.future;
      await subscription.cancel();
      await sent.abortTrigger;
      expect(cancelled, isTrue);
      await bytes.close();
    },
  );

  test('stream connection timeout aborts the request', () async {
    final response = Completer<http.StreamedResponse>();
    late http.AbortableRequest sent;
    final client = WxAgentApiClient(
      baseUrl: 'http://test.invalid',
      connectTimeout: const Duration(milliseconds: 20),
      httpClient: StreamingClient((request) {
        sent = request as http.AbortableRequest;
        return response.future;
      }),
    );
    addTearDown(client.dispose);
    await expectLater(
      client.runSummaryStream(),
      emitsError(isA<TimeoutException>()),
    );
    await sent.abortTrigger;
    response.complete(http.StreamedResponse(const Stream.empty(), 200));
  });

  test('ordinary requests time out and signal cancellation', () async {
    final sent = Completer<http.AbortableRequest>();
    final response = Completer<http.StreamedResponse>();
    final client = WxAgentApiClient(
      baseUrl: 'http://test.invalid',
      requestTimeout: const Duration(milliseconds: 20),
      httpClient: StreamingClient((request) {
        sent.complete(request as http.AbortableRequest);
        return response.future;
      }),
    );
    addTearDown(client.dispose);
    await expectLater(client.getStatus(), throwsA(isA<TimeoutException>()));
    await (await sent.future).abortTrigger;
    response.complete(http.StreamedResponse(const Stream.empty(), 200));
  });

  test(
    'idle stream timeout surfaces and cancels the underlying response',
    () async {
      final bytes = StreamController<List<int>>();
      final client = WxAgentApiClient(
        baseUrl: 'http://test.invalid',
        streamIdleTimeout: const Duration(milliseconds: 20),
        httpClient: StreamingClient(
          (_) async => http.StreamedResponse(bytes.stream, 200),
        ),
      );
      addTearDown(client.dispose);
      await expectLater(
        client.runFullRefreshStream(),
        emitsError(isA<TimeoutException>()),
      );
      await bytes.close();
    },
  );

  test('HTTP failures retain status code and UTF-8 body', () async {
    final client = WxAgentApiClient(
      baseUrl: 'http://test.invalid',
      httpClient: MockClient(
        (_) async => http.Response(
          '服务暂不可用',
          503,
          headers: {'content-type': 'application/json; charset=utf-8'},
        ),
      ),
    );
    addTearDown(client.dispose);
    await expectLater(
      client.getStatus(),
      throwsA(isA<HttpException>().having((e) => e.statusCode, 'status', 503)),
    );
  });

  test('dispose aborts pending work and rejects new requests', () async {
    final sent = Completer<http.AbortableRequest>();
    final response = Completer<http.StreamedResponse>();
    final client = WxAgentApiClient(
      baseUrl: 'http://test.invalid',
      httpClient: StreamingClient((request) {
        sent.complete(request as http.AbortableRequest);
        return response.future;
      }),
    );
    final subscription = client.runSummaryStream().listen((_) {});
    final request = await sent.future;
    client.dispose();
    await request.abortTrigger;
    await subscription.cancel();
    response.complete(http.StreamedResponse(const Stream.empty(), 200));
    await expectLater(client.getStatus(), throwsStateError);
  });
}
