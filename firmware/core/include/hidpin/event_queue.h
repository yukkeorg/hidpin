// Fixed-size FIFO of confirmed edge events waiting to be reported (PROTOCOL.md 4.5).
#ifndef HIDPIN_EVENT_QUEUE_H
#define HIDPIN_EVENT_QUEUE_H

#include <stdbool.h>
#include <stdint.h>

#include "hidpin/protocol.h"

typedef struct {
    uint8_t gpio;
    bool level;
    uint64_t start_us;
} hp_edge_event_t;

typedef struct {
    hp_edge_event_t items[HP_EVENT_QUEUE_SIZE];
    uint8_t head;
    uint8_t count;
    bool overflowed;  // an event was dropped since the flag was last cleared
} hp_event_queue_t;

void hp_event_queue_init(hp_event_queue_t *q);

// Drops the event and sets overflowed when the queue is full.
bool hp_event_queue_push(hp_event_queue_t *q, const hp_edge_event_t *event);

// index 0 is the oldest event; index must be less than the count.
const hp_edge_event_t *hp_event_queue_peek(const hp_event_queue_t *q, uint8_t index);

// Removes up to n of the oldest events.
void hp_event_queue_drop(hp_event_queue_t *q, uint8_t n);

#endif
