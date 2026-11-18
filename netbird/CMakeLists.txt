cmake_minimum_required(VERSION 3.18)
project(bofbird)

if(CMAKE_INSTALL_PREFIX_INITIALIZED_TO_DEFAULT)
  set_property(CACHE CMAKE_INSTALL_PREFIX PROPERTY VALUE ${CMAKE_SOURCE_DIR}/dist)
endif()

option(BUILD_GO_TARGETS "Build Go/CGo daemon targets (requires Go toolchain)" ON)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
# LTO disabled — GCC 15 mingw ICEs on heavily-templated asio/jsoncons during LTO
# set(CMAKE_INTERPROCEDURAL_OPTIMIZATION ON)

message("-- Building for ${CMAKE_CXX_COMPILER_ID} compiler")

if(CMAKE_SIZEOF_VOID_P EQUAL 8)
    set(LINK_SCRIPT ${CMAKE_SOURCE_DIR}/link.ld)
elseif(CMAKE_SIZEOF_VOID_P EQUAL 4)
    set(LINK_SCRIPT ${CMAKE_SOURCE_DIR}/link32.ld)
endif()

# Derive ARCH from pointer size (works reliably for cross-compilation
# where CMAKE_SYSTEM_PROCESSOR may be empty)
if(CMAKE_SIZEOF_VOID_P EQUAL 8)
    set(ARCH "x64")
else()
    set(ARCH "x86")
endif()

include_directories(${CMAKE_SOURCE_DIR}/beacon/)

# Beacon compat layer and loader for standalone execution
add_subdirectory(beacon)
add_subdirectory(loader)

# C++ BOF-PE modules
add_subdirectory(netbird)
add_subdirectory(portfwd)

# Go daemon BOF-PE (requires Go + mingw-w64 CGo toolchain)
if(BUILD_GO_TARGETS)
    add_subdirectory(netbirdd)
else()
    message("-- Skipping Go daemon targets (BUILD_GO_TARGETS=OFF)")
endif()
