#include "hidpin/protocol.h"

#include <string.h>

#define STATUS_EVENTS_OFFSET 28u
#define STATUS_EVENT_LEN 5u
#define PIN_CONFIG_ENTRIES_OFFSET 3u

const uint8_t hp_hid_report_descriptor[HP_HID_REPORT_DESCRIPTOR_LEN] = {
    0x06, 0x00, 0xFF,  // Usage Page (0xFF00)
    0x09, 0x01,        // Usage (0x01)
    0xA1, 0x01,        // Collection (Application)
    0x85, 0x01,        //   Report ID (1): status
    0x09, 0x10,        //   Usage (0x10)
    0x15, 0x00,        //   Logical Minimum (0)
    0x26, 0xFF, 0x00,  //   Logical Maximum (255)
    0x75, 0x08,        //   Report Size (8)
    0x95, 0x3F,        //   Report Count (63)
    0x81, 0x02,        //   Input (Data,Var,Abs)
    0x85, 0x02,        //   Report ID (2): device information
    0x09, 0x20,        //   Usage (0x20)
    0x95, 0x3F,        //   Report Count (63)
    0xB1, 0x02,        //   Feature (Data,Var,Abs)
    0x85, 0x03,        //   Report ID (3): pin configuration
    0x09, 0x30,        //   Usage (0x30)
    0x95, 0x3F,        //   Report Count (63)
    0xB1, 0x02,        //   Feature (Data,Var,Abs)
    0x85, 0x04,        //   Report ID (4): output
    0x09, 0x40,        //   Usage (0x40)
    0x95, 0x08,        //   Report Count (8)
    0x91, 0x02,        //   Output (Data,Var,Abs)
    0xC0,              // End Collection
};

static void put_u16(uint8_t *p, uint16_t v)
{
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
}

static void put_u32(uint8_t *p, uint32_t v)
{
    for (unsigned i = 0u; i < 4u; i++) {
        p[i] = (uint8_t)(v >> (8u * i));
    }
}

static void put_u64(uint8_t *p, uint64_t v)
{
    for (unsigned i = 0u; i < 8u; i++) {
        p[i] = (uint8_t)(v >> (8u * i));
    }
}

static uint32_t get_u32(const uint8_t *p)
{
    uint32_t v = 0u;
    for (unsigned i = 0u; i < 4u; i++) {
        v |= (uint32_t)p[i] << (8u * i);
    }
    return v;
}

bool hp_mode_is_input(uint8_t mode)
{
    return mode >= HP_MODE_INPUT_NOPULL && mode <= HP_MODE_INPUT_PULLDOWN;
}

void hp_pin_config_default(uint32_t available, hp_pin_config_t *config)
{
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        if ((available >> n) & 1u) {
            config->mode[n] = HP_MODE_INPUT_PULLUP;
            config->param[n] = HP_DEFAULT_DEBOUNCE_MS;
        } else {
            config->mode[n] = HP_MODE_UNUSED;
            config->param[n] = 0u;
        }
    }
}

void hp_status_encode(const hp_status_t *status, uint8_t *out)
{
    uint8_t count = status->event_count;
    if (count > HP_EVENTS_PER_REPORT) {
        count = HP_EVENTS_PER_REPORT;
    }

    memset(out, 0, HP_REPORT_PAYLOAD_LEN);
    put_u16(out, status->seq);
    out[2] = status->reason;
    out[3] = status->flags;
    out[4] = count;
    put_u32(out + 8, status->monitored);
    put_u32(out + 12, status->outputs);
    put_u32(out + 16, status->levels);
    put_u64(out + 20, status->timestamp_us);

    for (uint8_t i = 0u; i < count; i++) {
        uint8_t *event = out + STATUS_EVENTS_OFFSET + STATUS_EVENT_LEN * i;
        event[0] = (uint8_t)((status->events[i].gpio & 0x1Fu) | (status->events[i].level ? 0x80u : 0u));
        put_u32(event + 1, status->events[i].age_us);
    }
}

void hp_device_info_encode(const hp_device_info_t *info, uint8_t *out)
{
    memset(out, 0, HP_REPORT_PAYLOAD_LEN);
    out[0] = HP_PROTOCOL_VERSION;
    out[1] = info->fw_major;
    out[2] = info->fw_minor;
    out[3] = info->fw_patch;
    out[4] = info->board;
    out[5] = HP_GPIO_COUNT;
    put_u16(out + 6, HP_PERIODIC_INTERVAL_MS);
    put_u32(out + 8, info->available);
    out[12] = HP_EVENTS_PER_REPORT;
    out[13] = HP_EVENT_QUEUE_SIZE;
}

void hp_pin_config_encode(const hp_pin_config_report_t *report, uint8_t *out)
{
    out[0] = report->result;
    out[1] = report->result_gpio;
    out[2] = report->request_id;
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        out[PIN_CONFIG_ENTRIES_OFFSET + 2u * n] = report->config.mode[n];
        out[PIN_CONFIG_ENTRIES_OFFSET + 2u * n + 1u] = report->config.param[n];
    }
}

uint8_t hp_pin_config_decode_set(const uint8_t *payload, uint16_t len, uint32_t available,
                                 hp_pin_config_t *config, uint8_t *request_id, uint8_t *result_gpio)
{
    *request_id = (len >= 3u) ? payload[2] : 0u;
    *result_gpio = HP_RESULT_GPIO_NONE;
    if (len != HP_REPORT_PAYLOAD_LEN) {
        return HP_RESULT_BAD_LENGTH;
    }

    hp_pin_config_t parsed;
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        uint8_t mode = payload[PIN_CONFIG_ENTRIES_OFFSET + 2u * n];
        uint8_t param = payload[PIN_CONFIG_ENTRIES_OFFSET + 2u * n + 1u];
        *result_gpio = n;
        if (mode > HP_MODE_OUTPUT) {
            return HP_RESULT_BAD_MODE;
        }
        if (((available >> n) & 1u) == 0u && mode != HP_MODE_UNUSED) {
            return HP_RESULT_UNAVAILABLE_GPIO;
        }
        if (mode == HP_MODE_UNUSED && param != 0u) {
            return HP_RESULT_UNUSED_WITH_PARAM;
        }
        if (mode == HP_MODE_OUTPUT && param > 1u) {
            return HP_RESULT_BAD_OUTPUT_LEVEL;
        }
        parsed.mode[n] = mode;
        parsed.param[n] = param;
    }

    *result_gpio = HP_RESULT_GPIO_NONE;
    *config = parsed;
    return HP_RESULT_OK;
}

bool hp_output_decode(const uint8_t *payload, uint16_t len, uint32_t *mask, uint32_t *value)
{
    if (len != HP_OUTPUT_PAYLOAD_LEN) {
        return false;
    }
    *mask = get_u32(payload);
    *value = get_u32(payload + 4);
    return true;
}
