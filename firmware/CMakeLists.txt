# hidpin firmware for RP2040 and RP2350A boards.
#   cmake -S firmware -B build/pico -G Ninja -DPICO_BOARD=pico
#   cmake -S firmware -B build/qtpy -G Ninja -DPICO_BOARD=adafruit_qtpy_rp2040
#   cmake -S firmware -B build/pico_w -G Ninja -DPICO_BOARD=pico_w -DHIDPIN_GPIOS=0-22,26-28
# Set PICO_SDK_PATH to use a local SDK; otherwise the pinned release is fetched from git.
cmake_minimum_required(VERSION 3.20)

if(NOT PICO_SDK_PATH AND NOT DEFINED ENV{PICO_SDK_PATH})
    set(PICO_SDK_FETCH_FROM_GIT ON)
    set(PICO_SDK_FETCH_FROM_GIT_TAG 2.3.1)
endif()

# Any board of the Pico SDK can be used; the chip (RP2040 or RP2350A) comes from it.
set(PICO_BOARD pico CACHE STRING "Pico SDK board, for example pico, pico2, pico_w or adafruit_qtpy_rp2040")
if(NOT PICO_BOARD MATCHES "^[A-Za-z0-9_.-]+$")
    message(FATAL_ERROR "PICO_BOARD '${PICO_BOARD}' is not a board name")
endif()
string(LENGTH "${PICO_BOARD}" HIDPIN_BOARD_NAME_LENGTH)
if(HIDPIN_BOARD_NAME_LENGTH GREATER 40)
    message(FATAL_ERROR "PICO_BOARD '${PICO_BOARD}' is longer than the 40 characters of the device information")
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

# Status LED. Empty: the board's own LED (PICO_DEFAULT_LED_PIN or PICO_DEFAULT_WS2812_PIN of the board
# header), if it has one. A GPIO number moves it there.
set(HIDPIN_STATUS_LED_PIN "" CACHE STRING "GPIO of the status LED (empty: the board's own LED)")
set(HIDPIN_STATUS_LED_TYPE "gpio" CACHE STRING "LED on HIDPIN_STATUS_LED_PIN: gpio (lit when HIGH) or ws2812")
set_property(CACHE HIDPIN_STATUS_LED_TYPE PROPERTY STRINGS gpio ws2812)
if(HIDPIN_STATUS_LED_PIN STREQUAL "")
    message(STATUS "hidpin: status LED is the board's own, if it has one")
else()
    if(NOT HIDPIN_STATUS_LED_PIN MATCHES "^[0-9]+$" OR HIDPIN_STATUS_LED_PIN GREATER 29)
        message(FATAL_ERROR "HIDPIN_STATUS_LED_PIN must be a GPIO number from 0 to 29, got '${HIDPIN_STATUS_LED_PIN}'")
    endif()
    if(NOT HIDPIN_STATUS_LED_TYPE MATCHES "^(gpio|ws2812)$")
        message(FATAL_ERROR "HIDPIN_STATUS_LED_TYPE must be gpio or ws2812, got '${HIDPIN_STATUS_LED_TYPE}'")
    endif()
    message(STATUS "hidpin: status LED on GPIO${HIDPIN_STATUS_LED_PIN} (${HIDPIN_STATUS_LED_TYPE})")
endif()

# The chip comes from the board. RP2350B boards (48 GPIOs) are refused: protocol version 1 covers GPIO0-29.
# The SDK takes an RP2350 for a B package unless the board header sets PICO_RP2350A to 1. A board
# configured through a CMake file of its own has no header here; board.h then stops the build.
if(PICO_PLATFORM MATCHES "^rp2350")
    if(EXISTS "${PICO_BOARD_HEADER_FILE}")
        file(STRINGS "${PICO_BOARD_HEADER_FILE}" HIDPIN_RP2350A_LINES REGEX "^#define[ \t]+PICO_RP2350A[ \t]+1")
        if(NOT HIDPIN_RP2350A_LINES)
            message(FATAL_ERROR "PICO_BOARD '${PICO_BOARD}' has an RP2350B (48 GPIOs), which hidpin does not support")
        endif()
    endif()
    set(HIDPIN_CHIP RP2350A)
elseif(PICO_PLATFORM STREQUAL "rp2040")
    set(HIDPIN_CHIP RP2040)
else()
    message(FATAL_ERROR "PICO_PLATFORM '${PICO_PLATFORM}' is not supported; use an RP2040 or RP2350A board")
endif()

