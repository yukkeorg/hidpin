#include <stdio.h>
#include <string.h>

#include "hardware/gpio.h"
#include "pico/stdlib.h"
#include "tusb.h"

#include "board.h"
#include "gpio_hw.h"
#include "hidpin/engine.h"
#include "status_led.h"

#if HIDPIN_DEBUG
#define LOG(...) printf(__VA_ARGS__)
#else
// Keeps the arguments referenced and format-checked while generating no code.
#define LOG(...) \
    do { \
        if (0) { \
            printf(__VA_ARGS__); \
        } \
    } while (0)
#endif

// How long the LED shows activity after an edge event or an applied output report.
#define ACTIVITY_FLASH_US 100000u

// Accessed only from the main loop and from TinyUSB callbacks, which run inside tud_task().
static hp_engine_t engine;

static void send_status_report(uint64_t now_us)
{
    uint8_t report[HP_REPORT_PAYLOAD_LEN];
    if (!hp_engine_prepare_report(&engine, now_us, report)) {
        return;
    }
    if (tud_hid_report(HP_REPORT_ID_STATUS, report, sizeof(report))) {
        hp_engine_commit_report(&engine, now_us);
    }
}

uint16_t tud_hid_get_report_cb(uint8_t instance, uint8_t report_id, hid_report_type_t report_type, uint8_t *buffer,
                               uint16_t reqlen)
{
    (void)instance;
    uint8_t payload[HP_REPORT_PAYLOAD_LEN];

    if (report_type == HID_REPORT_TYPE_INPUT && report_id == HP_REPORT_ID_STATUS) {
        hp_engine_get_status(&engine, time_us_64(), payload);
    } else if (report_type == HID_REPORT_TYPE_FEATURE && report_id == HP_REPORT_ID_DEVICE_INFO) {
        hp_engine_get_device_info(&engine, payload);
    } else if (report_type == HID_REPORT_TYPE_FEATURE && report_id == HP_REPORT_ID_PIN_CONFIG) {
        hp_engine_get_pin_config(&engine, payload);
    } else {
        return 0;
    }

    uint16_t len = reqlen < sizeof(payload) ? reqlen : (uint16_t)sizeof(payload);
    memcpy(buffer, payload, len);
    return len;
}

void tud_hid_set_report_cb(uint8_t instance, uint8_t report_id, hid_report_type_t report_type, uint8_t const *buffer,
                           uint16_t bufsize)
{
    (void)instance;
    if (report_id == 0u && report_type == HID_REPORT_TYPE_OUTPUT && bufsize > 0u) {
        // Interrupt OUT transfers arrive with the report ID still at the start of the buffer.
        report_id = buffer[0];
        buffer++;
        bufsize--;
    }

    if (report_type == HID_REPORT_TYPE_FEATURE && report_id == HP_REPORT_ID_PIN_CONFIG) {
        hp_engine_set_pin_config(&engine, buffer, bufsize, time_us_64());
        LOG("pin config: request %u result %u gpio %u\n", engine.request_id, engine.result, engine.result_gpio);
    } else if (report_type == HID_REPORT_TYPE_OUTPUT && report_id == HP_REPORT_ID_OUTPUT) {
        bool applied = hp_engine_output(&engine, buffer, bufsize);
        LOG("output: %s (%u bytes)\n", applied ? "applied" : "ignored", bufsize);
    }
}

void tud_suspend_cb(bool remote_wakeup_en)
{
    (void)remote_wakeup_en;
    hp_engine_usb_reset(&engine);
    LOG("usb suspended\n");
}

int main(void)
{
    status_led_init();

    hp_hw_t hw;
    gpio_hw_init(&hw);
    hp_device_info_t info = {
        .fw_major = HIDPIN_FW_MAJOR,
        .fw_minor = HIDPIN_FW_MINOR,
        .fw_patch = HIDPIN_FW_PATCH,
        .board = HIDPIN_BOARD_ID,
        .available = HIDPIN_BOARD_AVAILABLE,
    };
    hp_engine_init(&engine, &hw, &info, time_us_64());

    tud_init(BOARD_TUD_RHPORT);
#if HIDPIN_DEBUG
    stdio_init_all();
#endif

    bool was_mounted = false;
    uint32_t seen_activity = hp_engine_activity(&engine);
    uint64_t activity_until_us = 0;
    while (true) {
        tud_task();

        raw_edge_t edge;
        while (gpio_hw_pop_edge(&edge)) {
            hp_engine_on_edge(&engine, edge.gpio, edge.level, edge.time_us);
        }
        uint64_t now_us = time_us_64();
        if (gpio_hw_take_overflow()) {
            hp_engine_resync(&engine, gpio_get_all(), now_us);
            LOG("edge buffer overflow: resynchronised\n");
        }
        hp_engine_task(&engine, now_us);

        // Bus reset does not invoke tud_umount_cb, so detect every loss of configuration here.
        bool mounted = tud_mounted();
        if (was_mounted && !mounted) {
            hp_engine_usb_reset(&engine);
            LOG("usb unconfigured\n");
        }
        was_mounted = mounted;

        if (mounted && !tud_suspended() && tud_hid_ready()) {
            send_status_report(now_us);
        }

        uint32_t activity = hp_engine_activity(&engine);
        if (activity != seen_activity) {
            seen_activity = activity;
            activity_until_us = now_us + ACTIVITY_FLASH_US;
        }
        status_led_show(now_us < activity_until_us ? STATUS_LED_ACTIVITY : STATUS_LED_POWER);
    }
}
