class KeyInfo {
  KeyInfo({
    this.dbKey,
    this.imageXorKey,
    this.imageAesKey,
    this.timestamp,
    this.raw = const {},
  });

  factory KeyInfo.fromJson(Map<String, dynamic> json) {
    return KeyInfo(
      dbKey: json['db_key'] as String?,
      imageXorKey: _toInt(json['image_xor_key']),
      imageAesKey: json['image_aes_key'] as String?,
      timestamp: json['timestamp'] as String?,
      raw: Map<String, dynamic>.from(json['raw'] as Map? ?? const {}),
    );
  }

  final String? dbKey;
  final int? imageXorKey;
  final String? imageAesKey;
  final String? timestamp;
  final Map<String, dynamic> raw;
}

class SessionMeta {
  SessionMeta({
    required this.sessionId,
    required this.displayName,
    this.sessionType,
    this.category,
    this.messages = 0,
    this.file,
    this.stateFile,
    this.stateFileExists = false,
    this.lastExportTime,
    this.totalExported = 0,
    this.lastAddedCount,
    this.firstTimestamp,
    this.lastTimestamp,
    this.sentMessages,
    this.receivedMessages,
    this.participants,
    // 新增统计字段
    this.textMessages,
    this.imageMessages,
    this.voiceMessages,
    this.videoMessages,
    this.otherMessages,
    this.activeDays,
    this.firstMessage,
    this.lastMessage,
  });

  factory SessionMeta.fromJson(Map<String, dynamic> json) {
    return SessionMeta(
      sessionId: _asString(
        json['session_id'] ?? json['sessionId'],
        fallback: '',
      ),
      displayName: _asString(
        json['display_name'] ?? json['displayName'],
        fallback: '未命名会话',
      ),
      sessionType: json['session_type'] as String?,
      category: json['category'] as String?,
      messages: _toInt(json['messages']) ?? 0,
      file: _asNullableString(json['file']),
      stateFile: _asNullableString(json['state_file']),
      stateFileExists: json['state_file_exists'] == true,
      lastExportTime: _asNullableString(json['last_export_time']),
      totalExported: _toInt(json['total_exported']) ?? 0,
      lastAddedCount: _toInt(json['last_added_count']),
      firstTimestamp: _asNullableString(json['first_timestamp']),
      lastTimestamp: _asNullableString(json['last_timestamp']),
      sentMessages: _toInt(json['sent_messages']),
      receivedMessages: _toInt(json['received_messages']),
      participants: (json['participants'] as List?)
          ?.map((item) => (item as Map).cast<String, dynamic>())
          .toList(),
      // 新增统计字段
      textMessages: _toInt(json['text_messages']),
      imageMessages: _toInt(json['image_messages']),
      voiceMessages: _toInt(json['voice_messages']),
      videoMessages: _toInt(json['video_messages']),
      otherMessages: _toInt(json['other_messages']),
      activeDays: _toInt(json['active_days']),
      firstMessage: _toInt(json['first_message']),
      lastMessage: _toInt(json['last_message']),
    );
  }

  final String sessionId;
  final String displayName;
  final String? sessionType;
  final String? category;
  final int messages;
  final String? file;
  final String? stateFile;
  final bool stateFileExists;
  final String? lastExportTime;
  final int totalExported;
  final int? lastAddedCount;
  final String? firstTimestamp;
  final String? lastTimestamp;
  final int? sentMessages;
  final int? receivedMessages;
  final List<Map<String, dynamic>>? participants;
  // 新增统计字段
  final int? textMessages;
  final int? imageMessages;
  final int? voiceMessages;
  final int? videoMessages;
  final int? otherMessages;
  final int? activeDays;
  final int? firstMessage; // Unix timestamp
  final int? lastMessage; // Unix timestamp
}

class AnalysisResult {
  AnalysisResult({
    required this.top,
    required this.breakdown,
    required this.totalMessages,
    required this.sessionCount,
    this.windowStart,
    this.windowEnd,
    this.hourlyDistribution = const [],
  });

