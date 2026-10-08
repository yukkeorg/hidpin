// Board identity, available GPIOs and chip (PROTOCOL.md 5.1-5.3), selected by PICO_BOARD.
#ifndef HIDPIN_BOARD_H
#define HIDPIN_BOARD_H

#include "pico.h"

#include "hidpin/protocol.h"

#if defined(RASPBERRYPI_PICO)
#define HIDPIN_BOARD_ID HP_BOARD_PICO
#define HIDPIN_BOARD_AVAILABLE 0x1C7FFFFFu  // GPIO0-22, 26-28
#elif defined(RASPBERRYPI_PICO2)
#define HIDPIN_BOARD_ID HP_BOARD_PICO2
#define HIDPIN_BOARD_AVAILABLE 0x1C7FFFFFu  // GPIO0-22, 26-28, as on the Pico
#elif defined(ADAFRUIT_QTPY_RP2040)
#define HIDPIN_BOARD_ID HP_BOARD_QTPY_RP2040
#define HIDPIN_BOARD_AVAILABLE 0x3FD00078u  // GPIO3-6, 20, 22-29
#else
#error "Unsupported board: build with PICO_BOARD=pico, pico2 or adafruit_qtpy_rp2040"
#endif

// A status LED moved with HIDPIN_STATUS_LED_PIN takes its GPIO away from monitoring and output.
#if defined(HIDPIN_STATUS_LED_PIN)
#define HIDPIN_AVAILABLE (HIDPIN_BOARD_AVAILABLE & ~(1u << HIDPIN_STATUS_LED_PIN))
#else
#define HIDPIN_AVAILABLE HIDPIN_BOARD_AVAILABLE
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
