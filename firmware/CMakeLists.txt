# hidpin firmware for RP2040 boards.
#   cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico
#   cmake -S firmware -B build/qtpy -G Ninja -DPICO_BOARD=adafruit_qtpy_rp2040
# Set PICO_SDK_PATH to use a local SDK; otherwise the pinned release is fetched from git.
cmake_minimum_required(VERSION 3.20)

if(NOT PICO_SDK_PATH AND NOT DEFINED ENV{PICO_SDK_PATH})
    set(PICO_SDK_FETCH_FROM_GIT ON)
    set(PICO_SDK_FETCH_FROM_GIT_TAG 2.3.1)
endif()

set(PICO_BOARD pico CACHE STRING "Target board: pico or adafruit_qtpy_rp2040")
set_property(CACHE PICO_BOARD PROPERTY STRINGS pico adafruit_qtpy_rp2040)
if(NOT PICO_BOARD STREQUAL "pico" AND NOT PICO_BOARD STREQUAL "adafruit_qtpy_rp2040")
    message(FATAL_ERROR "Unsupported PICO_BOARD '${PICO_BOARD}'. Use pico or adafruit_qtpy_rp2040.")
endif()

include(pico_sdk_import.cmake)

project(hidpin_firmware VERSION 0.1.0 LANGUAGES C CXX ASM)

option(HIDPIN_DEBUG "Add a USB CDC interface that carries debug logs" OFF)

pico_sdk_init()

add_subdirectory(core)
target_compile_options(hidpin_core PRIVATE -Wall -Wextra -Werror)

set(HIDPIN_APP_SOURCES
    app/gpio_hw.c
    app/main.c
    app/status_led.c
    app/usb_descriptors.c
)
# Warnings apply to our sources only; SDK sources compiled into the same target are left alone.
set_source_files_properties(${HIDPIN_APP_SOURCES} PROPERTIES COMPILE_OPTIONS "-Wall;-Wextra;-Werror")

add_executable(hidpin ${HIDPIN_APP_SOURCES})
target_include_directories(hidpin PRIVATE app)
target_compile_definitions(hidpin PRIVATE
    HIDPIN_FW_MAJOR=${PROJECT_VERSION_MAJOR}
    HIDPIN_FW_MINOR=${PROJECT_VERSION_MINOR}
    HIDPIN_FW_PATCH=${PROJECT_VERSION_PATCH}
    HIDPIN_DEBUG=$<BOOL:${HIDPIN_DEBUG}>
)
target_link_libraries(hidpin PRIVATE
    hidpin_core
    hardware_pio
    pico_stdlib
    pico_unique_id
    tinyusb_device
)

if(PICO_BOARD STREQUAL "adafruit_qtpy_rp2040")
    pico_generate_pio_header(hidpin ${CMAKE_CURRENT_LIST_DIR}/app/ws2812.pio)
endif()

# UART stdio would claim GPIO0/1, which are available GPIOs on the Pico.
pico_enable_stdio_uart(hidpin 0)
if(HIDPIN_DEBUG)
    pico_enable_stdio_usb(hidpin 1)
else()
    pico_enable_stdio_usb(hidpin 0)
endif()

pico_add_extra_outputs(hidpin)
