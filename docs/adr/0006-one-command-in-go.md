---
status: accepted
---

# Keep one hidpin command, the Go one, in tools/hidpin

**English** | [日本語](0006-one-command-in-go-ja.md)

The `hidpin` command existed twice, in Python (`python/src/hidpin/cli.py`) and in Go (`cmd/hidpin`), with the same commands, options and output. Every change to the command had to be made, tested and documented in both, and the two had already drifted apart (for a while only the Go one kept watching when the board was unplugged).
Because each copy of the same thing needs its own maintenance, only one command is kept: the Go one, which is the official `hidpin` command. The Python command and its tests are removed, and `python/` holds the host library only.
The source of the command moves from `cmd/hidpin` to `tools/hidpin`, so that the development of the tools stays under `tools/`. It stays in the Go module at the repository root: `go install github.com/yukkeorg/hidpin/tools/hidpin@latest`.

## Considered Options

- Keep both commands: rejected for the reason above.
- Keep the Python command instead: not chosen; the Go one is the official command.
- Give `tools/` a Go module of its own: not needed for now, as the command depends only on the host library in the same module.

## Consequences

- Installing the command needs Go (1.22 or newer) instead of Python; it is Linux only, like the Go host library.
- Changes to the command are made in `tools/hidpin` alone. Its tests run against the fake device and the simulated bus of `hidpin/hidpintest`, without hardware.
- The decision covers the command, not the host libraries: Python, Go and Rust each keep a host library, held to the same API by docs/HOST_API.md and to the same wire format by `protocol/vectors.json`.
- ADRs 0001, 0002 and 0004 still speak of "the CLI" or "both CLIs"; they record decisions made when there were two, and are left as they are.
