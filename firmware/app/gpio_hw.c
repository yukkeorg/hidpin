#include "gpio_hw.h"

#include "hardware/gpio.h"
#include "hardware/irq.h"
#include "hardware/sync.h"
#include "pico/time.h"

#define EDGE_EVENTS (GPIO_IRQ_EDGE_RISE | GPIO_IRQ_EDGE_FALL)
#define EDGE_BUFFER_SIZE 256u  // power of two

// Single producer (GPIO interrupt) / single consumer (main loop) ring buffer.
static raw_edge_t edge_buffer[EDGE_BUFFER_SIZE];
static volatile uint16_t edge_head;
static volatile uint16_t edge_tail;
static volatile bool edge_overflow;

static void gpio_edge_irq(uint gpio, uint32_t event_mask)
{
    uint64_t now_us = time_us_64();
    bool rise = (event_mask & GPIO_IRQ_EDGE_RISE) != 0u;
    bool fall = (event_mask & GPIO_IRQ_EDGE_FALL) != 0u;
    bool level;
    if (rise && !fall) {
        level = true;
    } else if (fall && !rise) {
        level = false;
    } else {
        // Both edges were latched before we ran: only the current value is meaningful.
        level = gpio_get(gpio);
    }

    uint16_t next = (uint16_t)((edge_head + 1u) & (EDGE_BUFFER_SIZE - 1u));
    if (next == edge_tail) {
        edge_overflow = true;
        return;
    }
    edge_buffer[edge_head] = (raw_edge_t){.gpio = (uint8_t)gpio, .level = level, .time_us = now_us};
    edge_head = next;
}

static void hw_set_unused(void *ctx, uint8_t gpio)
{
    (void)ctx;
    gpio_set_irq_enabled(gpio, EDGE_EVENTS, false);
    gpio_deinit(gpio);
    gpio_disable_pulls(gpio);
    gpio_set_input_enabled(gpio, false);
}

static void hw_set_input(void *ctx, uint8_t gpio, uint8_t mode)
{
    (void)ctx;
    gpio_set_irq_enabled(gpio, EDGE_EVENTS, false);
    gpio_init(gpio);
    gpio_set_pulls(gpio, mode == HP_MODE_INPUT_PULLUP, mode == HP_MODE_INPUT_PULLDOWN);
    gpio_set_input_enabled(gpio, true);
    gpio_acknowledge_irq(gpio, EDGE_EVENTS);
    gpio_set_irq_enabled(gpio, EDGE_EVENTS, true);
}

static void hw_set_output(void *ctx, uint8_t gpio, bool level)
{
    (void)ctx;
    gpio_set_irq_enabled(gpio, EDGE_EVENTS, false);
    // Latch the level before enabling the driver so the pin never glitches.
    gpio_put(gpio, level);
    gpio_set_dir(gpio, GPIO_OUT);
    gpio_set_function(gpio, GPIO_FUNC_SIO);
    gpio_disable_pulls(gpio);
}

static void hw_write_outputs(void *ctx, uint32_t mask, uint32_t value)
{
    (void)ctx;
    gpio_put_masked(mask, value);
}

static uint32_t hw_read_inputs(void *ctx)
{
    (void)ctx;
    return gpio_get_all();
}

void gpio_hw_init(hp_hw_t *hw)
{
    edge_head = 0u;
    edge_tail = 0u;
    edge_overflow = false;
    gpio_set_irq_callback(gpio_edge_irq);
    irq_set_enabled(IO_IRQ_BANK0, true);

    *hw = (hp_hw_t){
        .ctx = NULL,
        .set_unused = hw_set_unused,
        .set_input = hw_set_input,
        .set_output = hw_set_output,
        .write_outputs = hw_write_outputs,
        .read_inputs = hw_read_inputs,
    };
}

bool gpio_hw_pop_edge(raw_edge_t *edge)
{
    uint16_t tail = edge_tail;
    if (tail == edge_head) {
        return false;
    }
    *edge = edge_buffer[tail];
    edge_tail = (uint16_t)((tail + 1u) & (EDGE_BUFFER_SIZE - 1u));
    return true;
}

bool gpio_hw_take_overflow(void)
{
    uint32_t saved = save_and_disable_interrupts();
    bool overflow = edge_overflow;
    edge_overflow = false;
    restore_interrupts(saved);
    return overflow;
}
