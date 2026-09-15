# Hardware-independent core of the hidpin firmware.
# Built by the firmware (Pico SDK) and by the host-side unit tests in ../test.
add_library(hidpin_core STATIC
    src/debounce.c
    src/engine.c
    src/event_queue.c
    src/protocol.c
)
target_include_directories(hidpin_core PUBLIC include)