# Available GPIOs (PROTOCOL.md 5.1), as a list of GPIO numbers and ranges such as "0-22,26-28".
# The boards below have a preset and a board id of their own; any other board gets
# board id 0 and must be given HIDPIN_GPIOS, since some GPIOs may be wired to the board's own parts
# (power supply, wireless chip). The GPIO of the status LED must not be among them.
set(HIDPIN_GPIOS "" CACHE STRING "Available GPIOs, for example 0-22,26-28 (empty: the preset of the board)")
if(PICO_BOARD STREQUAL "pico")
    set(HIDPIN_BOARD_ID 1)
    set(HIDPIN_PRESET_GPIOS "0-22,26-28")
elseif(PICO_BOARD STREQUAL "adafruit_qtpy_rp2040")
    set(HIDPIN_BOARD_ID 2)
    set(HIDPIN_PRESET_GPIOS "3-6,20,22-29")
elseif(PICO_BOARD STREQUAL "pico2")
    set(HIDPIN_BOARD_ID 3)
    set(HIDPIN_PRESET_GPIOS "0-22,26-28")
else()
    set(HIDPIN_BOARD_ID 0)
    set(HIDPIN_PRESET_GPIOS "")
endif()

# Turns a list such as "0-22,26-28" into a bitmask in out_var.
function(hidpin_gpio_mask text out_var)
    set(mask 0)
    string(REPLACE "," ";" items "${text}")
    foreach(item IN LISTS items)
        string(STRIP "${item}" item)
        if(item MATCHES "^([0-9]+)-([0-9]+)$")
            set(first ${CMAKE_MATCH_1})
            set(last ${CMAKE_MATCH_2})
        elseif(item MATCHES "^[0-9]+$")
            set(first ${item})
            set(last ${item})
        else()
            message(FATAL_ERROR "HIDPIN_GPIOS: '${item}' is neither a GPIO number nor a range such as 26-28")
        endif()
        if(last GREATER 29 OR first GREATER last)
            message(FATAL_ERROR "HIDPIN_GPIOS: '${item}' is not within GPIO0-29 in ascending order")
        endif()
        foreach(gpio RANGE ${first} ${last})
            math(EXPR mask "${mask} | (1 << ${gpio})")
        endforeach()
    endforeach()
    math(EXPR mask "${mask}" OUTPUT_FORMAT HEXADECIMAL)
    set(${out_var} ${mask} PARENT_SCOPE)
endfunction()

if(NOT HIDPIN_GPIOS STREQUAL "")
    hidpin_gpio_mask("${HIDPIN_GPIOS}" HIDPIN_AVAILABLE)
    if(NOT HIDPIN_STATUS_LED_PIN STREQUAL "")
        math(EXPR HIDPIN_LED_BIT "${HIDPIN_AVAILABLE} & (1 << ${HIDPIN_STATUS_LED_PIN})")
        if(NOT HIDPIN_LED_BIT EQUAL 0)
            message(FATAL_ERROR "HIDPIN_GPIOS includes GPIO${HIDPIN_STATUS_LED_PIN}, the status LED")
        endif()
    endif()
elseif(NOT HIDPIN_PRESET_GPIOS STREQUAL "")
    hidpin_gpio_mask("${HIDPIN_PRESET_GPIOS}" HIDPIN_AVAILABLE)
    # A status LED moved with HIDPIN_STATUS_LED_PIN takes its GPIO away from the preset.
    if(NOT HIDPIN_STATUS_LED_PIN STREQUAL "")
        math(EXPR HIDPIN_AVAILABLE "${HIDPIN_AVAILABLE} & ~(1 << ${HIDPIN_STATUS_LED_PIN})" OUTPUT_FORMAT HEXADECIMAL)
    endif()
else()
    message(FATAL_ERROR "PICO_BOARD '${PICO_BOARD}' has no preset of available GPIOs. Give them with "
        "-DHIDPIN_GPIOS=..., for example -DHIDPIN_GPIOS=0-22,26-28, leaving out the GPIOs wired to the board's "
        "own parts.")
endif()
message(STATUS "hidpin: board ${PICO_BOARD} (id ${HIDPIN_BOARD_ID}, ${HIDPIN_CHIP}), available GPIOs ${HIDPIN_AVAILABLE}")

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
    HIDPIN_BOARD_ID=${HIDPIN_BOARD_ID}
    HIDPIN_SDK_BOARD="${PICO_BOARD}"
    HIDPIN_AVAILABLE=${HIDPIN_AVAILABLE}u
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

# Generated for every board: whether the status LED is a WS2812 is known only from the board header.
pico_generate_pio_header(hidpin ${CMAKE_CURRENT_LIST_DIR}/app/ws2812.pio)

# UART stdio would claim GPIO0/1, which are available GPIOs on the Pico.
pico_enable_stdio_uart(hidpin 0)
if(HIDPIN_DEBUG)
    pico_enable_stdio_usb(hidpin 1)
else()
    pico_enable_stdio_usb(hidpin 0)
endif()

pico_add_extra_outputs(hidpin)
