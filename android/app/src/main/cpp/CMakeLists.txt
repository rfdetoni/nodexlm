cmake_minimum_required(VERSION 3.22.1)
project(nodexlm LANGUAGES C CXX)

# Explicitly pinned independent inference library. No code from PrivateLM.
include(FetchContent)
set(NODEXLM_JNI_OUTPUT_DIR "${CMAKE_LIBRARY_OUTPUT_DIRECTORY}")
set(LLAMA_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_SERVER OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_TOOLS OFF CACHE BOOL "" FORCE)
set(GGML_NATIVE OFF CACHE BOOL "" FORCE)
set(GGML_OPENMP OFF CACHE BOOL "" FORCE)
set(GGML_RPC_RDMA OFF CACHE BOOL "" FORCE)
# Retain the ARMv8 baseline; on ARM64, GGML_CPU_ALL_VARIANTS + KleidiAI compile
# runtime-dispatched dotprod/fp16/i8mm/sve2 kernels without raising the APK baseline.
option(NODEXLM_KLEIDIAI "Build ARM64 KleidiAI kernels" ON)
if(ANDROID_ABI STREQUAL "arm64-v8a")
    set(BUILD_SHARED_LIBS ON CACHE BOOL "" FORCE)
    set(GGML_BACKEND_DL ON CACHE BOOL "" FORCE)
    set(GGML_CPU_ALL_VARIANTS ON CACHE BOOL "" FORCE)
    # Compile ggml-rpc directly into libnodexlm.so so GGML_BACKEND_DL does not
    # emit ggml-rpc as an un-linkable MODULE library on ARM64.
    set(GGML_RPC OFF CACHE BOOL "" FORCE)
    if(NODEXLM_KLEIDIAI)
        set(GGML_CPU_KLEIDIAI ON CACHE BOOL "" FORCE)
    else()
        set(GGML_CPU_KLEIDIAI OFF CACHE BOOL "" FORCE)
    endif()
    set(GGML_VULKAN ON CACHE BOOL "" FORCE)
    # The Vulkan shaders use SPIR-V headers from the BUILD host. Android NDK
    # CMake cross-compiling hides host CMake package configs under its sysroot.
    # Stage actual host headers and a minimal config within the build tree;
    # do not link host Vulkan binaries into the Android APK.
    find_path(NODEXLM_HOST_SPIRV_INCLUDE
        NAMES spirv/unified1/spirv.hpp
        NO_CMAKE_FIND_ROOT_PATH)
    if(NOT NODEXLM_HOST_SPIRV_INCLUDE)
        message(FATAL_ERROR "SPIR-V headers required for Android Vulkan; install spirv-headers")
    endif()
    # Vulkan-Hpp is C++ header-only and is absent from the Android NDK.
    # Copy headers from the build host, but link against Android's Vulkan
    # loader from the NDK sysroot, never the host x86_64 loader.
    find_path(NODEXLM_HOST_VULKAN_INCLUDE
        NAMES vulkan/vulkan.hpp
        NO_CMAKE_FIND_ROOT_PATH)
    if(NOT NODEXLM_HOST_VULKAN_INCLUDE)
        message(FATAL_ERROR "Vulkan-Hpp headers missing; install libvulkan-dev")
    endif()
    set(NODEXLM_VULKAN_STAGING "${CMAKE_BINARY_DIR}/nodexlm-vulkan-headers")
    file(MAKE_DIRECTORY "${NODEXLM_VULKAN_STAGING}")
    file(COPY "${NODEXLM_HOST_VULKAN_INCLUDE}/vulkan"
        DESTINATION "${NODEXLM_VULKAN_STAGING}")
    # vulkan_core.h includes vk_video/* as a sibling of vulkan/*.
    # Staging only vulkan/ previously broke the ARM64 NDK build.
    find_path(NODEXLM_HOST_VK_VIDEO_INCLUDE
        NAMES vk_video/vulkan_video_codec_av1std.h
        NO_CMAKE_FIND_ROOT_PATH)
    if(NOT NODEXLM_HOST_VK_VIDEO_INCLUDE)
        message(FATAL_ERROR "Vulkan video codec headers missing; install Vulkan-Headers")
    endif()
    file(COPY "${NODEXLM_HOST_VK_VIDEO_INCLUDE}/vk_video"
        DESTINATION "${NODEXLM_VULKAN_STAGING}")
    set(NODEXLM_SPIRV_STAGING "${CMAKE_BINARY_DIR}/nodexlm-spirv")
    file(MAKE_DIRECTORY "${NODEXLM_SPIRV_STAGING}")
    file(COPY "${NODEXLM_HOST_SPIRV_INCLUDE}/spirv"
        DESTINATION "${NODEXLM_SPIRV_STAGING}")
    set(NODEXLM_SPIRV_PACKAGE "${CMAKE_BINARY_DIR}/nodexlm-spirv-package")
    file(MAKE_DIRECTORY "${NODEXLM_SPIRV_PACKAGE}")
    # llama.cpp v1.1.0 links the imported header-only target explicitly.
    # Point it at staged headers, never a host binary or sysroot library.
    file(WRITE "${NODEXLM_SPIRV_PACKAGE}/SPIRV-HeadersConfig.cmake"
        "if(NOT TARGET SPIRV-Headers::SPIRV-Headers)\n"
        "    add_library(SPIRV-Headers::SPIRV-Headers INTERFACE IMPORTED)\n"
        "    set_target_properties(SPIRV-Headers::SPIRV-Headers PROPERTIES\n"
        "        INTERFACE_INCLUDE_DIRECTORIES \"${NODEXLM_SPIRV_STAGING}\")\n"
        "endif()\n")
    set("SPIRV-Headers_DIR" "${NODEXLM_SPIRV_PACKAGE}" CACHE PATH
        "Android-compatible host SPIR-V headers package" FORCE)
