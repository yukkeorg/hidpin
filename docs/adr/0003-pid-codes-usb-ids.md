---
status: accepted
---

# Use the pid.codes USB ids 1209:6870

**English** | [日本語](0003-pid-codes-usb-ids-ja.md)

The hidpin host tools find devices by VID:PID, and the udev rule grants access by VID:PID, so hidpin needs ids of its own that no other device uses.
pid.codes (VID `0x1209`) allocates PIDs free of charge to open source hardware and software whose source is public, and hidpin (MIT licence, public on GitHub) meets that condition.
For the PID we chose `0x6870`, the initials of hidpin in ASCII ("hp"). When we applied (2026-09-17), it collided with neither the 921 registered PIDs nor the 85 open pull requests.
The application ([PR #1280](https://github.com/pidcodes/pidcodes.github.com/pull/1280)) was accepted on 2026-09-28, and the registration is at <https://pid.codes/1209/6870/>.

## Considered Options

- Keep using the pid.codes test PID `1209:0001`: it is for in-house testing only and must not be used on a device that is redistributed, sold or manufactured. It can also collide with other test devices.
- Get a VID from the USB-IF: it costs 6,000 US dollars, which does not suit a personal open source project.
- Get a PID under the Raspberry Pi VID `0x2E8A`: this is offered to products built on Raspberry Pi silicon, and for devices that work with a standard driver such as HID, Raspberry Pi prefers a shared PID told apart by the product strings. The ids would also be tied to the chip vendor, which goes against keeping the project independent of the chip (ADR 0001).

## Consequences

- Anyone who redistributes a device running modified firmware gets a PID of their own (README, "About the USB identifiers"). The test PID can be used for in-house experiments.
- To change the registration (description, URLs), send a pull request to the pid.codes repository for `1209/6870/index.md` and `org/yukke.org/index.md`.
  The drafts of the application used to live in `docs/pid-codes/`; they were removed because they were identical to the registered files, and they remain in the git history.
