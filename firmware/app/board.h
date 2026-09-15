// Board identity and available GPIOs (PROTOCOL.md 5.1), selected by PICO_BOARD.
#ifndef HIDPIN_BOARD_H
#define HIDPIN_BOARD_H

#include "pico.h"

#include "hidpin/protocol.h"

#if defined(RASPBERRYPI_PICO)
#define HIDPIN_BOARD_ID HP_BOARD_PICO
#define HIDPIN_BOARD_AVAILABLE 0x1C7FFFFFu  // GPIO0-22, 26-28
#elif defined(ADAFRUIT_QTPY_RP2040)
#define HIDPIN_BOARD_ID HP_BOARD_QTPY_RP2040
#define HIDPIN_BOARD_AVAILABLE 0x3FD00078u  // GPIO3-6, 20, 22-29
#else
#error "Unsupported board: build with PICO_BOARD=pico or PICO_BOARD=adafruit_qtpy_rp2040"
#endif

#endif
