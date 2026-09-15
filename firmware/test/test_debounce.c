#include "hidpin/debounce.h"
#include "test_util.h"

static void test_confirms_after_stable_period(void)
{
    hp_debounce_t d;
    hp_debounce_reset(&d, true, 20);
    uint64_t start = 0;

    EXPECT_TRUE(!hp_debounce_edge(&d, false, 1000, &start));
    EXPECT_TRUE(!hp_debounce_poll(&d, 20999, &start));
    EXPECT_TRUE(d.confirmed);
    EXPECT_TRUE(hp_debounce_poll(&d, 21000, &start));
    EXPECT_EQ(1000, start);
    EXPECT_TRUE(!d.confirmed);
    EXPECT_TRUE(!hp_debounce_poll(&d, 50000, &start));
}

static void test_bounce_restarts_wait_from_last_change(void)
{
    hp_debounce_t d;
    hp_debounce_reset(&d, true, 20);
    uint64_t start = 0;

    hp_debounce_edge(&d, false, 0, &start);
    hp_debounce_edge(&d, true, 1000, &start);
    hp_debounce_edge(&d, false, 3000, &start);
    EXPECT_TRUE(!hp_debounce_poll(&d, 22999, &start));
    EXPECT_TRUE(hp_debounce_poll(&d, 23000, &start));
    EXPECT_EQ(3000, start);
}

static void test_return_to_confirmed_cancels(void)
{
    hp_debounce_t d;
    hp_debounce_reset(&d, true, 20);
    uint64_t start = 0;

    hp_debounce_edge(&d, false, 0, &start);
    hp_debounce_edge(&d, true, 5000, &start);
    EXPECT_TRUE(!hp_debounce_poll(&d, 100000, &start));
    EXPECT_TRUE(d.confirmed);
}

static void test_zero_debounce_confirms_on_edge(void)
{
    hp_debounce_t d;
    hp_debounce_reset(&d, true, 0);
    uint64_t start = 0;

    EXPECT_TRUE(hp_debounce_edge(&d, false, 4242, &start));
    EXPECT_EQ(4242, start);
    EXPECT_TRUE(!d.confirmed);
    EXPECT_TRUE(!hp_debounce_edge(&d, false, 5000, &start));
    EXPECT_TRUE(!hp_debounce_poll(&d, 6000, &start));
}

static void test_set_time_restarts_pending_wait(void)
{
    hp_debounce_t d;
    hp_debounce_reset(&d, true, 20);
    uint64_t start = 0;

    hp_debounce_edge(&d, false, 0, &start);
    hp_debounce_set_time(&d, 10, 15000);
    EXPECT_TRUE(d.confirmed);
    EXPECT_TRUE(!hp_debounce_poll(&d, 24999, &start));
    EXPECT_TRUE(hp_debounce_poll(&d, 25000, &start));
    EXPECT_EQ(15000, start);
}

static void test_set_time_without_pending_keeps_level(void)
{
    hp_debounce_t d;
    hp_debounce_reset(&d, false, 20);
    uint64_t start = 0;

    hp_debounce_set_time(&d, 5, 1000);
    EXPECT_TRUE(!d.confirmed);
    EXPECT_TRUE(!hp_debounce_poll(&d, 100000, &start));
    EXPECT_EQ(5000, d.debounce_us);
}

int main(void)
{
    RUN_TEST(test_confirms_after_stable_period);
    RUN_TEST(test_bounce_restarts_wait_from_last_change);
    RUN_TEST(test_return_to_confirmed_cancels);
    RUN_TEST(test_zero_debounce_confirms_on_edge);
    RUN_TEST(test_set_time_restarts_pending_wait);
    RUN_TEST(test_set_time_without_pending_keeps_level);
    TEST_EXIT();
}
