cmake_minimum_required(VERSION 3.22.1)
project(nodexlm LANGUAGES C CXX)

# Explicitly pinned independent inference library. No code from PrivateLM.
include(FetchContent)
set(LLAMA_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_SERVER OFF CACHE BOOL "" FORCE)
set(GGML_NATIVE OFF CACHE BOOL "" FORCE)
set(GGML_OPENMP OFF CACHE BOOL "" FORCE)
# This is the same verified llama.cpp revision used by the Host RPC runtime.
# The GGML RPC service runs in its own Android process and listens on loopback.
set(GGML_RPC ON CACHE BOOL "" FORCE)
set(GGML_RPC_RDMA OFF CACHE BOOL "" FORCE)
set(GGML_BACKEND_DL OFF CACHE BOOL "" FORCE)
# ARM64 can expose CPU and Vulkan (integrated) GPU to the same RPC endpoint.
# ARM32 remains CPU-only for compatibility with older Android 10 phones.
if(ANDROID_ABI STREQUAL "arm64-v8a")
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
    set(NODEXLM_SPIRV_STAGING "${CMAKE_BINARY_DIR}/nodexlm-spirv")
    file(MAKE_DIRECTORY "${NODEXLM_SPIRV_STAGING}")
    file(COPY "${NODEXLM_HOST_SPIRV_INCLUDE}/spirv"
        DESTINATION "${NODEXLM_SPIRV_STAGING}")
    set(NODEXLM_SPIRV_PACKAGE "${CMAKE_BINARY_DIR}/nodexlm-spirv-package")
    file(MAKE_DIRECTORY "${NODEXLM_SPIRV_PACKAGE}")
    file(WRITE "${NODEXLM_SPIRV_PACKAGE}/SPIRV-HeadersConfig.cmake"
        "# Validated SPIR-V header-only package staged for Android NDK cross compile\\n")
    set("SPIRV-Headers_DIR" "${NODEXLM_SPIRV_PACKAGE}" CACHE PATH
        "Android-compatible host SPIR-V headers package" FORCE)
else()
    set(GGML_VULKAN OFF CACHE BOOL "" FORCE)
endif()
# ARMv7 cannot execute AArch64-only optimized kernels.
if(ANDROID_ABI STREQUAL "armeabi-v7a")
    set(GGML_CPU_KLEIDIAI OFF CACHE BOOL "" FORCE)
    # Baseline 32-bit ARM lacks the llamafile FP16 Neon intrinsics.
    set(GGML_LLAMAFILE OFF CACHE BOOL "" FORCE)
endif()
set(BUILD_SHARED_LIBS OFF CACHE BOOL "" FORCE)
file(READ "${CMAKE_CURRENT_SOURCE_DIR}/../../../../../internal/llamaruntime/revision.json" NODEXLM_LLAMA_REVISION)
string(JSON NODEXLM_LLAMA_COMMIT GET "${NODEXLM_LLAMA_REVISION}" commit)
FetchContent_Declare(llamacpp
    GIT_REPOSITORY https://github.com/ggml-org/llama.cpp.git
    GIT_TAG ${NODEXLM_LLAMA_COMMIT}
    GIT_SHALLOW FALSE
)
FetchContent_MakeAvailable(llamacpp)
if(ANDROID_ABI STREQUAL "arm64-v8a" AND TARGET ggml-vulkan)
    target_include_directories(ggml-vulkan BEFORE PRIVATE
        "${NODEXLM_VULKAN_STAGING}" "${NODEXLM_SPIRV_STAGING}")
endif()
add_library(nodexlm SHARED native.cpp)
target_link_libraries(nodexlm PRIVATE llama ggml ggml-rpc log android)
target_include_directories(nodexlm PRIVATE ${llamacpp_SOURCE_DIR}/ggml/include)
target_include_directories(nodexlm PRIVATE ${llamacpp_SOURCE_DIR}/include)
set_target_properties(nodexlm PROPERTIES CXX_STANDARD 17 CXX_STANDARD_REQUIRED YES)
