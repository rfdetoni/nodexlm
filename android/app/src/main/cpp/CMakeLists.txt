cmake_minimum_required(VERSION 3.22.1)
project(nodexlm LANGUAGES C CXX)

# Explicitly pinned independent inference library. No code from PrivateLM.
include(FetchContent)
set(LLAMA_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(LLAMA_BUILD_SERVER OFF CACHE BOOL "" FORCE)
set(GGML_NATIVE OFF CACHE BOOL "" FORCE)
set(GGML_OPENMP OFF CACHE BOOL "" FORCE)
set(BUILD_SHARED_LIBS OFF CACHE BOOL "" FORCE)
FetchContent_Declare(llamacpp
    GIT_REPOSITORY https://github.com/ggml-org/llama.cpp.git
    GIT_TAG 18ab62a8ec5144ae5d2f65e78374ba69652d75dd
    GIT_SHALLOW FALSE
)
FetchContent_MakeAvailable(llamacpp)
add_library(nodexlm SHARED native.cpp)
target_link_libraries(nodexlm PRIVATE llama log android)
target_include_directories(nodexlm PRIVATE ${llamacpp_SOURCE_DIR}/include)
set_target_properties(nodexlm PROPERTIES CXX_STANDARD 17 CXX_STANDARD_REQUIRED YES)
