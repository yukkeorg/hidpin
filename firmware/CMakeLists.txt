# hidpin firmware for RP2040 boards.
#   cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico
#   cmake -S firmware -B build/qtpy -G Ninja -DPICO_BOARD=adafruit_qtpy_rp2040
# Set PICO_SDK_PATH to use a local SDK; otherwise the pinned release is fetched from git.
cmake_minimum_required(VERSION 3.20)

if(NOT PICO_SDK_PATH AND NOT DEFINED ENV{PICO_SDK_PATH})
    set(PICO_SDK_FETCH_FROM_GIT ON)
    set(PICO_SDK_FETCH_FROM_GIT_TAG 2.3.1)
endif()

set(HIDPIN_BOARDS pico pico2 adafruit_qtpy_rp2040)
set(PICO_BOARD pico CACHE STRING "Target board: pico, pico2 or adafruit_qtpy_rp2040")
set_property(CACHE PICO_BOARD PROPERTY STRINGS ${HIDPIN_BOARDS})
if(NOT PICO_BOARD IN_LIST HIDPIN_BOARDS)
    message(FATAL_ERROR "Unsupported PICO_BOARD '${PICO_BOARD}'. Use pico, pico2 or adafruit_qtpy_rp2040.")
endif()

include(pico_sdk_import.cmake)

project(hidpin_firmware VERSION 0.1.0 LANGUAGES C CXX ASM)

option(HIDPIN_DEBUG "Add a USB CDC interface that carries debug logs" OFF)

# USB identifiers. 1209:6870 is allocated to hidpin by pid.codes (https://pid.codes/1209/6870/).
# A modified device that is redistributed needs ids of its own; 1209:0001 is the pid.codes test PID,
# for in-house testing only. An existing build directory keeps the PID it was configured with.
set(HIDPIN_USB_VID "0x1209" CACHE STRING "USB Vendor ID")
set(HIDPIN_USB_PID "0x6870" CACHE STRING "USB Product ID")
if(HIDPIN_USB_VID STREQUAL "0x1209" AND HIDPIN_USB_PID STREQUAL "0x0001")
    message(STATUS "hidpin: USB ${HIDPIN_USB_VID}:${HIDPIN_USB_PID} (pid.codes test PID; in-house testing only)")
else()
    message(STATUS "hidpin: USB ${HIDPIN_USB_VID}:${HIDPIN_USB_PID}")
endif()

# Status LED. Empty: the board's own LED (the on-board LED of the Pico and the Pico 2, the NeoPixel of
# the QT Py). A GPIO number moves it there; that GPIO is then left out of the available GPIOs.
set(HIDPIN_STATUS_LED_PIN "" CACHE STRING "GPIO of the status LED (empty: the board's own LED)")
set(HIDPIN_STATUS_LED_TYPE "gpio" CACHE STRING "LED on HIDPIN_STATUS_LED_PIN: gpio (lit when HIGH) or ws2812")
set_property(CACHE HIDPIN_STATUS_LED_TYPE PROPERTY STRINGS gpio ws2812)
if(HIDPIN_STATUS_LED_PIN STREQUAL "")
    message(STATUS "hidpin: status LED is the board's own")
else()
    if(NOT HIDPIN_STATUS_LED_PIN MATCHES "^[0-9]+$" OR HIDPIN_STATUS_LED_PIN GREATER 29)
        message(FATAL_ERROR "HIDPIN_STATUS_LED_PIN must be a GPIO number from 0 to 29, got '${HIDPIN_STATUS_LED_PIN}'")
    endif()
    if(NOT HIDPIN_STATUS_LED_TYPE MATCHES "^(gpio|ws2812)$")
        message(FATAL_ERROR "HIDPIN_STATUS_LED_TYPE must be gpio or ws2812, got '${HIDPIN_STATUS_LED_TYPE}'")
    endif()
    message(STATUS "hidpin: status LED on GPIO${HIDPIN_STATUS_LED_PIN} (${HIDPIN_STATUS_LED_TYPE})")
endif()
if(HIDPIN_STATUS_LED_PIN STREQUAL "")
    # Of the supported boards, only the QT Py has a WS2812 of its own.
    if(PICO_BOARD STREQUAL "adafruit_qtpy_rp2040")
        set(HIDPIN_NEEDS_WS2812 ON)
    endif()
elseif(HIDPIN_STATUS_LED_TYPE STREQUAL "ws2812")
    set(HIDPIN_NEEDS_WS2812 ON)
endif()

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
    HIDPIN_USB_VID=${HIDPIN_USB_VID}
    HIDPIN_USB_PID=${HIDPIN_USB_PID}
    HIDPIN_FW_MAJOR=${PROJECT_VERSION_MAJOR}
    HIDPIN_FW_MINOR=${PROJECT_VERSION_MINOR}
    HIDPIN_FW_PATCH=${PROJECT_VERSION_PATCH}
    HIDPIN_DEBUG=$<BOOL:${HIDPIN_DEBUG}>
)
if(NOT HIDPIN_STATUS_LED_PIN STREQUAL "")
    target_compile_definitions(hidpin PRIVATE
        HIDPIN_STATUS_LED_PIN=${HIDPIN_STATUS_LED_PIN}
        HIDPIN_STATUS_LED_WS2812=$<STREQUAL:${HIDPIN_STATUS_LED_TYPE},ws2812>
    )
endif()
target_link_libraries(hidpin PRIVATE
    hidpin_core
    hardware_pio
    pico_stdlib
    pico_unique_id
    tinyusb_device
)

if(HIDPIN_NEEDS_WS2812)
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
