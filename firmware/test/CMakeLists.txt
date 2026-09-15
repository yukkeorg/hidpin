# Host-side unit tests for firmware/core. Does not need the Pico SDK.
#   cmake -S firmware/test -B build/core-tests && cmake --build build/core-tests && ctest --test-dir build/core-tests
cmake_minimum_required(VERSION 3.20)
project(hidpin_core_tests LANGUAGES C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)

option(HIDPIN_SANITIZE "Build the tests with AddressSanitizer and UndefinedBehaviorSanitizer" ON)

add_compile_options(-Wall -Wextra -Wpedantic -Wshadow -Werror)
if(HIDPIN_SANITIZE)
    add_compile_options(-fsanitize=address,undefined -fno-sanitize-recover=all -fno-omit-frame-pointer)
    add_link_options(-fsanitize=address,undefined)
endif()

add_subdirectory(../core ${CMAKE_CURRENT_BINARY_DIR}/core)

find_package(Python3 REQUIRED COMPONENTS Interpreter)
set(VECTORS_JSON ${CMAKE_CURRENT_SOURCE_DIR}/../../protocol/vectors.json)
set(VECTORS_HEADER ${CMAKE_CURRENT_BINARY_DIR}/generated/vectors.h)
add_custom_command(
    OUTPUT ${VECTORS_HEADER}
    COMMAND Python3::Interpreter ${CMAKE_CURRENT_SOURCE_DIR}/gen_vectors.py ${VECTORS_JSON} ${VECTORS_HEADER}
    DEPENDS ${VECTORS_JSON} ${CMAKE_CURRENT_SOURCE_DIR}/gen_vectors.py
    COMMENT "Generating C test vectors from protocol/vectors.json"
)
add_custom_target(hidpin_vectors DEPENDS ${VECTORS_HEADER})

enable_testing()
foreach(name protocol debounce event_queue engine)
    add_executable(test_${name} test_${name}.c)
    target_link_libraries(test_${name} PRIVATE hidpin_core)
    target_include_directories(test_${name} PRIVATE ${CMAKE_CURRENT_BINARY_DIR}/generated)
    add_dependencies(test_${name} hidpin_vectors)
    add_test(NAME ${name} COMMAND test_${name})
endforeach()
