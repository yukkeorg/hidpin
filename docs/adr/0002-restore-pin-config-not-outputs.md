---
status: accepted
---

# After reconnecting, write the pin configuration back but do not restore output levels

**English** | [日本語](0002-restore-pin-config-not-outputs-ja.md)

The high-level layer of the Go library (meant for long-running daemons) holds a **target pin configuration** that covers every **available GPIO** (a GPIO not named is "unused").
When the **pin configuration** on the **device** differs from it, the layer writes it back, both on reconnection and when it is changed from outside while connected.
**Output levels**, on the other hand, are not part of the target. The layer only reports that they returned to their **initial output levels**; whether to drive them again is up to the application.
The **initial output level** exists to put the equipment into a safe state when USB is disconnected, suspended or reset (PROTOCOL.md 7.2).
If the library drove the outputs again by itself, equipment would start moving while nobody is watching, such as right after the host resumes from sleep.
Writing the pin configuration back only makes each **output pin** start at its **initial output level**, so the safe state holds.

## Considered Options

- Restore the output levels automatically as well: applications could treat outputs as "the state they should be in", but this was rejected for the reason above.
- Follow changes to the pin configuration made from outside, only reporting them: the target and the actual configuration would differ until the next reconnection, which is inconsistent with writing it back on reconnection.
- Hold only the pins named: the pins could be shared with another program, but unwired pins would keep the pull-ups of the **default pin configuration**.

## Consequences

- Pointing two programs with different **target pin configurations** at the same **device** makes them write it back over and over. The high-level layer reports this.
- Uses that should not touch the pin configuration (the CLI `watch`, a daemon that only records) run without a **target pin configuration**. They then follow the **pin configuration** on the **device** and write nothing back.
