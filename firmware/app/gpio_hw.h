// GPIO access for the engine and the edge interrupt buffer.
#ifndef HIDPIN_GPIO_HW_H
#define HIDPIN_GPIO_HW_H

#include <stdbool.h>
#include <stdint.h>

#include "hidpin/engine.h"

typedef struct {
    uint8_t gpio;
    bool level;
    uint64_t time_us;
} raw_edge_t;

// Installs the GPIO interrupt handler and fills in the engine's hardware callbacks.
void gpio_hw_init(hp_hw_t *hw);

// Takes the oldest edge recorded by the interrupt handler. Main loop only.
bool gpio_hw_pop_edge(raw_edge_t *edge);

// Returns true once after edges were dropped because the buffer was full. Main loop only.
bool gpio_hw_take_overflow(void);

#endif
