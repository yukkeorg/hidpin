// On-board status LED: lit while powered, flashing on GPIO activity.
#ifndef HIDPIN_STATUS_LED_H
#define HIDPIN_STATUS_LED_H

typedef enum {
    STATUS_LED_POWER,     // Pico: on, QT Py: green
    STATUS_LED_ACTIVITY,  // Pico: off, QT Py: blue
} status_led_state_t;

void status_led_init(void);
void status_led_show(status_led_state_t state);

#endif
