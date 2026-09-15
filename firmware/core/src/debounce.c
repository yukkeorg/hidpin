#include "hidpin/debounce.h"

void hp_debounce_reset(hp_debounce_t *d, bool level, uint8_t debounce_ms)
{
    d->confirmed = level;
    d->raw = level;
    d->pending = false;
    d->last_change_us = 0u;
    d->debounce_us = (uint32_t)debounce_ms * 1000u;
}

void hp_debounce_set_time(hp_debounce_t *d, uint8_t debounce_ms, uint64_t now_us)
{
    d->debounce_us = (uint32_t)debounce_ms * 1000u;
    if (d->pending) {
        d->last_change_us = now_us;
    }
}

bool hp_debounce_edge(hp_debounce_t *d, bool raw, uint64_t now_us, uint64_t *start_us)
{
    d->raw = raw;
    d->last_change_us = now_us;
    if (raw == d->confirmed) {
        d->pending = false;
        return false;
    }
    if (d->debounce_us == 0u) {
        d->confirmed = raw;
        d->pending = false;
        *start_us = now_us;
        return true;
    }
    d->pending = true;
    return false;
}

bool hp_debounce_poll(hp_debounce_t *d, uint64_t now_us, uint64_t *start_us)
{
    if (!d->pending || now_us < d->last_change_us) {
        return false;
    }
    if (now_us - d->last_change_us < d->debounce_us) {
        return false;
    }
    d->confirmed = d->raw;
    d->pending = false;
    *start_us = d->last_change_us;
    return true;
}
