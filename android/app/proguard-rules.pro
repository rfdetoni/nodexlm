# NativeEntry is referenced directly from the Android process.
-keep class dev.nodexlm.app.NativeEntry { *; }

-keep interface dev.nodexlm.app.NativeEntry$TokenSink { *; }
-keepclassmembers class * implements dev.nodexlm.app.NativeEntry$TokenSink { public void onToken(byte[]); }
