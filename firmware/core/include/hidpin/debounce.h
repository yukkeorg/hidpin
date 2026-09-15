// Chattering removal for one monitored pin (PROTOCOL.md section 8).
#ifndef HIDPIN_DEBOUNCE_H
#define HIDPIN_DEBOUNCE_H

#include <stdbool.h>
#include <stdint.h>

typedef struct {
    bool confirmed;           // pin level reported to the host
    bool raw;                 // last observed electrical value
    bool pending;             // raw differs from confirmed and is waiting to become stable
    uint64_t last_change_us;  // time of the last electrical change
    uint32_t debounce_us;
} hp_debounce_t;

// Starts from a known level with no pending change.
void hp_debounce_reset(hp_debounce_t *d, bool level, uint8_t debounce_ms);

// Changes the debounce time, keeping the confirmed level and restarting any pending wait at now_us.
void hp_debounce_set_time(hp_debounce_t *d, uint8_t debounce_ms, uint64_t now_us);

// Records an electrical change. Returns true when the level is confirmed immediately
// (debounce time 0); *start_us then receives the time the change began.
bool hp_debounce_edge(hp_debounce_t *d, bool raw, uint64_t now_us, uint64_t *start_us);

// Confirms a pending change once it has been stable for the debounce time.
bool hp_debounce_poll(hp_debounce_t *d, uint64_t now_us, uint64_t *start_us);

#endif