  factory AnalysisResult.fromJson(Map<String, dynamic> json) {
    final topList = (json['top'] as List? ?? [])
        .map((e) => SessionMeta.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
    final window =
        (json['window'] as Map?)?.cast<String, dynamic>() ?? const {};
    return AnalysisResult(
      top: topList,
      breakdown: Map<String, dynamic>.from(
        json['breakdown'] as Map? ?? const {},
      ),
      totalMessages: _toInt(json['total_messages']) ?? 0,
      sessionCount: _toInt(json['session_count']) ?? topList.length,
      windowStart: window['start'] as String?,
      windowEnd: window['end'] as String?,
      hourlyDistribution: (json['hourly_distribution'] as List? ?? const [])
          .map((e) => _toInt(e) ?? 0)
          .toList(),
    );
  }

  final List<SessionMeta> top;
  final Map<String, dynamic> breakdown;
  final int totalMessages;
  final int sessionCount;
  final String? windowStart;
  final String? windowEnd;
  final List<int> hourlyDistribution;
}

class SummaryHistoryEntry {
  SummaryHistoryEntry({
    required this.name,
    required this.path,
    this.modified,
    this.sizeBytes,
  });

  factory SummaryHistoryEntry.fromJson(Map<String, dynamic> json) {
    return SummaryHistoryEntry(
      name: json['name'] as String? ?? '',
      path: json['path'] as String? ?? '',
      modified: json['modified'] as String?,
      sizeBytes: _toInt(json['size']),
    );
  }

  final String name;
  final String path;
  final String? modified;
  final int? sizeBytes;
}

class ExportHistoryRecord {
  ExportHistoryRecord({
    required this.exportTime,
    this.filePath,
    this.addedCount,
  });

  factory ExportHistoryRecord.fromJson(Map<String, dynamic> json) {
    return ExportHistoryRecord(
      exportTime: json['exportTime'] as String? ?? '',
      filePath: json['filePath'] as String?,
      addedCount: _toInt(json['addedCount']),
    );
  }

  final String exportTime;
  final String? filePath;
  final int? addedCount;
}

class SummaryRunResult {
  SummaryRunResult({
    required this.messages,
    required this.sessions,
    required this.outputPath,
    required this.durationSeconds,
    required this.content,
    this.mode,
    this.chunkCount,
    this.logs,
  });

  factory SummaryRunResult.fromJson(Map<String, dynamic> json) {
    return SummaryRunResult(
      messages: _toInt(json['messages']) ?? _toInt(json['total_messages']) ?? 0,
      sessions: _toInt(json['sessions']) ?? _toInt(json['total_sessions']) ?? 0,
      outputPath:
          (json['summary_file'] ??
                  json['summary_output'] ??
                  json['output_path'] ??
                  json['output'] ??
                  json['path'])
              as String?,
      durationSeconds: _toInt(json['duration']) ?? 0,
      content: json['summary'] as String? ?? json['content'] as String? ?? '',
      mode: json['mode'] as String?,
      chunkCount: _toInt(json['chunk_count']),
      logs: (json['logs'] as List<dynamic>?)?.map((e) => e.toString()).toList(),
    );
  }

  final int messages;
  final int sessions;
  final String? outputPath;
  final int durationSeconds;
  final String content;
  final String? mode;
  final int? chunkCount;
  final List<String>? logs;
}

int? _toInt(dynamic value) {
  if (value == null) return null;
  if (value is int) return value;
  if (value is double) return value.toInt();
  if (value is String) {
    return int.tryParse(value);
  }
  return null;
}

String _asString(dynamic value, {String fallback = ''}) {
  if (value == null) return fallback;
  final text = value.toString();
  return text.isEmpty ? fallback : text;
}

String? _asNullableString(dynamic value) {
  if (value == null) return null;
  final text = value.toString();
  return text.isEmpty ? null : text;
}