else()
    # ARMv7 remains statically linked CPU-only for older Android 10 devices.
    set(BUILD_SHARED_LIBS OFF CACHE BOOL "" FORCE)
    set(GGML_BACKEND_DL OFF CACHE BOOL "" FORCE)
    set(GGML_CPU_ALL_VARIANTS OFF CACHE BOOL "" FORCE)
    set(GGML_RPC ON CACHE BOOL "" FORCE)
    set(GGML_VULKAN OFF CACHE BOOL "" FORCE)
    set(GGML_CPU_KLEIDIAI OFF CACHE BOOL "" FORCE)
    # Baseline 32-bit ARM lacks the llamafile FP16 Neon intrinsics.
    set(GGML_LLAMAFILE OFF CACHE BOOL "" FORCE)
endif()

file(READ "${CMAKE_CURRENT_SOURCE_DIR}/../../../../../internal/llamaruntime/revision.json" NODEXLM_LLAMA_REVISION)
string(JSON NODEXLM_LLAMA_COMMIT GET "${NODEXLM_LLAMA_REVISION}" commit)
FetchContent_Declare(llamacpp
    GIT_REPOSITORY https://github.com/ggml-org/llama.cpp.git
    GIT_TAG ${NODEXLM_LLAMA_COMMIT}
    GIT_SHALLOW FALSE
    PATCH_COMMAND ${CMAKE_COMMAND} -DSOURCE=<SOURCE_DIR> -DPATCH=${CMAKE_CURRENT_SOURCE_DIR}/../../../../../internal/llamaruntime/patches/unix-rpc.patch -DEXTRA_PATCH=${CMAKE_CURRENT_SOURCE_DIR}/../../../../../internal/llamaruntime/patches/arm32-rpc-wire.patch -DANDROID_SHUTDOWN_PATCH=${CMAKE_CURRENT_SOURCE_DIR}/android-rpc-shutdown.patch -DANDROID_CACHE_PATCH=${CMAKE_CURRENT_SOURCE_DIR}/android-rpc-cache.patch -DKLEIDIAI_SHA_PATCH=${CMAKE_CURRENT_SOURCE_DIR}/kleidiai-sha256.patch -P ${CMAKE_CURRENT_SOURCE_DIR}/../../../../../scripts/apply-rpc-patch.cmake
)
FetchContent_MakeAvailable(llamacpp)

if(NODEXLM_JNI_OUTPUT_DIR)
    set(CMAKE_LIBRARY_OUTPUT_DIRECTORY "${NODEXLM_JNI_OUTPUT_DIR}")
    set(CMAKE_RUNTIME_OUTPUT_DIRECTORY "${NODEXLM_JNI_OUTPUT_DIR}")
endif()

if(ANDROID_ABI STREQUAL "arm64-v8a" AND TARGET ggml-vulkan)
    target_include_directories(ggml-vulkan BEFORE PRIVATE
        "${NODEXLM_VULKAN_STAGING}" "${NODEXLM_SPIRV_STAGING}")
endif()

if(ANDROID_ABI STREQUAL "arm64-v8a")
    if(NODEXLM_JNI_OUTPUT_DIR)
        foreach(_nodex_target IN ITEMS
            ggml-base
            ggml
            llama
            ggml-vulkan
            ggml-cpu-android_armv8.0_1
            ggml-cpu-android_armv8.2_1
            ggml-cpu-android_armv8.2_2
            ggml-cpu-android_armv8.6_1
            ggml-cpu-android_armv9.0_1
            ggml-cpu-android_armv9.2_1
            ggml-cpu-android_armv9.2_2
        )
            if(TARGET ${_nodex_target})
                set_target_properties(${_nodex_target} PROPERTIES
                    LIBRARY_OUTPUT_DIRECTORY "${NODEXLM_JNI_OUTPUT_DIR}"
                    RUNTIME_OUTPUT_DIRECTORY "${NODEXLM_JNI_OUTPUT_DIR}")
            endif()
        endforeach()
    endif()
    add_library(nodexlm SHARED
        native.cpp
        ${llamacpp_SOURCE_DIR}/ggml/src/ggml-rpc/ggml-rpc.cpp
        ${llamacpp_SOURCE_DIR}/ggml/src/ggml-rpc/log.cpp
        ${llamacpp_SOURCE_DIR}/ggml/src/ggml-rpc/transport.cpp
    )
    target_link_libraries(nodexlm PRIVATE llama ggml ggml-base log android dl)
    target_include_directories(nodexlm PRIVATE
        ${llamacpp_SOURCE_DIR}/ggml/include
        ${llamacpp_SOURCE_DIR}/ggml/src
        ${llamacpp_SOURCE_DIR}/include
    )
else()
    add_library(nodexlm SHARED native.cpp)
    target_link_libraries(nodexlm PRIVATE llama ggml ggml-rpc log android)
    target_include_directories(nodexlm PRIVATE
        ${llamacpp_SOURCE_DIR}/ggml/include
        ${llamacpp_SOURCE_DIR}/include
    )
endif()
set_target_properties(nodexlm PROPERTIES CXX_STANDARD 17 CXX_STANDARD_REQUIRED YES)
