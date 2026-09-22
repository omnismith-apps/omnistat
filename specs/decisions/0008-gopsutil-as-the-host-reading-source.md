---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-09-22
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0008: Host readings come from gopsutil, not from hand-written per-OS sources

## Context

Constitution V puts the standard library first: "a dependency must earn its place in a
plan against a requirement ID". Until now nothing needed more than `os` — `hostname` uses
`os.Hostname`, `machine-id` reads a file or runs one command. Spec 004's `cpu` module is
the first that needs values the standard library does not expose at all: cumulative
per-state CPU time counters, load averages, logical CPU count and the CPU model string.

On Linux those are four small parsers over `/proc`. The question is what happens for
every platform and every module after that. `cpu`, `memory`, `disk` and `net` on three
operating systems is twelve hand-written sources, each needing its own OS-specific
knowledge (`/proc/stat`'s USER_HZ on Linux, `host_processor_info` on macOS,
`NtQuerySystemInformation` on Windows), and eleven of the twelve untestable on the CI
runner that builds them.

The product goal is that omnistat eventually runs on Windows as well as Linux and macOS.

## Decision

Host readings for value modules come from `github.com/shirou/gopsutil/v4`, pinned in
`go.mod`, reached only through a narrow interface owned by the module that needs it, so
that the readings are injectable and fakeable in tests (004 NFR-005).

Two constraints on how it is used:

- **Only stateless reads.** `cpu.Times`, `load.Avg`, `cpu.Counts`, `cpu.Info` and their
  equivalents. `cpu.Percent` is forbidden: it keeps a package-level baseline seeded in an
  `init()`, shared by every caller in the process. The rate arithmetic stays in the
  provider, where ADR-0006 put it.
- **Only readings the OS actually maintains.** Where the library emulates a value the
  platform does not have — `load.Avg` on Windows starts a background goroutine that
  samples processor queue length and applies its own decay — omnistat does not collect it
  (ADR-0007). That emulation would also violate ADR-0005's rule that no goroutine
  outlives a collect call.

Modules that the standard library already serves on every target keep using it:
`hostname` stays on `os.Hostname`, and `machine-id` is not migrated.

## Alternatives considered

- **Hand-written per-OS sources** — what spec 004 first assumed. Zero dependencies and
  fully unit-testable parsing: a test can assert that a given `/proc/stat` body yields
  9.9%. But it is Linux-only in practice, it scales as modules × platforms, and it puts
  omnistat in the business of tracking three kernels' counter semantics. Reasonable for
  one module on one platform; the wrong bet across the roadmap.
- **`prometheus/procfs`** — excellent and well-tested, but Linux-only by design. It
  solves the parsing and none of the portability.
- **`elastic/go-sysinfo`** — comparable coverage and a cleaner interface, but a smaller
  contributor base and a narrower metric surface for the modules planned after `cpu`.
- **`mackerelio/go-osstat`** — small and focused, but no Windows support.
- **Shelling out to `top`, `iostat`, `wmic`** — slow, locale- and version-dependent, and
  it breaks 004 NFR-004's "executes no external command". Never seriously on the table.

## Consequences

- Positive: `usage`, `cpu_model`, `cpu_cores` and `cpu_arch` work on Linux, macOS and
  Windows from the first release of the module, and the next four metric modules get
  their readings for free.
- Positive: measured, not assumed — the dependency builds with `CGO_ENABLED=0` for
  `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64` and
  `windows/arm64`, so constitution V's static-binary rule holds, with room for the
  Windows pairs if the matrix is ever amended.
- Negative / accepted trade-offs: the module graph grows from 3 modules to 12 (gopsutil
  plus purego, go-ole, plan9stats, perfstat, go-sysconf, numcpus, wmi and `x/sys`), most
  of which are inert on any one platform. This is the single largest dependency decision
  in the repository so far and is worth revisiting if the transitive tree grows further.
- Negative: parsing moves out of omnistat's tests. What can still be asserted is the
  arithmetic that encodes our decisions — the clamping of 004 FR-005, the re-baselining
  of FR-013, the partial-collection rules of FR-015 — against a faked reading source.
  What is lost is "this `/proc/stat` body yields 9.9%". Accepted: those tests would have
  pinned down the kernel's format, which is not omnistat's contract to keep.
- Negative: a bug in an upstream platform path is a bug omnistat ships and cannot easily
  test. Mitigated by the narrow interface — replacing the source for one platform, or
  altogether, touches one implementation.
- Follow-ups: pin the version in `go.mod` and treat an upgrade as a change worth reading
  the changelog for, since these are values operators alert on. If a later module needs a
  reading gopsutil gets wrong or does not expose, implement that one reading behind the
  same interface rather than forking the decision.
