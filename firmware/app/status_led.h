// Status LED (the board's own, or the one set with HIDPIN_STATUS_LED_PIN): lit while powered,
// changing briefly on GPIO activity.
#ifndef HIDPIN_STATUS_LED_H
#define HIDPIN_STATUS_LED_H

typedef enum {
    STATUS_LED_POWER,     // plain LED: on, WS2812: green
    STATUS_LED_ACTIVITY,  // plain LED: off, WS2812: blue
} status_led_state_t;

void status_led_init(void);
void status_led_show(status_led_state_t state);

#endif
