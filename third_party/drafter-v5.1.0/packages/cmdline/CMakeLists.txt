cmake_minimum_required(VERSION 3.4 FATAL_ERROR)

project(cmdline VERSION 1.0 LANGUAGES CXX)

add_library(cmdline INTERFACE
    )

target_include_directories(cmdline INTERFACE
    $<BUILD_INTERFACE:${cmdline_BINARY_DIR}>
    $<BUILD_INTERFACE:${cmdline_SOURCE_DIR}>
    $<INSTALL_INTERFACE:>
    )

install(EXPORT cmdline-targets
    FILE cmdline-targets.cmake
    NAMESPACE cmdline::
    DESTINATION lib/cmake/cmdline
    )

install(TARGETS cmdline EXPORT cmdline-targets
    INCLUDES DESTINATION include
    )

include(CMakePackageConfigHelpers)
write_basic_package_version_file("cmdline-config-version.cmake"
    VERSION ${cmdline_VERSION}
    COMPATIBILITY SameMajorVersion
    )
install(
    FILES
        "cmdline-config.cmake"
        "${CMAKE_CURRENT_BINARY_DIR}/cmdline-config-version.cmake"
    DESTINATION
        lib/cmake/cmdline
    )

add_library(cmdline::cmdline ALIAS cmdline)
