import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

/// Shared desktop palette so pages and platforms stay consistent.
abstract final class WxAgentColors {
  static const primary = Color(0xFF247568);
  static const secondary = Color(0xFF4D8179);
  static const sidebar = Color(0xFFF0F3F1);
  static const background = Color(0xFFF7F8FA);
  static const surface = Colors.white;
  static const border = Color(0xFFE4E9E7);
  static const textPrimary = Color(0xFF24332F);
  static const textSecondary = Color(0xFF667570);
  static const success = Color(0xFF287D56);
  static const warning = Color(0xFFA76920);
  static const danger = Color(0xFFBC4545);
}

ThemeData buildWxAgentTheme() {
  final fontFamily = defaultTargetPlatform == TargetPlatform.macOS
      ? 'PingFang SC'
      : 'Microsoft YaHei';
  final base = ThemeData(
    useMaterial3: true,
    colorScheme: ColorScheme.fromSeed(
      seedColor: WxAgentColors.primary,
      primary: WxAgentColors.primary,
      surface: WxAgentColors.surface,
      error: WxAgentColors.danger,
    ),
    fontFamily: fontFamily,
    fontFamilyFallback: const [
      'PingFang SC',
      'Microsoft YaHei',
      'Noto Sans SC',
      'Segoe UI',
      'Helvetica Neue',
      'Arial',
    ],
  );
  final rounded = RoundedRectangleBorder(
    borderRadius: BorderRadius.circular(12),
  );
  final inputBorder = OutlineInputBorder(
    borderRadius: BorderRadius.circular(12),
    borderSide: const BorderSide(color: WxAgentColors.border),
  );

  return base.copyWith(
    scaffoldBackgroundColor: WxAgentColors.background,
    dividerTheme: const DividerThemeData(
      color: WxAgentColors.border,
      thickness: 1,
      space: 24,
    ),
    appBarTheme: const AppBarTheme(
      backgroundColor: WxAgentColors.background,
      foregroundColor: WxAgentColors.textPrimary,
      elevation: 0,
      scrolledUnderElevation: 0,
    ),
    cardTheme: CardThemeData(
      color: WxAgentColors.surface,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(18),
        side: const BorderSide(color: WxAgentColors.border),
      ),
      margin: EdgeInsets.zero,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: WxAgentColors.surface,
      border: inputBorder,
      enabledBorder: inputBorder,
      focusedBorder: inputBorder.copyWith(
        borderSide: const BorderSide(color: WxAgentColors.primary, width: 1.5),
      ),
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 16),
      labelStyle: const TextStyle(color: WxAgentColors.textSecondary),
      hintStyle: const TextStyle(color: WxAgentColors.textSecondary),
    ),
    elevatedButtonTheme: ElevatedButtonThemeData(
      style: ElevatedButton.styleFrom(
        elevation: 0,
        backgroundColor: WxAgentColors.primary,
        foregroundColor: Colors.white,
        minimumSize: const Size(0, 46),
        shape: rounded,
        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
        textStyle: TextStyle(
          fontFamily: fontFamily,
          fontWeight: FontWeight.w600,
          fontSize: 14,
        ),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: WxAgentColors.textPrimary,
        minimumSize: const Size(0, 44),
        side: const BorderSide(color: WxAgentColors.border),
        shape: rounded,
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(shape: rounded),
    ),
    chipTheme: base.chipTheme.copyWith(
      side: const BorderSide(color: WxAgentColors.border),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
    ),
    tooltipTheme: const TooltipThemeData(
      waitDuration: Duration(milliseconds: 400),
    ),
    textTheme: base.textTheme
        .apply(
          bodyColor: WxAgentColors.textPrimary,
          displayColor: WxAgentColors.textPrimary,
        )
        .copyWith(
          headlineMedium: TextStyle(
            fontFamily: fontFamily,
            fontSize: 27,
            fontWeight: FontWeight.w700,
            height: 1.35,
            color: WxAgentColors.textPrimary,
          ),
          bodyMedium: TextStyle(
            fontFamily: fontFamily,
            fontSize: 14,
            height: 1.65,
            color: WxAgentColors.textPrimary,
          ),
        ),
  );
}
