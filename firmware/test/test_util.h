// Minimal assertion helpers for the core unit tests.
#ifndef HIDPIN_TEST_UTIL_H
#define HIDPIN_TEST_UTIL_H

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int test_failures = 0;

#define EXPECT_TRUE(cond) \
    do { \
        if (!(cond)) { \
            fprintf(stderr, "%s:%d: expected %s\n", __FILE__, __LINE__, #cond); \
            test_failures++; \
        } \
    } while (0)

#define EXPECT_EQ(expected, actual) \
    do { \
        uint64_t expected_ = (uint64_t)(expected); \
        uint64_t actual_ = (uint64_t)(actual); \
        if (expected_ != actual_) { \
            fprintf(stderr, "%s:%d: %s: expected 0x%" PRIX64 ", got 0x%" PRIX64 "\n", \
                    __FILE__, __LINE__, #actual, expected_, actual_); \
            test_failures++; \
        } \
    } while (0)

#define EXPECT_BYTES(label, expected, actual, len) \
    do { \
        const uint8_t *expected_ = (const uint8_t *)(expected); \
        const uint8_t *actual_ = (const uint8_t *)(actual); \
        for (size_t i_ = 0; i_ < (size_t)(len); i_++) { \
            if (expected_[i_] != actual_[i_]) { \
                fprintf(stderr, "%s:%d: %s: byte %zu expected 0x%02X, got 0x%02X\n", \
                        __FILE__, __LINE__, (label), i_, expected_[i_], actual_[i_]); \
                test_failures++; \
                break; \
            } \
        } \
    } while (0)

#define RUN_TEST(fn) \
    do { \
        int before_ = test_failures; \
        fn(); \
        printf("%s %s\n", test_failures == before_ ? "PASS" : "FAIL", #fn); \
    } while (0)

#define TEST_EXIT() return test_failures == 0 ? EXIT_SUCCESS : EXIT_FAILURE

#endif
