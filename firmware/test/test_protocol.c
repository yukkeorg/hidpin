#include "hidpin/protocol.h"
#include "test_util.h"
#include "vectors.h"

static void test_status_vectors(void)
{
    for (size_t i = 0; i < VEC_STATUS_COUNT; i++) {
        const vec_status_t *v = &VEC_STATUS[i];
        hp_status_t status;
        memset(&status, 0, sizeof(status));
        status.seq = v->seq;
        status.reason = v->reason;
        status.flags = v->flags;
        status.event_count = v->event_count;
        status.monitored = v->monitored;
        status.outputs = v->outputs;
        status.levels = v->levels;
        status.timestamp_us = v->timestamp_us;
        for (uint8_t k = 0; k < v->event_count; k++) {
            status.events[k].gpio = v->events[k].gpio;
            status.events[k].level = v->events[k].level != 0u;
            status.events[k].age_us = v->events[k].age_us;
        }

        uint8_t out[HP_REPORT_PAYLOAD_LEN];
        memset(out, 0xAA, sizeof(out));
        hp_status_encode(&status, out);
        EXPECT_BYTES(v->name, v->payload, out, HP_REPORT_PAYLOAD_LEN);
    }
}

static void test_device_info_vectors(void)
{
    for (size_t i = 0; i < VEC_DEVICE_INFO_COUNT; i++) {
        const vec_device_info_t *v = &VEC_DEVICE_INFO[i];
        hp_device_info_t info = {
            .fw_major = v->fw_major,
            .fw_minor = v->fw_minor,
            .fw_patch = v->fw_patch,
            .board = v->board,
            .available = v->available,
        };
        uint8_t out[HP_REPORT_PAYLOAD_LEN];
        memset(out, 0xAA, sizeof(out));
        hp_device_info_encode(&info, out);
        EXPECT_BYTES(v->name, v->payload, out, HP_REPORT_PAYLOAD_LEN);
    }
}

static void test_pin_config_vectors(void)
{
    for (size_t i = 0; i < VEC_PIN_CONFIG_COUNT; i++) {
        const vec_pin_config_t *v = &VEC_PIN_CONFIG[i];
        hp_pin_config_report_t report = {
            .result = v->result,
            .result_gpio = v->result_gpio,
            .request_id = v->request_id,
        };
        memcpy(report.config.mode, v->mode, sizeof(report.config.mode));
        memcpy(report.config.param, v->param, sizeof(report.config.param));
        uint8_t out[HP_REPORT_PAYLOAD_LEN];
        memset(out, 0xAA, sizeof(out));
        hp_pin_config_encode(&report, out);
        EXPECT_BYTES(v->name, v->payload, out, HP_REPORT_PAYLOAD_LEN);
    }
}

static void test_default_config_matches_vectors(void)
{
    const struct {
        const char *name;
        uint32_t available;
    } boards[] = {
        {"pico_default", 0x1C7FFFFFu},
        {"qtpy_rp2040_default", 0x3FD00078u},
    };
    for (size_t b = 0; b < sizeof(boards) / sizeof(boards[0]); b++) {
        const vec_pin_config_t *v = NULL;
        for (size_t i = 0; i < VEC_PIN_CONFIG_COUNT; i++) {
            if (strcmp(VEC_PIN_CONFIG[i].name, boards[b].name) == 0) {
                v = &VEC_PIN_CONFIG[i];
            }
        }
        EXPECT_TRUE(v != NULL);
        if (v == NULL) {
            continue;
        }
        hp_pin_config_t config;
        hp_pin_config_default(boards[b].available, &config);
        EXPECT_BYTES(v->name, v->mode, config.mode, HP_GPIO_COUNT);
        EXPECT_BYTES(v->name, v->param, config.param, HP_GPIO_COUNT);
    }
}

static void test_pin_config_set_vectors(void)
{
    for (size_t i = 0; i < VEC_PIN_CONFIG_SET_COUNT; i++) {
        const vec_pin_config_set_t *v = &VEC_PIN_CONFIG_SET[i];
        int failures_before = test_failures;
        hp_pin_config_t config;
        memset(&config, 0xEE, sizeof(config));
        uint8_t request_id = 0xEE;
        uint8_t result_gpio = 0xEE;
        uint8_t result = hp_pin_config_decode_set(v->payload, v->length, v->available, &config, &request_id,
                                                  &result_gpio);
        EXPECT_EQ(v->result, result);
        EXPECT_EQ(v->result_gpio, result_gpio);
        EXPECT_EQ(v->request_id, request_id);
        if (result == HP_RESULT_OK) {
            for (uint8_t n = 0; n < HP_GPIO_COUNT; n++) {
                EXPECT_EQ(v->payload[3 + 2 * n], config.mode[n]);
                EXPECT_EQ(v->payload[4 + 2 * n], config.param[n]);
            }
        } else {
            uint8_t untouched[sizeof(config)];
            memset(untouched, 0xEE, sizeof(untouched));
            EXPECT_BYTES(v->name, untouched, &config, sizeof(config));
        }
        if (test_failures != failures_before) {
            fprintf(stderr, "  in vector %s\n", v->name);
        }
    }
}

static void test_output_vectors(void)
{
    for (size_t i = 0; i < VEC_OUTPUT_COUNT; i++) {
        const vec_output_t *v = &VEC_OUTPUT[i];
        uint32_t mask = 0xDEADBEEFu;
        uint32_t value = 0xDEADBEEFu;
        bool valid = hp_output_decode(v->payload, v->length, &mask, &value);
        EXPECT_EQ(v->valid, valid);
        if (valid) {
            EXPECT_EQ(v->mask, mask);
            EXPECT_EQ(v->value, value);
        } else {
            EXPECT_EQ(0xDEADBEEFu, mask);
            EXPECT_EQ(0xDEADBEEFu, value);
        }
    }
}

static void test_report_descriptor_matches_spec(void)
{
    EXPECT_EQ(VEC_REPORT_DESCRIPTOR_LEN, HP_HID_REPORT_DESCRIPTOR_LEN);
    EXPECT_BYTES("report_descriptor", VEC_REPORT_DESCRIPTOR, hp_hid_report_descriptor, HP_HID_REPORT_DESCRIPTOR_LEN);
}

int main(void)
{
    RUN_TEST(test_report_descriptor_matches_spec);
    RUN_TEST(test_status_vectors);
    RUN_TEST(test_device_info_vectors);
    RUN_TEST(test_pin_config_vectors);
    RUN_TEST(test_default_config_matches_vectors);
    RUN_TEST(test_pin_config_set_vectors);
    RUN_TEST(test_output_vectors);
    TEST_EXIT();
}
