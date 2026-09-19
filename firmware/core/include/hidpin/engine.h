// Device behaviour of hidpin, independent of the Pico SDK and TinyUSB.
// The engine is not reentrant: call every function from the same execution context.
#ifndef HIDPIN_ENGINE_H
#define HIDPIN_ENGINE_H

#include <stdbool.h>
#include <stdint.h>

#include "hidpin/debounce.h"
#include "hidpin/event_queue.h"
#include "hidpin/protocol.h"

// GPIO access supplied by the firmware (or a fake in tests). All callbacks are required.
typedef struct {
    void *ctx;
    void (*set_unused)(void *ctx, uint8_t gpio);
    void (*set_input)(void *ctx, uint8_t gpio, uint8_t mode);  // mode is an HP_MODE_INPUT_* value
    void (*set_output)(void *ctx, uint8_t gpio, bool level);
    void (*write_outputs)(void *ctx, uint32_t mask, uint32_t value);
    uint32_t (*read_inputs)(void *ctx);  // electrical value of every GPIO, bit n = GPIOn
} hp_hw_t;

typedef struct {
    hp_hw_t hw;
    hp_device_info_t info;

    hp_pin_config_t config;  // latest accepted pin configuration
    uint8_t result;
    uint8_t result_gpio;
    uint8_t request_id;

    hp_debounce_t debounce[HP_GPIO_COUNT];
    hp_event_queue_t queue;

    // Published snapshot, updated when a configuration change has settled.
    uint32_t monitored;
    uint32_t outputs;
    uint32_t reported_levels;  // monitored levels up to the last event handed to the host
    uint32_t output_levels;

    uint32_t settling;  // GPIOs waiting for their pull to settle before the first read
    bool settle_active;
    bool config_changed;
    uint64_t settle_deadline_us;

    uint32_t activity;  // edge events plus applied output reports; wraps around

    uint8_t pending_reasons;
    uint16_t next_seq;
    uint16_t last_seq;
    bool has_sent;
    uint64_t last_sent_us;

    bool staged;
    uint8_t staged_events;
    uint8_t staged_reasons;
    bool staged_overflow;
    uint32_t staged_reported_levels;
} hp_engine_t;

// Applies the default pin configuration. GPIOs outside info->available are never touched.
void hp_engine_init(hp_engine_t *e, const hp_hw_t *hw, const hp_device_info_t *info, uint64_t now_us);

// Reports an electrical change on a GPIO, observed at now_us.
void hp_engine_on_edge(hp_engine_t *e, uint8_t gpio, bool raw_level, uint64_t now_us);

// Re-synchronises with the electrical values of all GPIOs (bit n = GPIOn) after edge
// notifications may have been lost; treats every differing monitored pin as changed at now_us.
void hp_engine_resync(hp_engine_t *e, uint32_t raw, uint64_t now_us);

// Advances debouncing, settling and the periodic timer. Call at least once per millisecond.
void hp_engine_task(hp_engine_t *e, uint64_t now_us);

// Builds the next Interrupt IN status report into out (HP_REPORT_PAYLOAD_LEN bytes).
// Returns false when nothing needs to be sent. The report takes effect only after
// hp_engine_commit_report(); an uncommitted report is rebuilt by the next call.
bool hp_engine_prepare_report(hp_engine_t *e, uint64_t now_us, uint8_t *out);
void hp_engine_commit_report(hp_engine_t *e, uint64_t now_us);

// Get_Report responses.
void hp_engine_get_status(const hp_engine_t *e, uint64_t now_us, uint8_t *out);
void hp_engine_get_device_info(const hp_engine_t *e, uint8_t *out);
void hp_engine_get_pin_config(const hp_engine_t *e, uint8_t *out);

// Set_Report(Feature) for the pin configuration; payload excludes the report ID.
void hp_engine_set_pin_config(hp_engine_t *e, const uint8_t *payload, uint16_t len, uint64_t now_us);

// Output report; payload excludes the report ID. Returns false when the report was ignored.
bool hp_engine_output(hp_engine_t *e, const uint8_t *payload, uint16_t len);

// USB disconnect, suspend or bus reset: return output pins to their initial output levels.
void hp_engine_usb_reset(hp_engine_t *e);

// Counts confirmed edge events and applied output reports (the firmware flashes its LED on
// every change). Only differences between two readings are meaningful; the value wraps.
uint32_t hp_engine_activity(const hp_engine_t *e);

#endif
