"""Command line interface: hidpin list / info / watch / config / output."""

from __future__ import annotations

import argparse
import json
import sys

from hidpin import protocol
from hidpin.device import Device, HidpinError, find_devices
from hidpin.protocol import PinMode, PinSetting, Reason, StatusFlags, StatusReport

INPUT_MODES = {
    "nopull": PinMode.INPUT,
    "in": PinMode.INPUT,
    "pullup": PinMode.PULLUP,
    "pulldown": PinMode.PULLDOWN,
}
LEVEL_WORDS = {"high": True, "1": True, "on": True, "low": False, "0": False, "off": False}


def parse_pin_spec(text: str) -> tuple[int, PinSetting]:
    """Parses "5=pullup:20", "6=off" or "7=out:low"."""
    gpio_text, separator, setting_text = text.partition("=")
    if not separator:
        raise argparse.ArgumentTypeError(f"'{text}' is not of the form GPIO=SETTING")
    try:
        gpio = int(gpio_text, 0)
    except ValueError:
        raise argparse.ArgumentTypeError(f"the GPIO number is not a number: '{gpio_text}'") from None
    if not 0 <= gpio < protocol.GPIO_COUNT:
        raise argparse.ArgumentTypeError(f"GPIO{gpio} is out of range (0-{protocol.GPIO_COUNT - 1})")

    kind, _, param_text = setting_text.partition(":")
    kind = kind.lower()
    if kind in ("off", "unused"):
        if param_text:
            raise argparse.ArgumentTypeError(f"'{text}': off takes no value")
        return gpio, PinSetting.unused()
    if kind in INPUT_MODES:
        debounce = protocol.DEFAULT_DEBOUNCE_MS
        if param_text:
            try:
                debounce = int(param_text, 0)
            except ValueError:
                raise argparse.ArgumentTypeError(f"'{text}': the debounce time is not a number") from None
        if not 0 <= debounce <= 255:
            raise argparse.ArgumentTypeError(f"'{text}': the debounce time must be 0-255 ms")
        return gpio, PinSetting.monitor(INPUT_MODES[kind], debounce)
    if kind == "out":
        level = LEVEL_WORDS.get(param_text.lower())
        if level is None:
            raise argparse.ArgumentTypeError(f"'{text}': an output is out:high or out:low")
        return gpio, PinSetting.output(level)
    raise argparse.ArgumentTypeError(
        f"'{text}': the setting must be one of off, nopull, pullup, pulldown, out"
    )


def parse_output_spec(text: str) -> tuple[int, bool]:
    """Parses "7=high" or "7=0"."""
    gpio_text, separator, level_text = text.partition("=")
    if not separator:
        raise argparse.ArgumentTypeError(f"'{text}' is not of the form GPIO=VALUE")
    try:
        gpio = int(gpio_text, 0)
    except ValueError:
        raise argparse.ArgumentTypeError(f"the GPIO number is not a number: '{gpio_text}'") from None
    level = LEVEL_WORDS.get(level_text.lower())
    if level is None:
        raise argparse.ArgumentTypeError(f"'{text}': the value must be high/low or 1/0")
    return gpio, level


def parse_gpio_list(text: str) -> list[int]:
    return [int(part, 0) for part in text.split(",") if part]


def flag_names(value) -> list[str]:
    return [flag.name for flag in type(value) if value & flag]


def open_device(args) -> Device:
    return Device.open(args.serial)


def cmd_list(args) -> int:
    entries = find_devices()
    if args.json:
        print(json.dumps([{"serial": e.serial, "manufacturer": e.manufacturer, "product": e.product} for e in entries]))
        return 0
    if not entries:
        print("no hidpin device found")
        return 0
    for entry in entries:
        print(f"{entry.serial}  {entry.manufacturer} {entry.product}")
    return 0


def cmd_info(args) -> int:
    with open_device(args) as device:
        info = device.info
        if args.json:
            print(
                json.dumps(
                    {
                        "serial": device.serial,
                        "protocol_version": info.protocol_version,
                        "firmware": info.firmware_version,
                        "board": info.board,
                        "board_name": info.board_name,
                        "available": info.available,
                        "available_gpios": info.available_gpios,
                        "periodic_interval_ms": info.periodic_interval_ms,
                        "events_per_report": info.events_per_report,
                        "event_queue_size": info.event_queue_size,
                    }
                )
            )
            return 0
        print(f"serial number     : {device.serial}")
        print(f"board             : {info.board_name}")
        print(f"firmware          : {info.firmware_version}")
        print(f"protocol version  : {info.protocol_version}")
        print(f"available GPIOs   : {len(info.available_gpios)} pins {info.available_gpios}")
        print(f"periodic interval : {info.periodic_interval_ms} ms")
        print(f"events per report : {info.events_per_report} (queue of {info.event_queue_size})")
    return 0


def cmd_config_get(args) -> int:
    with open_device(args) as device:
        report = device.get_pin_config()
        available = device.info.available_gpios
        if args.json:
            print(
                json.dumps(
                    {
                        "result": int(report.result),
                        "result_gpio": report.result_gpio,
                        "request_id": report.request_id,
                        "pins": {str(gpio): str(report.config[gpio]) for gpio in available},
                    }
                )
            )
            return 0
        message = protocol.RESULT_MESSAGES.get(report.result, "unknown")
        print(f"result of the last write: {int(report.result)} ({message})")
        for gpio in available:
            print(f"  GPIO{gpio:<2} {report.config[gpio]}")
    return 0


