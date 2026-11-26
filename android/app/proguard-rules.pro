# NativeEntry is referenced directly from the Android process.
-keep class dev.nodexlm.app.NativeEntry { *; }

-keep interface dev.nodexlm.app.NativeEntry$TokenSink { *; }
-keepclassmembers class * implements dev.nodexlm.app.NativeEntry$TokenSink { public void onToken(byte[]); }

-keep class dev.nodexlm.app.NativeRustRpc { *; }
-keep class dev.nodexlm.app.NativeRustDownload { *; }

# Optional LiteRT-LM JNI bridge; no effect when the SDK is absent.
-keep class com.google.ai.edge.litertlm.** { *; }
