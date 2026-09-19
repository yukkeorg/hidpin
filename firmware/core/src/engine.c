#include "hidpin/engine.h"

#include <string.h>

static uint32_t gpio_bit(uint8_t gpio)
{
    return (uint32_t)1u << gpio;
}

static uint32_t with_bit(uint32_t mask, uint8_t gpio, bool set)
{
    if (set) {
        return mask | gpio_bit(gpio);
    }
    return mask & ~gpio_bit(gpio);
}

static uint32_t input_mask(const hp_pin_config_t *config)
{
    uint32_t mask = 0u;
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        if (hp_mode_is_input(config->mode[n])) {
            mask |= gpio_bit(n);
        }
    }
    return mask;
}

static uint32_t output_mask(const hp_pin_config_t *config)
{
    uint32_t mask = 0u;
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        if (config->mode[n] == HP_MODE_OUTPUT) {
            mask |= gpio_bit(n);
        }
    }
    return mask;
}

static uint32_t initial_output_levels(const hp_pin_config_t *config)
{
    uint32_t levels = 0u;
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        if (config->mode[n] == HP_MODE_OUTPUT && config->param[n] != 0u) {
            levels |= gpio_bit(n);
        }
    }
    return levels;
}

static uint32_t confirmed_levels(const hp_engine_t *e)
{
    uint32_t levels = 0u;
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        if ((e->monitored & gpio_bit(n)) != 0u && e->debounce[n].confirmed) {
            levels |= gpio_bit(n);
        }
    }
    return levels;
}

static bool is_tracking(const hp_engine_t *e, uint8_t gpio)
{
    uint32_t bit = gpio_bit(gpio);
    return (e->monitored & bit) != 0u && (e->settling & bit) == 0u && hp_mode_is_input(e->config.mode[gpio]);
}

static void push_event(hp_engine_t *e, uint8_t gpio, bool level, uint64_t start_us)
{
    e->activity++;
    hp_edge_event_t event = {.gpio = gpio, .level = level, .start_us = start_us};
    (void)hp_event_queue_push(&e->queue, &event);
}

static uint32_t event_age(uint64_t now_us, uint64_t start_us)
{
    if (start_us >= now_us) {
        return 0u;
    }
    uint64_t age = now_us - start_us;
    if (age > UINT32_MAX) {
        return UINT32_MAX;
    }
    return (uint32_t)age;
}

static void start_settle(hp_engine_t *e, uint64_t now_us)
{
    e->settle_active = true;
    e->settle_deadline_us = now_us + HP_SETTLE_US;
}

static void finish_settle(hp_engine_t *e)
{
    uint32_t raw = e->hw.read_inputs(e->hw.ctx);
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        if ((e->settling & gpio_bit(n)) != 0u) {
            bool level = ((raw >> n) & 1u) != 0u;
            hp_debounce_reset(&e->debounce[n], level, e->config.param[n]);
            e->reported_levels = with_bit(e->reported_levels, n, level);
        }
    }

    e->monitored = input_mask(&e->config);
    e->outputs = output_mask(&e->config);
    e->reported_levels &= e->monitored;
    e->settling = 0u;
    e->settle_active = false;
    if (e->config_changed) {
        e->pending_reasons |= HP_REASON_CONFIG_CHANGED;
        e->config_changed = false;
    }
}

void hp_engine_init(hp_engine_t *e, const hp_hw_t *hw, const hp_device_info_t *info, uint64_t now_us)
{
    memset(e, 0, sizeof(*e));
    e->hw = *hw;
    e->info = *info;
    e->result = HP_RESULT_OK;
    e->result_gpio = HP_RESULT_GPIO_NONE;
    hp_event_queue_init(&e->queue);
    hp_pin_config_default(info->available, &e->config);

    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        if (hp_mode_is_input(e->config.mode[n])) {
            e->hw.set_input(e->hw.ctx, n, e->config.mode[n]);
            e->settling |= gpio_bit(n);
        }
    }
    start_settle(e, now_us);
    e->last_sent_us = now_us;
}