def cmd_config_set(args) -> int:
    settings = dict(args.pins)
    with open_device(args) as device:
        report = device.set_pin_config(device.get_pin_config().config.with_pins(settings))
        if args.json:
            print(json.dumps({"request_id": report.request_id, "pins": {str(g): str(s) for g, s in settings.items()}}))
            return 0
        for gpio, setting in sorted(settings.items()):
            print(f"GPIO{gpio} is now {setting}")
    return 0


def cmd_output(args) -> int:
    levels = dict(args.pins)
    with open_device(args) as device:
        outputs = device.pin_config.outputs_mask
        unknown = [gpio for gpio in levels if not outputs >> gpio & 1]
        if unknown:
            names = ", ".join(f"GPIO{gpio}" for gpio in unknown)
            print(f"warning: {names} is not an output pin and will be ignored", file=sys.stderr)
        device.set_outputs(levels)
        report = device.request_status()
        if args.json:
            print(json.dumps({"levels": report.levels, "outputs": report.outputs}))
            return 0
        for gpio in sorted(levels):
            if outputs >> gpio & 1:
                print(f"GPIO{gpio} = {'HIGH' if report.level(gpio) else 'LOW'}")
    return 0


def report_json(device: Device, report: StatusReport, config) -> str:
    return json.dumps(
        {
            "seq": report.seq,
            "reason": flag_names(report.reason),
            "flags": flag_names(report.flags),
            "timestamp_us": report.timestamp_us,
            "monitored": report.monitored,
            "outputs": report.outputs,
            "levels": report.levels,
            "on": {str(gpio): state for gpio, state in device.on_off(report, config).items()},
            "events": [
                {
                    "gpio": event.gpio,
                    "level": event.level,
                    "age_us": event.age_us,
                    "start_us": event.start_us(report.timestamp_us),
                }
                for event in report.events
            ],
            "missed_reports": device.missed_reports,
        }
    )


def print_events(device: Device, report: StatusReport, config) -> None:
    for event in report.events:
        active_low = device.is_active_low(event.gpio, config)
        state = "ON " if (event.level != active_low) else "OFF"
        start = event.start_us(report.timestamp_us)
        when = "         ?" if start is None else f"{start / 1_000_000:10.6f}"
        print(f"[{when}] GPIO{event.gpio:<2} {state} ({'HIGH' if event.level else 'LOW'})")


def cmd_watch(args) -> int:
    with open_device(args) as device:
        config = device.pin_config
        for gpio in parse_gpio_list(args.active_low or ""):
            device.set_polarity(gpio, True)
        for gpio in parse_gpio_list(args.active_high or ""):
            device.set_polarity(gpio, False)

        initial = device.request_status()
        if args.json:
            print(report_json(device, initial, config), flush=True)
        else:
            states = device.on_off(initial, config)
            print(" ".join(f"GPIO{gpio}={'ON' if on else 'OFF'}" for gpio, on in sorted(states.items())), flush=True)

        missed = device.missed_reports
        while True:
            report = device.read_status(timeout_ms=1000)
            if report is None:
                continue
            if args.json:
                print(report_json(device, report, config), flush=True)
            else:
                if device.missed_reports != missed:
                    lost = device.missed_reports - missed
                    print(f"warning: missed {lost} status report(s)", file=sys.stderr)
                    missed = device.missed_reports
                if report.flags & StatusFlags.OVERFLOW:
                    print("warning: the device dropped edge events (the history is incomplete)", file=sys.stderr)
                if report.reason & Reason.CONFIG_CHANGED:
                    config = device.get_pin_config().config
                    print("the pin configuration changed", flush=True)
                if report.reason & Reason.OUTPUT_RESET:
                    print("outputs returned to their initial level (USB disconnect or suspend)", flush=True)
                print_events(device, report, config)
                if args.all and not report.events:
                    print(f"seq={report.seq} reason={','.join(flag_names(report.reason))}", flush=True)
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="hidpin", description="watch and configure a hidpin device")
    parser.add_argument("--json", action="store_true", help="print the result as JSON")
    subparsers = parser.add_subparsers(dest="command", required=True)

    def with_serial(sub: argparse.ArgumentParser) -> argparse.ArgumentParser:
        sub.add_argument("--serial", help="serial number of the device (optional when only one is connected)")
        return sub

    listing = subparsers.add_parser("list", help="list the connected devices")
    listing.set_defaults(func=cmd_list)

    info = with_serial(subparsers.add_parser("info", help="show the device information"))
    info.set_defaults(func=cmd_info)

    watch = with_serial(subparsers.add_parser("watch", help="keep printing status reports"))
    watch.add_argument("--active-low", help="GPIOs where LOW means ON (comma separated)")
    watch.add_argument("--active-high", help="GPIOs where HIGH means ON (comma separated)")
    watch.add_argument("--all", action="store_true", help="also print reports that carry no events")
    watch.set_defaults(func=cmd_watch)

    config = subparsers.add_parser("config", help="read or write the pin configuration")
    config_sub = config.add_subparsers(dest="config_command", required=True)
    config_get = with_serial(config_sub.add_parser("get", help="show the current pin configuration"))
    config_get.set_defaults(func=cmd_config_get)
    config_set = with_serial(config_sub.add_parser("set", help="change the pin configuration"))
    config_set.add_argument(
        "pins",
        nargs="+",
        type=parse_pin_spec,
        metavar="GPIO=SETTING",
        help="for example: 5=pullup:20 6=off 7=out:low (pins not named keep their setting)",
    )
    config_set.set_defaults(func=cmd_config_set)

    output = with_serial(subparsers.add_parser("output", help="change the value of output pins"))
    output.add_argument("pins", nargs="+", type=parse_output_spec, metavar="GPIO=VALUE", help="for example: 7=high 8=0")
    output.set_defaults(func=cmd_output)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except KeyboardInterrupt:
        return 0
    except HidpinError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
