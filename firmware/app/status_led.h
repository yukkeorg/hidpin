// On-board LED showing whether the host has configured the device.
#ifndef HIDPIN_STATUS_LED_H
#define HIDPIN_STATUS_LED_H

#include <stdbool.h>

void status_led_init(void);
void status_led_set(bool on);

#endif