void hp_engine_on_edge(hp_engine_t *e, uint8_t gpio, bool raw_level, uint64_t now_us)
{
    if (gpio >= HP_GPIO_COUNT || !is_tracking(e, gpio)) {
        return;
    }
    uint64_t start_us;
    if (hp_debounce_edge(&e->debounce[gpio], raw_level, now_us, &start_us)) {
        push_event(e, gpio, raw_level, start_us);
    }
}

void hp_engine_resync(hp_engine_t *e, uint32_t raw, uint64_t now_us)
{
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        bool level = ((raw >> n) & 1u) != 0u;
        if (is_tracking(e, n) && level != e->debounce[n].raw) {
            hp_engine_on_edge(e, n, level, now_us);
        }
    }
}

void hp_engine_task(hp_engine_t *e, uint64_t now_us)
{
    if (e->settle_active && now_us >= e->settle_deadline_us) {
        finish_settle(e);
    }

    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        uint64_t start_us;
        if (is_tracking(e, n) && hp_debounce_poll(&e->debounce[n], now_us, &start_us)) {
            push_event(e, n, e->debounce[n].confirmed, start_us);
        }
    }

    if (now_us - e->last_sent_us >= (uint64_t)HP_PERIODIC_INTERVAL_MS * 1000u) {
        e->pending_reasons |= HP_REASON_PERIODIC;
    }
}

bool hp_engine_prepare_report(hp_engine_t *e, uint64_t now_us, uint8_t *out)
{
    uint8_t queued = e->queue.count;
    bool overflow = e->queue.overflowed;
    e->staged = false;
    if (e->pending_reasons == 0u && queued == 0u && !overflow) {
        return false;
    }

    hp_status_t status;
    memset(&status, 0, sizeof(status));
    uint8_t n = queued < HP_EVENTS_PER_REPORT ? queued : (uint8_t)HP_EVENTS_PER_REPORT;
    uint32_t reflected = e->reported_levels;
    for (uint8_t i = 0u; i < n; i++) {
        const hp_edge_event_t *event = hp_event_queue_peek(&e->queue, i);
        status.events[i].gpio = event->gpio;
        status.events[i].level = event->level;
        status.events[i].age_us = event_age(now_us, event->start_us);
        reflected = with_bit(reflected, event->gpio, event->level);
    }

    bool drained = queued == n;
    if (drained) {
        // Also covers the effect of any dropped events.
        reflected = confirmed_levels(e);
    }

    status.seq = e->next_seq;
    status.reason = (uint8_t)(e->pending_reasons | (n > 0u ? HP_REASON_LEVEL_CHANGED : 0u));
    if (!drained) {
        status.flags = HP_FLAG_MORE_EVENTS;
    } else if (overflow) {
        status.flags = HP_FLAG_OVERFLOW;
    }
    status.event_count = n;
    status.monitored = e->monitored;
    status.outputs = e->outputs;
    status.levels = (reflected & e->monitored) | (e->output_levels & e->outputs);
    status.timestamp_us = now_us;
    hp_status_encode(&status, out);

    e->staged = true;
    e->staged_events = n;
    e->staged_reasons = status.reason;
    e->staged_overflow = drained && overflow;
    e->staged_reported_levels = reflected & e->monitored;
    return true;
}

void hp_engine_commit_report(hp_engine_t *e, uint64_t now_us)
{
    if (!e->staged) {
        return;
    }
    hp_event_queue_drop(&e->queue, e->staged_events);
    e->reported_levels = e->staged_reported_levels;
    e->pending_reasons &= (uint8_t)~e->staged_reasons;
    if (e->staged_overflow) {
        e->queue.overflowed = false;
    }
    e->last_seq = e->next_seq;
    e->next_seq++;
    e->has_sent = true;
    e->last_sent_us = now_us;
    e->staged = false;
}

