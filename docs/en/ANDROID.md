# Android 10+ Guest

ARMv7 supports the CPU backend; ARM64 can use CPU and compatible Vulkan drivers. RAM and integrated GPU memory can overlap and must not be added as independent capacity. Native driver/device compatibility requires a physical-device test.

In Cluster, enter the Host's temporary pairing code and optionally its HTTPS URL. Start RPC processing after choosing Automatic, CPU, Vulkan or Hybrid. The Host retains the GGUF and controls layer placement. Keep both versions on the same native revision.

The RPC process uses a private filesystem Unix socket in app storage and a pinned TLS tunnel to the Host. Its separate process is stopped to release native allocations. Energy settings support charging-only operation, a battery threshold and a time window, including overnight windows. Restart the Guest after changing the profile. Severe thermal status always stops it; the notification explains a safety stop.

The model library supports public Hugging Face GGUF sets. Select the original first shard; all parts are downloaded sequentially and staged before publishing the complete model. Expected total size and available space are checked. A provided Hugging Face LFS SHA-256 is verified off the UI thread. Android DownloadManager handles network pauses. To import, select every shard of one model in the file picker. Incomplete sets cannot become selectable.

Standalone mode loads a complete local GGUF and leases jobs. It streams text chunks through the coordinator, uses standard UTF-8, honors supported sampling/stop controls and checks cancellation. This mode has its own smaller native context/output limits and does not add its whole-model memory to the distributed model.

User-facing resources include English and Brazilian Portuguese. Technical backend errors can retain their original diagnostic text. Release builds require a persistent signing keystore; never commit it. A new signing identity may require reinstalling an existing APK. Validate APK signatures, 16 KiB native alignment, background restrictions and energy behavior on target devices before stable distribution.
