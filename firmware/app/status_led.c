#include "status_led.h"

#include <stdbool.h>

#include "pico/stdlib.h"

#if defined(PICO_DEFAULT_WS2812_PIN)
#include "hardware/pio.h"
#include "ws2812.pio.h"
#endif

#define WS2812_FREQ_HZ 800000.0f
// Colours are in GRB order; kept dim.
#define WS2812_GREEN 0x100000u
#define WS2812_BLUE 0x000010u

static status_led_state_t led_state;
static bool led_initialised = false;

#if defined(PICO_DEFAULT_WS2812_PIN)
static PIO ws2812_pio = pio0;
static uint ws2812_sm = 0;

static void ws2812_put(uint32_t grb)
{
    pio_sm_put_blocking(ws2812_pio, ws2812_sm, grb << 8u);
}
#endif

static void apply(status_led_state_t state)
{
#if defined(PICO_DEFAULT_LED_PIN)
    gpio_put(PICO_DEFAULT_LED_PIN, state == STATUS_LED_POWER);
#elif defined(PICO_DEFAULT_WS2812_PIN)
    ws2812_put(state == STATUS_LED_POWER ? WS2812_GREEN : WS2812_BLUE);
#else
    (void)state;
#endif
}

void status_led_init(void)
{
#if defined(PICO_DEFAULT_LED_PIN)
    gpio_init(PICO_DEFAULT_LED_PIN);
    gpio_set_dir(PICO_DEFAULT_LED_PIN, GPIO_OUT);
#elif defined(PICO_DEFAULT_WS2812_PIN)
#if defined(PICO_DEFAULT_WS2812_POWER_PIN)
    gpio_init(PICO_DEFAULT_WS2812_POWER_PIN);
    gpio_set_dir(PICO_DEFAULT_WS2812_POWER_PIN, GPIO_OUT);
    gpio_put(PICO_DEFAULT_WS2812_POWER_PIN, true);
#endif
    uint offset = pio_add_program(ws2812_pio, &ws2812_program);
    ws2812_program_init(ws2812_pio, ws2812_sm, offset, PICO_DEFAULT_WS2812_PIN, WS2812_FREQ_HZ, false);
#endif
    led_state = STATUS_LED_POWER;
    led_initialised = true;
    apply(led_state);
}

void status_led_show(status_led_state_t state)
{
    if (!led_initialised || state == led_state) {
        return;
    }
    led_state = state;
    apply(state);
}