void hp_engine_get_status(const hp_engine_t *e, uint64_t now_us, uint8_t *out)
{
    hp_status_t status;
    memset(&status, 0, sizeof(status));
    status.seq = e->has_sent ? e->last_seq : 0u;
    status.reason = HP_REASON_HOST_REQUEST;
    status.flags = e->queue.count > 0u ? HP_FLAG_MORE_EVENTS : 0u;
    status.monitored = e->monitored;
    status.outputs = e->outputs;
    status.levels = confirmed_levels(e) | (e->output_levels & e->outputs);
    status.timestamp_us = now_us;
    hp_status_encode(&status, out);
}

void hp_engine_get_device_info(const hp_engine_t *e, uint8_t *out)
{
    hp_device_info_encode(&e->info, out);
}

void hp_engine_get_pin_config(const hp_engine_t *e, uint8_t *out)
{
    hp_pin_config_report_t report = {
        .result = e->result,
        .result_gpio = e->result_gpio,
        .request_id = e->request_id,
        .config = e->config,
    };
    hp_pin_config_encode(&report, out);
}

void hp_engine_set_pin_config(hp_engine_t *e, const uint8_t *payload, uint16_t len, uint64_t now_us)
{
    hp_pin_config_t next;
    e->result = hp_pin_config_decode_set(payload, len, e->info.available, &next, &e->request_id, &e->result_gpio);
    if (e->result != HP_RESULT_OK) {
        return;
    }

    bool changed = false;
    for (uint8_t n = 0u; n < HP_GPIO_COUNT; n++) {
        uint8_t old_mode = e->config.mode[n];
        uint8_t new_mode = next.mode[n];
        uint8_t new_param = next.param[n];
        if (old_mode == new_mode && e->config.param[n] == new_param) {
            continue;
        }
        changed = true;

        if (hp_mode_is_input(new_mode)) {
            if (old_mode == new_mode) {
                hp_debounce_set_time(&e->debounce[n], new_param, now_us);
            } else {
                e->hw.set_input(e->hw.ctx, n, new_mode);
                e->settling |= gpio_bit(n);
            }
            e->output_levels = with_bit(e->output_levels, n, false);
        } else if (new_mode == HP_MODE_OUTPUT) {
            e->settling &= ~gpio_bit(n);
            e->hw.set_output(e->hw.ctx, n, new_param != 0u);
            e->output_levels = with_bit(e->output_levels, n, new_param != 0u);
        } else {
            e->settling &= ~gpio_bit(n);
            e->hw.set_unused(e->hw.ctx, n);
            e->output_levels = with_bit(e->output_levels, n, false);
        }
    }

    if (changed) {
        e->config = next;
        e->config_changed = true;
        start_settle(e, now_us);
    }
}

bool hp_engine_output(hp_engine_t *e, const uint8_t *payload, uint16_t len)
{
    uint32_t mask;
    uint32_t value;
    if (!hp_output_decode(payload, len, &mask, &value)) {
        return false;
    }

    e->activity++;
    uint32_t target = mask & output_mask(&e->config);
    e->output_levels = (e->output_levels & ~target) | (value & target);
    if (target != 0u) {
        e->hw.write_outputs(e->hw.ctx, target, value & target);
    }
    e->pending_reasons |= HP_REASON_OUTPUT_APPLIED;
    return true;
}

void hp_engine_usb_reset(hp_engine_t *e)
{
    uint32_t outputs = output_mask(&e->config);
    if (outputs == 0u) {
        return;
    }
    uint32_t levels = initial_output_levels(&e->config);
    e->output_levels = (e->output_levels & ~outputs) | levels;
    e->hw.write_outputs(e->hw.ctx, outputs, levels);
    e->pending_reasons |= HP_REASON_OUTPUT_RESET;
}

uint32_t hp_engine_activity(const hp_engine_t *e)
{
    return e->activity;
}
