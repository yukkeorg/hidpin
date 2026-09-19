#include "hidpin/engine.h"
#include "test_util.h"

#define PICO_AVAILABLE 0x1C7FFFFFu
#define BIT(n) ((uint32_t)1u << (n))

typedef struct {
    uint32_t raw;
    char kind[HP_GPIO_COUNT];  // 0 = untouched, 'u' = unused, 'i' = input, 'o' = output
    uint8_t input_mode[HP_GPIO_COUNT];
    uint32_t driven;
    int configure_calls;
    int write_calls;
} fake_hw_t;

static fake_hw_t hw;
static hp_engine_t engine;

static void fake_set_unused(void *ctx, uint8_t gpio)
{
    fake_hw_t *fake = ctx;
    fake->kind[gpio] = 'u';
    fake->configure_calls++;
}

static void fake_set_input(void *ctx, uint8_t gpio, uint8_t mode)
{
    fake_hw_t *fake = ctx;
    fake->kind[gpio] = 'i';
    fake->input_mode[gpio] = mode;
    fake->configure_calls++;
}

static void fake_set_output(void *ctx, uint8_t gpio, bool level)
{
    fake_hw_t *fake = ctx;
    fake->kind[gpio] = 'o';
    fake->driven = level ? (fake->driven | BIT(gpio)) : (fake->driven & ~BIT(gpio));
    fake->configure_calls++;
}

static void fake_write_outputs(void *ctx, uint32_t mask, uint32_t value)
{
    fake_hw_t *fake = ctx;
    fake->driven = (fake->driven & ~mask) | (value & mask);
    fake->write_calls++;
}

static uint32_t fake_read_inputs(void *ctx)
{
    fake_hw_t *fake = ctx;
    return fake->raw;
}

