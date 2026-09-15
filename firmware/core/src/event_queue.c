#include "hidpin/event_queue.h"

void hp_event_queue_init(hp_event_queue_t *q)
{
    q->head = 0u;
    q->count = 0u;
    q->overflowed = false;
}

bool hp_event_queue_push(hp_event_queue_t *q, const hp_edge_event_t *event)
{
    if (q->count >= HP_EVENT_QUEUE_SIZE) {
        q->overflowed = true;
        return false;
    }
    q->items[(q->head + q->count) % HP_EVENT_QUEUE_SIZE] = *event;
    q->count++;
    return true;
}

const hp_edge_event_t *hp_event_queue_peek(const hp_event_queue_t *q, uint8_t index)
{
    return &q->items[(q->head + index) % HP_EVENT_QUEUE_SIZE];
}

void hp_event_queue_drop(hp_event_queue_t *q, uint8_t n)
{
    if (n > q->count) {
        n = q->count;
    }
    q->head = (uint8_t)((q->head + n) % HP_EVENT_QUEUE_SIZE);
    q->count = (uint8_t)(q->count - n);
}
