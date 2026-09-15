// Wire format of hidpin protocol version 1. See docs/PROTOCOL.md.
#ifndef HIDPIN_PROTOCOL_H
#define HIDPIN_PROTOCOL_H

#include <stdbool.h>
#include <stdint.h>

#define HP_PROTOCOL_VERSION 1u

#define HP_REPORT_ID_STATUS 0x01u
#define HP_REPORT_ID_DEVICE_INFO 0x02u
#define HP_REPORT_ID_PIN_CONFIG 0x03u
#define HP_REPORT_ID_OUTPUT 0x04u

// Payload lengths exclude the report ID byte.
#define HP_REPORT_PAYLOAD_LEN 63u
#define HP_OUTPUT_PAYLOAD_LEN 8u

#define HP_GPIO_COUNT 30u
#define HP_EVENTS_PER_REPORT 7u
#define HP_EVENT_QUEUE_SIZE 32u
#define HP_PERIODIC_INTERVAL_MS 1000u
#define HP_DEFAULT_DEBOUNCE_MS 20u
#define HP_SETTLE_US 1000u

#define HP_REASON_LEVEL_CHANGED 0x01u
#define HP_REASON_OUTPUT_APPLIED 0x02u
#define HP_REASON_CONFIG_CHANGED 0x04u
#define HP_REASON_PERIODIC 0x08u
#define HP_REASON_OUTPUT_RESET 0x10u
#define HP_REASON_HOST_REQUEST 0x80u

#define HP_FLAG_OVERFLOW 0x01u
#define HP_FLAG_MORE_EVENTS 0x02u

#define HP_MODE_UNUSED 0u
#define HP_MODE_INPUT_NOPULL 1u
#define HP_MODE_INPUT_PULLUP 2u
#define HP_MODE_INPUT_PULLDOWN 3u
#define HP_MODE_OUTPUT 4u

#define HP_RESULT_OK 0u
#define HP_RESULT_BAD_LENGTH 1u
#define HP_RESULT_BAD_MODE 2u
#define HP_RESULT_UNAVAILABLE_GPIO 3u
#define HP_RESULT_UNUSED_WITH_PARAM 4u
#define HP_RESULT_BAD_OUTPUT_LEVEL 5u
#define HP_RESULT_GPIO_NONE 0xFFu

#define HP_BOARD_PICO 1u
#define HP_BOARD_QTPY_RP2040 2u

#define HP_HID_REPORT_DESCRIPTOR_LEN 47u

// HID report descriptor of the vendor-defined collection (PROTOCOL.md 3.2).
extern const uint8_t hp_hid_report_descriptor[HP_HID_REPORT_DESCRIPTOR_LEN];

typedef struct {
    uint8_t gpio;
    bool level;
    uint32_t age_us;
} hp_status_event_t;

typedef struct {
    uint16_t seq;
    uint8_t reason;
    uint8_t flags;
    uint8_t event_count;
    uint32_t monitored;
    uint32_t outputs;
    uint32_t levels;
    uint64_t timestamp_us;
    hp_status_event_t events[HP_EVENTS_PER_REPORT];
} hp_status_t;

typedef struct {
    uint8_t fw_major;
    uint8_t fw_minor;
    uint8_t fw_patch;
    uint8_t board;
    uint32_t available;
} hp_device_info_t;

typedef struct {
    uint8_t mode[HP_GPIO_COUNT];
    uint8_t param[HP_GPIO_COUNT];
} hp_pin_config_t;

// Contents of the pin configuration Feature report as returned by Get_Report.
typedef struct {
    uint8_t result;
    uint8_t result_gpio;
    uint8_t request_id;
    hp_pin_config_t config;
} hp_pin_config_report_t;

bool hp_mode_is_input(uint8_t mode);

void hp_pin_config_default(uint32_t available, hp_pin_config_t *config);

// Each encoder writes exactly HP_REPORT_PAYLOAD_LEN bytes to out.
void hp_status_encode(const hp_status_t *status, uint8_t *out);
void hp_device_info_encode(const hp_device_info_t *info, uint8_t *out);
void hp_pin_config_encode(const hp_pin_config_report_t *report, uint8_t *out);

// Validates a Set_Report(Feature) payload for the pin configuration (PROTOCOL.md 6.3).
// Returns an HP_RESULT_* code. *config is written only when the result is HP_RESULT_OK.
uint8_t hp_pin_config_decode_set(const uint8_t *payload, uint16_t len, uint32_t available,
                                 hp_pin_config_t *config, uint8_t *request_id, uint8_t *result_gpio);

// Returns false (leaving *mask and *value untouched) when the payload length is not HP_OUTPUT_PAYLOAD_LEN.
bool hp_output_decode(const uint8_t *payload, uint16_t len, uint32_t *mask, uint32_t *value);

#endif