static uint32_t read_u32(const uint8_t *p)
{
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

static void decode_status(const uint8_t *p, hp_status_t *s)
{
    memset(s, 0, sizeof(*s));
    s->seq = (uint16_t)(p[0] | (p[1] << 8));
    s->reason = p[2];
    s->flags = p[3];
    s->event_count = p[4];
    s->monitored = read_u32(p + 8);
    s->outputs = read_u32(p + 12);
    s->levels = read_u32(p + 16);
    s->timestamp_us = (uint64_t)read_u32(p + 20) | ((uint64_t)read_u32(p + 24) << 32);
    for (uint8_t i = 0; i < s->event_count && i < HP_EVENTS_PER_REPORT; i++) {
        const uint8_t *event = p + 28 + 5 * i;
        s->events[i].gpio = event[0] & 0x1Fu;
        s->events[i].level = (event[0] & 0x80u) != 0u;
        s->events[i].age_us = read_u32(event + 1);
    }
}

static void init_engine(uint32_t raw)
{
    memset(&hw, 0, sizeof(hw));
    hw.raw = raw;
    hp_hw_t ops = {
        .ctx = &hw,
        .set_unused = fake_set_unused,
        .set_input = fake_set_input,
        .set_output = fake_set_output,
        .write_outputs = fake_write_outputs,
        .read_inputs = fake_read_inputs,
    };
    hp_device_info_t info = {.fw_major = 0, .fw_minor = 1, .fw_patch = 0, .board = HP_BOARD_PICO,
                             .available = PICO_AVAILABLE};
    hp_engine_init(&engine, &ops, &info, 0);
}

// Pull-ups hold every input high; the initial settle completes at 1 ms.
static void setup(void)
{
    init_engine(0x3FFFFFFFu);
    hp_engine_task(&engine, HP_SETTLE_US);
}

static bool next_report(uint64_t now_us, hp_status_t *status)
{
    uint8_t buf[HP_REPORT_PAYLOAD_LEN];
    if (!hp_engine_prepare_report(&engine, now_us, buf)) {
        return false;
    }
    hp_engine_commit_report(&engine, now_us);
    decode_status(buf, status);
    return true;
}

static void get_status(uint64_t now_us, hp_status_t *status)
{
    uint8_t buf[HP_REPORT_PAYLOAD_LEN];
    hp_engine_get_status(&engine, now_us, buf);
    decode_status(buf, status);
}

static void set_config(const hp_pin_config_t *config, uint8_t request_id, uint64_t now_us)
{
    hp_pin_config_report_t report = {.result = 0, .result_gpio = 0xFF, .request_id = request_id, .config = *config};
    uint8_t buf[HP_REPORT_PAYLOAD_LEN];
    hp_pin_config_encode(&report, buf);
    hp_engine_set_pin_config(&engine, buf, sizeof(buf), now_us);
}

static bool send_output(uint32_t mask, uint32_t value, uint16_t len)
{
    uint8_t buf[16] = {0};
    for (unsigned i = 0; i < 4; i++) {
        buf[i] = (uint8_t)(mask >> (8 * i));
        buf[4 + i] = (uint8_t)(value >> (8 * i));
    }
    return hp_engine_output(&engine, buf, len);
}

// Sets GPIO0-9 to debounce 0 so that each edge becomes an event immediately.
static void use_zero_debounce_on_low_pins(uint64_t now_us)
{
    hp_pin_config_t config;
    hp_pin_config_default(PICO_AVAILABLE, &config);
    for (uint8_t n = 0; n < 10; n++) {
        config.param[n] = 0;
    }
    set_config(&config, 1, now_us);
    hp_engine_task(&engine, now_us + HP_SETTLE_US);
    hp_status_t status;
    EXPECT_TRUE(next_report(now_us + HP_SETTLE_US, &status));
    EXPECT_EQ(HP_REASON_CONFIG_CHANGED, status.reason);
}

static void test_init_touches_only_available_pins(void)
{
    init_engine(0x3FFFFFFFu & ~BIT(26));
    for (uint8_t n = 0; n < HP_GPIO_COUNT; n++) {
        if ((PICO_AVAILABLE & BIT(n)) != 0u) {
            EXPECT_EQ('i', hw.kind[n]);
            EXPECT_EQ(HP_MODE_INPUT_PULLUP, hw.input_mode[n]);
        } else {
            EXPECT_EQ(0, hw.kind[n]);
        }
    }

    hp_status_t status;
    hp_engine_task(&engine, 999);
    get_status(999, &status);
    EXPECT_EQ(HP_REASON_HOST_REQUEST, status.reason);
    EXPECT_EQ(0, status.monitored);

    hp_engine_task(&engine, 1000);
    get_status(1000, &status);
    EXPECT_EQ(PICO_AVAILABLE, status.monitored);
    EXPECT_EQ(PICO_AVAILABLE & ~BIT(26), status.levels);

    uint8_t buf[HP_REPORT_PAYLOAD_LEN];
    EXPECT_TRUE(!hp_engine_prepare_report(&engine, 1000, buf));
}

static void test_periodic_report_and_seq(void)
{
    setup();
    hp_status_t status;
    hp_engine_task(&engine, 999999);
    EXPECT_TRUE(!next_report(999999, &status));

    hp_engine_task(&engine, 1000000);
    EXPECT_TRUE(next_report(1000000, &status));
    EXPECT_EQ(0, status.seq);
    EXPECT_EQ(HP_REASON_PERIODIC, status.reason);
    EXPECT_EQ(0, status.flags);
    EXPECT_EQ(0, status.event_count);
    EXPECT_EQ(PICO_AVAILABLE, status.levels);
    EXPECT_EQ(1000000, status.timestamp_us);
    EXPECT_TRUE(!next_report(1000000, &status));

    hp_engine_task(&engine, 2000000);
    EXPECT_TRUE(next_report(2000000, &status));
    EXPECT_EQ(1, status.seq);

    get_status(2000001, &status);
    EXPECT_EQ(1, status.seq);
}

static void test_uncommitted_report_is_rebuilt(void)
{
    setup();
    uint8_t buf[HP_REPORT_PAYLOAD_LEN];
    hp_engine_task(&engine, 1000000);
    EXPECT_TRUE(hp_engine_prepare_report(&engine, 1000000, buf));
    EXPECT_TRUE(hp_engine_prepare_report(&engine, 1000100, buf));
    hp_status_t status;
    decode_status(buf, &status);
    EXPECT_EQ(0, status.seq);
    hp_engine_commit_report(&engine, 1000100);
    hp_engine_commit_report(&engine, 1000200);
    EXPECT_EQ(1, engine.next_seq);
}

static void test_press_with_bounce_yields_one_event(void)
{
    setup();
    hp_engine_on_edge(&engine, 5, false, 2000000);
    hp_engine_on_edge(&engine, 5, true, 2001000);
    hp_engine_on_edge(&engine, 5, false, 2003000);
    hp_engine_task(&engine, 2022999);
    EXPECT_EQ(0, engine.queue.count);
    hp_engine_task(&engine, 2023000);
    EXPECT_EQ(1, engine.queue.count);

    hp_status_t status;
    EXPECT_TRUE(next_report(2023500, &status));
    EXPECT_TRUE((status.reason & HP_REASON_LEVEL_CHANGED) != 0u);
    EXPECT_EQ(0, status.flags);
    EXPECT_EQ(1, status.event_count);
    EXPECT_EQ(5, status.events[0].gpio);
    EXPECT_TRUE(!status.events[0].level);
    EXPECT_EQ(20500, status.events[0].age_us);
    EXPECT_EQ(PICO_AVAILABLE & ~BIT(5), status.levels);
}

static void test_events_split_across_reports(void)
{
    setup();
    use_zero_debounce_on_low_pins(10000);
    for (uint8_t n = 0; n < 10; n++) {
        hp_engine_on_edge(&engine, n, false, 20000u + n);
    }

    hp_status_t first;
    EXPECT_TRUE(next_report(30000, &first));
    EXPECT_EQ(7, first.event_count);
    EXPECT_EQ(HP_FLAG_MORE_EVENTS, first.flags);
    EXPECT_EQ(HP_REASON_LEVEL_CHANGED, first.reason);
    EXPECT_EQ(PICO_AVAILABLE & ~0x7Fu, first.levels);
    EXPECT_EQ(6, first.events[6].gpio);
    EXPECT_EQ(30000u - 20006u, first.events[6].age_us);

    hp_status_t second;
    EXPECT_TRUE(next_report(30100, &second));
    EXPECT_EQ(3, second.event_count);
    EXPECT_EQ(0, second.flags);
    EXPECT_EQ(PICO_AVAILABLE & ~0x3FFu, second.levels);
    EXPECT_EQ(first.seq + 1u, second.seq);
}

static void test_overflow_reports_confirmed_levels(void)
{
    setup();
    use_zero_debounce_on_low_pins(10000);
    // 41 toggles on GPIO0 starting with a fall; the last one leaves the pin low.
    for (uint8_t i = 0; i < 41; i++) {
        hp_engine_on_edge(&engine, 0, (i % 2u) == 1u, 50000u + i);
    }
    EXPECT_EQ(HP_EVENT_QUEUE_SIZE, engine.queue.count);
    EXPECT_TRUE(engine.queue.overflowed);

    hp_status_t status;
    for (int batch = 0; batch < 4; batch++) {
        EXPECT_TRUE(next_report(60000u + (uint64_t)batch, &status));
        EXPECT_EQ(7, status.event_count);
        EXPECT_EQ(HP_FLAG_MORE_EVENTS, status.flags);
    }
    EXPECT_TRUE(next_report(60010, &status));
    EXPECT_EQ(4, status.event_count);
    EXPECT_EQ(HP_FLAG_OVERFLOW, status.flags);
    // The last reported event (the 32nd toggle) is a rise, but the confirmed level is low.
    EXPECT_TRUE(status.events[3].level);
    EXPECT_EQ(0, status.levels & BIT(0));
    EXPECT_TRUE(!engine.queue.overflowed);
}

static void test_get_status_does_not_consume_events(void)
{
    setup();
    use_zero_debounce_on_low_pins(10000);
    hp_engine_on_edge(&engine, 0, false, 20000);

    hp_status_t status;
    get_status(20100, &status);
    EXPECT_EQ(HP_REASON_HOST_REQUEST, status.reason);
    EXPECT_EQ(0, status.event_count);
    EXPECT_EQ(HP_FLAG_MORE_EVENTS, status.flags);
    EXPECT_EQ(0, status.levels & BIT(0));
    EXPECT_EQ(0, status.seq);
    EXPECT_EQ(1, engine.queue.count);
}

static void test_param_only_change_keeps_level(void)
{
    setup();
    hp_status_t status;
    hp_engine_on_edge(&engine, 5, false, 100000);
    hp_engine_task(&engine, 120000);
    EXPECT_TRUE(next_report(120000, &status));

    int calls = hw.configure_calls;
    hp_pin_config_t config;
    hp_pin_config_default(PICO_AVAILABLE, &config);
    config.param[5] = 50;
    set_config(&config, 7, 130000);
    EXPECT_EQ(calls, hw.configure_calls);

    uint8_t buf[HP_REPORT_PAYLOAD_LEN];
    hp_engine_get_pin_config(&engine, buf);
    EXPECT_EQ(HP_RESULT_OK, buf[0]);
    EXPECT_EQ(HP_RESULT_GPIO_NONE, buf[1]);
    EXPECT_EQ(7, buf[2]);
    EXPECT_EQ(HP_MODE_INPUT_PULLUP, buf[3 + 2 * 5]);
    EXPECT_EQ(50, buf[4 + 2 * 5]);

    hp_engine_task(&engine, 131000);
    EXPECT_TRUE(next_report(131000, &status));
    EXPECT_EQ(HP_REASON_CONFIG_CHANGED, status.reason);
    EXPECT_EQ(0, status.levels & BIT(5));
}

static void test_pull_change_resettles_from_raw(void)
{
    setup();
    hp_pin_config_t config;
    hp_pin_config_default(PICO_AVAILABLE, &config);
    config.mode[6] = HP_MODE_INPUT_PULLDOWN;
    set_config(&config, 3, 10000);
    EXPECT_EQ('i', hw.kind[6]);
    EXPECT_EQ(HP_MODE_INPUT_PULLDOWN, hw.input_mode[6]);
    hw.raw &= ~BIT(6);

    hp_status_t status;
    hp_engine_task(&engine, 10500);
    get_status(10500, &status);
    EXPECT_TRUE((status.levels & BIT(6)) != 0u);

    hp_engine_task(&engine, 11000);
    EXPECT_TRUE(next_report(11000, &status));
    EXPECT_EQ(HP_REASON_CONFIG_CHANGED, status.reason);
    EXPECT_EQ(0, status.levels & BIT(6));
    EXPECT_TRUE((status.monitored & BIT(6)) != 0u);
}

static void test_output_pin_lifecycle(void)
{
    setup();
    hp_status_t status;
    hp_pin_config_t config;
    hp_pin_config_default(PICO_AVAILABLE, &config);
    config.mode[7] = HP_MODE_OUTPUT;
    config.param[7] = 1;
    set_config(&config, 4, 10000);
    EXPECT_EQ('o', hw.kind[7]);
    EXPECT_TRUE((hw.driven & BIT(7)) != 0u);

    hp_engine_task(&engine, 11000);
    EXPECT_TRUE(next_report(11000, &status));
    EXPECT_EQ(BIT(7), status.outputs);
    EXPECT_EQ(0, status.monitored & BIT(7));
    EXPECT_TRUE((status.levels & BIT(7)) != 0u);

    EXPECT_TRUE(send_output(BIT(7) | BIT(8), 0, HP_OUTPUT_PAYLOAD_LEN));
    EXPECT_EQ(1, hw.write_calls);
    EXPECT_EQ(0, hw.driven & BIT(7));
    EXPECT_TRUE(next_report(12000, &status));
    EXPECT_EQ(HP_REASON_OUTPUT_APPLIED, status.reason);
    EXPECT_EQ(0, status.levels & BIT(7));
    EXPECT_TRUE((status.levels & BIT(8)) != 0u);

    EXPECT_TRUE(!send_output(BIT(7), BIT(7), 9));
    EXPECT_TRUE(!next_report(13000, &status));

    EXPECT_TRUE(send_output(BIT(8), BIT(8), HP_OUTPUT_PAYLOAD_LEN));
    EXPECT_EQ(1, hw.write_calls);
    EXPECT_TRUE(next_report(14000, &status));
    EXPECT_EQ(HP_REASON_OUTPUT_APPLIED, status.reason);

    hp_engine_usb_reset(&engine);
    EXPECT_TRUE((hw.driven & BIT(7)) != 0u);
    EXPECT_TRUE(next_report(15000, &status));
    EXPECT_EQ(HP_REASON_OUTPUT_RESET, status.reason);
    EXPECT_TRUE((status.levels & BIT(7)) != 0u);

    config.param[7] = 0;
    set_config(&config, 5, 16000);
    EXPECT_EQ(0, hw.driven & BIT(7));
}

static void test_usb_reset_without_outputs_sets_no_reason(void)
{
    setup();
    hp_engine_usb_reset(&engine);
    hp_status_t status;
    EXPECT_TRUE(!next_report(500000, &status));
    EXPECT_EQ(0, hw.write_calls);
}

static void test_rejected_config_keeps_previous(void)
{
    setup();
    int calls = hw.configure_calls;
    hp_pin_config_t config;
    hp_pin_config_default(PICO_AVAILABLE, &config);
    config.mode[5] = HP_MODE_OUTPUT;
    config.param[5] = 0;
    config.mode[23] = HP_MODE_INPUT_PULLUP;
    set_config(&config, 11, 10000);

    uint8_t buf[HP_REPORT_PAYLOAD_LEN];
    hp_engine_get_pin_config(&engine, buf);
    EXPECT_EQ(HP_RESULT_UNAVAILABLE_GPIO, buf[0]);
    EXPECT_EQ(23, buf[1]);
    EXPECT_EQ(11, buf[2]);
    EXPECT_EQ(HP_MODE_INPUT_PULLUP, buf[3 + 2 * 5]);
    EXPECT_EQ(HP_MODE_UNUSED, buf[3 + 2 * 23]);
    EXPECT_EQ(calls, hw.configure_calls);

    hp_status_t status;
    hp_engine_task(&engine, 20000);
    EXPECT_TRUE(!next_report(20000, &status));
}

static void test_identical_config_is_not_a_change(void)
{
    setup();
    hp_pin_config_t config;
    hp_pin_config_default(PICO_AVAILABLE, &config);
    set_config(&config, 5, 10000);

    hp_status_t status;
    hp_engine_task(&engine, 20000);
    EXPECT_TRUE(!next_report(20000, &status));
    uint8_t buf[HP_REPORT_PAYLOAD_LEN];
    hp_engine_get_pin_config(&engine, buf);
    EXPECT_EQ(HP_RESULT_OK, buf[0]);
    EXPECT_EQ(5, buf[2]);
}

static void test_unused_transition_stops_tracking(void)
{
    setup();
    hp_pin_config_t config;
    hp_pin_config_default(PICO_AVAILABLE, &config);
    config.mode[8] = HP_MODE_UNUSED;
    config.param[8] = 0;
    set_config(&config, 6, 10000);
    EXPECT_EQ('u', hw.kind[8]);

    hp_status_t status;
    hp_engine_task(&engine, 11000);
    EXPECT_TRUE(next_report(11000, &status));
    EXPECT_EQ(0, status.monitored & BIT(8));
    EXPECT_EQ(0, status.levels & BIT(8));

    hp_engine_on_edge(&engine, 8, false, 12000);
    hp_engine_task(&engine, 40000);
    EXPECT_EQ(0, engine.queue.count);
}

static void test_event_age_saturates(void)
{
    setup();
    use_zero_debounce_on_low_pins(10000);
    hp_engine_on_edge(&engine, 1, false, 2000000);

    hp_status_t status;
    EXPECT_TRUE(next_report(2000000u + 0x100000000ull, &status));
    EXPECT_EQ(1, status.event_count);
    EXPECT_EQ(0xFFFFFFFFu, status.events[0].age_us);
}

static void test_resync_injects_only_differing_pins(void)
{
    setup();
    hp_engine_on_edge(&engine, 3, false, 100000);
    // GPIO3 is already known to be low; GPIO4 changed without an edge notification.
    hp_engine_resync(&engine, 0x3FFFFFFFu & ~(BIT(3) | BIT(4)), 105000);
    EXPECT_EQ(100000, engine.debounce[3].last_change_us);
    EXPECT_EQ(105000, engine.debounce[4].last_change_us);

    hp_engine_task(&engine, 125000);
    EXPECT_EQ(2, engine.queue.count);
    EXPECT_EQ(3, hp_event_queue_peek(&engine.queue, 0)->gpio);
    EXPECT_EQ(4, hp_event_queue_peek(&engine.queue, 1)->gpio);
    EXPECT_EQ(105000, hp_event_queue_peek(&engine.queue, 1)->start_us);
}

static void test_activity_counts_events_and_outputs(void)
{
    setup();
    uint32_t start = hp_engine_activity(&engine);

    hp_engine_task(&engine, 1000000);  // a periodic report is not activity
    EXPECT_EQ(start, hp_engine_activity(&engine));

    hp_engine_on_edge(&engine, 5, false, 2000000);
    hp_engine_on_edge(&engine, 5, true, 2001000);  // bounce back: no event, no activity
    hp_engine_task(&engine, 2030000);
    EXPECT_EQ(start, hp_engine_activity(&engine));
    hp_engine_on_edge(&engine, 5, false, 2040000);
    hp_engine_task(&engine, 2060000);
    EXPECT_EQ(start + 1u, hp_engine_activity(&engine));

    hp_pin_config_t config;
    hp_pin_config_default(PICO_AVAILABLE, &config);
    config.mode[7] = HP_MODE_OUTPUT;
    config.param[7] = 0;
    set_config(&config, 3, 3000000);  // configuration changes are not activity
    hp_engine_task(&engine, 3001000);
    EXPECT_EQ(start + 1u, hp_engine_activity(&engine));

    EXPECT_TRUE(send_output(BIT(7), BIT(7), HP_OUTPUT_PAYLOAD_LEN));
    EXPECT_EQ(start + 2u, hp_engine_activity(&engine));
    EXPECT_TRUE(!send_output(BIT(7), 0, 9));  // ignored report: no activity
    EXPECT_EQ(start + 2u, hp_engine_activity(&engine));
}

static void test_device_info_passthrough(void)
{
    setup();
    uint8_t actual[HP_REPORT_PAYLOAD_LEN];
    uint8_t expected[HP_REPORT_PAYLOAD_LEN];
    hp_engine_get_device_info(&engine, actual);
    hp_device_info_encode(&engine.info, expected);
    EXPECT_BYTES("device_info", expected, actual, HP_REPORT_PAYLOAD_LEN);
    EXPECT_EQ(HP_BOARD_PICO, actual[4]);
}

int main(void)
{
    RUN_TEST(test_init_touches_only_available_pins);
    RUN_TEST(test_periodic_report_and_seq);
    RUN_TEST(test_uncommitted_report_is_rebuilt);
    RUN_TEST(test_press_with_bounce_yields_one_event);
    RUN_TEST(test_events_split_across_reports);
    RUN_TEST(test_overflow_reports_confirmed_levels);
    RUN_TEST(test_get_status_does_not_consume_events);
    RUN_TEST(test_param_only_change_keeps_level);
    RUN_TEST(test_pull_change_resettles_from_raw);
    RUN_TEST(test_output_pin_lifecycle);
    RUN_TEST(test_usb_reset_without_outputs_sets_no_reason);
    RUN_TEST(test_rejected_config_keeps_previous);
    RUN_TEST(test_identical_config_is_not_a_change);
    RUN_TEST(test_unused_transition_stops_tracking);
    RUN_TEST(test_event_age_saturates);
    RUN_TEST(test_resync_injects_only_differing_pins);
    RUN_TEST(test_activity_counts_events_and_outputs);
    RUN_TEST(test_device_info_passthrough);
    TEST_EXIT();
}
