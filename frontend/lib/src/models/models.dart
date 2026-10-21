class KeyInfo {
  KeyInfo({this.dbKey, this.timestamp});
  factory KeyInfo.fromJson(Map<String, dynamic> json) => KeyInfo(
    dbKey: json['db_key'] as String?,
    timestamp: json['timestamp'] as String?,
  );
  final String? dbKey;
  final String? timestamp;
}

class SessionMeta {
  SessionMeta({
    required this.sessionId,
    required this.displayName,
    this.messages = 0,
  });
  factory SessionMeta.fromJson(Map<String, dynamic> json) => SessionMeta(
    sessionId: json['session_id']?.toString() ?? '',
    displayName: json['display_name']?.toString() ?? '未命名会话',
    messages: (json['messages'] as num?)?.toInt() ?? 0,
  );
  final String sessionId;
  final String displayName;
  final int messages;
}

class ChatRecord {
  ChatRecord.fromJson(Map<String, dynamic> json)
    : sessionId = json['session_id']?.toString() ?? '',
      displayName = json['display_name']?.toString() ?? '',
      localId = json['local_id']?.toString() ?? '',
      formattedTime = json['formatted_time']?.toString() ?? '',
      type = json['type']?.toString() ?? '',
      content = json['content']?.toString() ?? '',
      sender = json['sender']?.toString() ?? '',
      senderName = json['sender_name']?.toString() ?? '',
      isSend = json['is_send'] == true || json['is_send'] == 1;
  final String sessionId;
  final String displayName;
  final String localId;
  final String formattedTime;
  final String type;
  final String content;
  final String sender;
  final String senderName;
  final bool isSend;
}

class MessagePage {
  MessagePage.fromJson(Map<String, dynamic> json)
    : messages = (json['messages'] as List? ?? [])
          .map(
            (value) =>
                ChatRecord.fromJson(Map<String, dynamic>.from(value as Map)),
          )
          .toList(),
      total = (json['total'] as num?)?.toInt() ?? 0,
      offset = (json['offset'] as num?)?.toInt() ?? 0;
  final List<ChatRecord> messages;
  final int total;
  final int offset;
}
