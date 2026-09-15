"""Convert protocol/vectors.json into a C header for the core unit tests."""

import json
import sys
from pathlib import Path


def num(value):
    return int(value, 0) if isinstance(value, str) else int(value)


def byte_array(hex_text, size):
    data = bytes.fromhex(hex_text)
    if len(data) > size:
        raise ValueError(f"payload of {len(data)} bytes exceeds {size}")
    padded = data + bytes(size - len(data))
    return "{" + ", ".join(f"0x{b:02X}" for b in padded) + "}", len(data)


def int_array(values):
    return "{" + ", ".join(str(num(v)) for v in values) + "}"


def c_string(text):
    return '"' + text.replace("\\", "\\\\").replace('"', '\\"') + '"'


def main(src, dst):
    vectors = json.loads(Path(src).read_text())
    lines = [
        "/* Generated from protocol/vectors.json by firmware/test/gen_vectors.py. Do not edit. */",
        "#ifndef HIDPIN_TEST_VECTORS_H",
        "#define HIDPIN_TEST_VECTORS_H",
        "",
        "#include <stddef.h>",
        "#include <stdint.h>",
        "",
        "typedef struct { uint8_t gpio; uint8_t level; uint32_t age_us; } vec_event_t;",
        "typedef struct {",
        "    const char *name; uint16_t seq; uint8_t reason; uint8_t flags;",
        "    uint32_t monitored; uint32_t outputs; uint32_t levels; uint64_t timestamp_us;",
        "    uint8_t event_count; vec_event_t events[7]; uint8_t payload[63];",
        "} vec_status_t;",
        "typedef struct {",
        "    const char *name; uint8_t fw_major; uint8_t fw_minor; uint8_t fw_patch; uint8_t board;",
        "    uint32_t available; uint8_t payload[63];",
        "} vec_device_info_t;",
        "typedef struct {",
        "    const char *name; uint8_t result; uint8_t result_gpio; uint8_t request_id;",
        "    uint8_t mode[30]; uint8_t param[30]; uint8_t payload[63];",
        "} vec_pin_config_t;",
        "typedef struct {",
        "    const char *name; uint32_t available; uint16_t length; uint8_t payload[64];",
        "    uint8_t result; uint8_t result_gpio; uint8_t request_id;",
        "} vec_pin_config_set_t;",
        "typedef struct {",
        "    const char *name; uint16_t length; uint8_t payload[16]; uint8_t valid; uint32_t mask; uint32_t value;",
        "} vec_output_t;",
        "",
    ]

    descriptor = bytes.fromhex(vectors["report_descriptor"])
    lines.append(
        "static const uint8_t VEC_REPORT_DESCRIPTOR[] = {" + ", ".join(f"0x{b:02X}" for b in descriptor) + "};"
    )
    lines.append(f"#define VEC_REPORT_DESCRIPTOR_LEN {len(descriptor)}u")

    lines.append("static const vec_status_t VEC_STATUS[] = {")
    for v in vectors["status"]:
        events = [
            "{" + f"{num(e['gpio'])}, {num(e['level'])}, {num(e['age_us'])}u" + "}" for e in v["events"]
        ]
        events += ["{0, 0, 0u}"] * (7 - len(events))
        payload, _ = byte_array(v["payload"], 63)
        lines.append(
            f"    {{{c_string(v['name'])}, {num(v['seq'])}, {num(v['reason'])}, {num(v['flags'])}, "
            f"{num(v['monitored'])}u, {num(v['outputs'])}u, {num(v['levels'])}u, {num(v['timestamp_us'])}ull, "
            f"{len(v['events'])}, {{{', '.join(events)}}}, {payload}}},"
        )
    lines.append("};")

    lines.append("static const vec_device_info_t VEC_DEVICE_INFO[] = {")
    for v in vectors["device_info"]:
        payload, _ = byte_array(v["payload"], 63)
        lines.append(
            f"    {{{c_string(v['name'])}, {num(v['fw_major'])}, {num(v['fw_minor'])}, {num(v['fw_patch'])}, "
            f"{num(v['board'])}, {num(v['available'])}u, {payload}}},"
        )
    lines.append("};")

    lines.append("static const vec_pin_config_t VEC_PIN_CONFIG[] = {")
    for v in vectors["pin_config"]:
        payload, _ = byte_array(v["payload"], 63)
        lines.append(
            f"    {{{c_string(v['name'])}, {num(v['result'])}, {num(v['result_gpio'])}, {num(v['request_id'])}, "
            f"{int_array(v['mode'])}, {int_array(v['param'])}, {payload}}},"
        )
    lines.append("};")

    lines.append("static const vec_pin_config_set_t VEC_PIN_CONFIG_SET[] = {")
    for v in vectors["pin_config_set"]:
        payload, length = byte_array(v["payload"], 64)
        lines.append(
            f"    {{{c_string(v['name'])}, {num(v['available'])}u, {length}, {payload}, "
            f"{num(v['result'])}, {num(v['result_gpio'])}, {num(v['request_id'])}}},"
        )
    lines.append("};")

    lines.append("static const vec_output_t VEC_OUTPUT[] = {")
    for v in vectors["output"]:
        payload, length = byte_array(v["payload"], 16)
        lines.append(
            f"    {{{c_string(v['name'])}, {length}, {payload}, {1 if v['valid'] else 0}, "
            f"{num(v['mask'])}u, {num(v['value'])}u}},"
        )
    lines.append("};")

    for table in ["VEC_STATUS", "VEC_DEVICE_INFO", "VEC_PIN_CONFIG", "VEC_PIN_CONFIG_SET", "VEC_OUTPUT"]:
        lines.append(f"#define {table}_COUNT (sizeof({table}) / sizeof({table}[0]))")
    lines += ["", "#endif", ""]

    Path(dst).parent.mkdir(parents=True, exist_ok=True)
    Path(dst).write_text("\n".join(lines))


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
