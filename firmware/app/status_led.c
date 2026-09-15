#include "status_led.h"

#include "pico/stdlib.h"

#if defined(PICO_DEFAULT_WS2812_PIN)
#include "hardware/pio.h"
#include "ws2812.pio.h"
#endif

#define WS2812_FREQ_HZ 800000.0f
#define WS2812_ON_GRB 0x100000u  // dim green

static bool led_state = false;

#if defined(PICO_DEFAULT_WS2812_PIN)
static PIO ws2812_pio = pio0;
static uint ws2812_sm = 0;

static void ws2812_put(uint32_t grb)
{
    pio_sm_put_blocking(ws2812_pio, ws2812_sm, grb << 8u);
}
#endif

void status_led_init(void)
{
#if defined(PICO_DEFAULT_LED_PIN)
    gpio_init(PICO_DEFAULT_LED_PIN);
    gpio_set_dir(PICO_DEFAULT_LED_PIN, GPIO_OUT);
    gpio_put(PICO_DEFAULT_LED_PIN, false);
#elif defined(PICO_DEFAULT_WS2812_PIN)
#if defined(PICO_DEFAULT_WS2812_POWER_PIN)
    gpio_init(PICO_DEFAULT_WS2812_POWER_PIN);
    gpio_set_dir(PICO_DEFAULT_WS2812_POWER_PIN, GPIO_OUT);
    gpio_put(PICO_DEFAULT_WS2812_POWER_PIN, true);
#endif
    uint offset = pio_add_program(ws2812_pio, &ws2812_program);
    ws2812_program_init(ws2812_pio, ws2812_sm, offset, PICO_DEFAULT_WS2812_PIN, WS2812_FREQ_HZ, false);
    ws2812_put(0u);
#endif
    led_state = false;
}

void status_led_set(bool on)
{
    if (on == led_state) {
        return;
    }
    led_state = on;
#if defined(PICO_DEFAULT_LED_PIN)
    gpio_put(PICO_DEFAULT_LED_PIN, on);
#elif defined(PICO_DEFAULT_WS2812_PIN)
    ws2812_put(on ? WS2812_ON_GRB : 0u);
#endif
}
