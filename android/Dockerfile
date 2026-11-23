# Local Android debug APK builder (Podman or Docker).
# Build image:
#   podman build -t nodexlm-android-builder -f android/Dockerfile android
# Compile debug APK:
#   podman run --rm --userns=keep-id -v "$PWD:/workspace" -w /workspace/android \
#     nodexlm-android-builder gradle --no-daemon :app:assembleDebug
FROM docker.io/library/ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive \
    JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64 \
    ANDROID_HOME=/opt/android-sdk \
    ANDROID_SDK_ROOT=/opt/android-sdk \
    NDK_VERSION=27.2.12479018 \
    ANDROID_BUILD_TOOLS=35.0.0 \
    GRADLE_VERSION=8.11.1

RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential \
      ca-certificates \
      curl \
      git \
      unzip \
      python3 \
      ninja-build \
      openjdk-17-jdk-headless \
      glslc \
      spirv-headers \
      libvulkan-dev \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 --branch v1.3.296 https://github.com/KhronosGroup/Vulkan-Headers.git /tmp/Vulkan-Headers \
    && cp -r /tmp/Vulkan-Headers/include/vulkan /tmp/Vulkan-Headers/include/vk_video /usr/include/ \
    && rm -rf /tmp/Vulkan-Headers

RUN curl -fsSL "https://services.gradle.org/distributions/gradle-${GRADLE_VERSION}-bin.zip" -o /tmp/gradle.zip \
    && mkdir -p /opt/gradle \
    && unzip -q /tmp/gradle.zip -d /opt/gradle \
    && ln -s "/opt/gradle/gradle-${GRADLE_VERSION}/bin/gradle" /usr/local/bin/gradle \
    && rm -f /tmp/gradle.zip

RUN curl -fsSL "https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip" -o /tmp/cmdline-tools.zip \
    && mkdir -p "${ANDROID_HOME}/cmdline-tools" \
    && unzip -q /tmp/cmdline-tools.zip -d "${ANDROID_HOME}/cmdline-tools" \
    && mv "${ANDROID_HOME}/cmdline-tools/cmdline-tools" "${ANDROID_HOME}/cmdline-tools/latest" \
    && rm -f /tmp/cmdline-tools.zip

ENV PATH="${ANDROID_HOME}/platform-tools:${ANDROID_HOME}/cmdline-tools/latest/bin:/opt/gradle/gradle-${GRADLE_VERSION}/bin:${PATH}"

RUN (yes || true) | sdkmanager --licenses >/dev/null \
    && sdkmanager --install \
      "platform-tools" \
      "platforms;android-35" \
      "build-tools;35.0.0" \
      "ndk;27.2.12479018" \
      "cmake;3.22.1" \
    && chmod -R a+rX "${ANDROID_HOME}" /opt/gradle
