// Board identity, available GPIOs, chip (PROTOCOL.md 5.1-5.4) and status LED. The board id, its SDK
// name and the available GPIOs come from CMakeLists.txt; the chip and the LED from the board header.
#ifndef HIDPIN_BOARD_H
#define HIDPIN_BOARD_H

#include "pico.h"

#include "hidpin/protocol.h"

#if !defined(HIDPIN_BOARD_ID) || !defined(HIDPIN_SDK_BOARD) || !defined(HIDPIN_AVAILABLE)
#error "HIDPIN_BOARD_ID, HIDPIN_SDK_BOARD and HIDPIN_AVAILABLE come from firmware/CMakeLists.txt"
#endif

// Protocol version 1 covers GPIO0-29, the GPIOs of the RP2040 and the RP2350A.
#if PICO_RP2350 && !PICO_RP2350A
#error "RP2350B boards (48 GPIOs) are not supported"
#endif

// The status LED: the one chosen with HIDPIN_STATUS_LED_PIN, otherwise the board's own, a plain LED
// or a WS2812 (NeoPixel) whose power may have to be switched on first. No LED when the board has none.
#if defined(HIDPIN_STATUS_LED_PIN)
#define HIDPIN_LED_PIN HIDPIN_STATUS_LED_PIN
#define HIDPIN_LED_IS_WS2812 HIDPIN_STATUS_LED_WS2812
#elif defined(PICO_DEFAULT_LED_PIN)
#define HIDPIN_LED_PIN PICO_DEFAULT_LED_PIN
#define HIDPIN_LED_IS_WS2812 0
#elif defined(PICO_DEFAULT_WS2812_PIN)
#define HIDPIN_LED_PIN PICO_DEFAULT_WS2812_PIN
#define HIDPIN_LED_IS_WS2812 1
#if defined(PICO_DEFAULT_WS2812_POWER_PIN)
#define HIDPIN_LED_POWER_PIN PICO_DEFAULT_WS2812_POWER_PIN
#endif
#endif

// The GPIOs the status LED uses cannot also be available GPIOs.
#if defined(HIDPIN_LED_PIN) && HIDPIN_LED_PIN < 30 && (HIDPIN_AVAILABLE & (1u << HIDPIN_LED_PIN))
#error "HIDPIN_GPIOS includes the GPIO of the board's status LED; leave it out or move the LED with HIDPIN_STATUS_LED_PIN"
#endif
#if defined(HIDPIN_LED_POWER_PIN) && HIDPIN_LED_POWER_PIN < 30 && (HIDPIN_AVAILABLE & (1u << HIDPIN_LED_POWER_PIN))
#error "HIDPIN_GPIOS includes the GPIO that powers the board's status LED; leave it out"
#endif

// Fills in the chip, its revision and its known problems.
static inline void board_identify_chip(hp_device_info_t *info)
{
#if PICO_RP2350
    info->chip = HP_CHIP_RP2350A;
    info->chip_revision = rp2350_chip_version();
    // Erratum E9: on A2 and earlier, internal pull-downs cannot hold an input low.
    if (info->chip_revision <= 2u) {
        info->quirks |= HP_QUIRK_PULL_DOWN_UNRELIABLE;
    }
#else
    info->chip = HP_CHIP_RP2040;
    info->chip_revision = rp2040_chip_version();
#endif
}

#endif
