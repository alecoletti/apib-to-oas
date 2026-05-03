cmake_minimum_required(VERSION 3.4 FATAL_ERROR)

project(dtl VERSION 1.0 LANGUAGES CXX)

add_library(dtl INTERFACE
    )

target_include_directories(dtl INTERFACE
    $<BUILD_INTERFACE:${dtl_BINARY_DIR}/dtl>
    $<BUILD_INTERFACE:${dtl_SOURCE_DIR}/dtl>
    $<INSTALL_INTERFACE:dtl>
    )

install(EXPORT dtl-targets
    FILE dtl-targets.cmake
    NAMESPACE dtl::
    DESTINATION lib/cmake/dtl
    )

install(TARGETS dtl EXPORT dtl-targets
    INCLUDES DESTINATION include
    )

include(CMakePackageConfigHelpers)
write_basic_package_version_file("dtl-config-version.cmake"
    VERSION ${dtl_VERSION}
    COMPATIBILITY SameMajorVersion
    )
install(
    FILES
        "dtl-config.cmake"
        "${CMAKE_CURRENT_BINARY_DIR}/dtl-config-version.cmake"
    DESTINATION
        lib/cmake/dtl
    )

add_library(dtl::dtl ALIAS dtl)
