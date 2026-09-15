#include "hidpin/event_queue.h"
#include "test_util.h"

static hp_edge_event_t make_event(uint8_t i)
{
    hp_edge_event_t event = {.gpio = (uint8_t)(i % HP_GPIO_COUNT), .level = (i & 1u) != 0u, .start_us = 1000u * i};
    return event;
}

static void test_fifo_order_and_overflow(void)
{
    hp_event_queue_t q;
    hp_event_queue_init(&q);

    for (uint8_t i = 0; i < HP_EVENT_QUEUE_SIZE; i++) {
        hp_edge_event_t event = make_event(i);
        EXPECT_TRUE(hp_event_queue_push(&q, &event));
    }
    EXPECT_TRUE(!q.overflowed);

    hp_edge_event_t extra = make_event(99);
    EXPECT_TRUE(!hp_event_queue_push(&q, &extra));
    EXPECT_TRUE(q.overflowed);
    EXPECT_EQ(HP_EVENT_QUEUE_SIZE, q.count);

    for (uint8_t i = 0; i < HP_EVENT_QUEUE_SIZE; i++) {
        EXPECT_EQ(1000u * i, hp_event_queue_peek(&q, i)->start_us);
    }
}

static void test_drop_and_wraparound(void)
{
    hp_event_queue_t q;
    hp_event_queue_init(&q);

    for (uint8_t i = 0; i < 30; i++) {
        hp_edge_event_t event = make_event(i);
        hp_event_queue_push(&q, &event);
    }
    hp_event_queue_drop(&q, 25);
    EXPECT_EQ(5, q.count);
    EXPECT_EQ(25000u, hp_event_queue_peek(&q, 0)->start_us);

    for (uint8_t i = 30; i < 57; i++) {
        hp_edge_event_t event = make_event(i);
        EXPECT_TRUE(hp_event_queue_push(&q, &event));
    }
    EXPECT_EQ(HP_EVENT_QUEUE_SIZE, q.count);
    EXPECT_EQ(56000u, hp_event_queue_peek(&q, 31)->start_us);

    hp_event_queue_drop(&q, 100);
    EXPECT_EQ(0, q.count);
}

int main(void)
{
    RUN_TEST(test_fifo_order_and_overflow);
    RUN_TEST(test_drop_and_wraparound);
    TEST_EXIT();
}
