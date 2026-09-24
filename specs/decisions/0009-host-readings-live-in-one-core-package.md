---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-09-24
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0009: Host readings live in one core package

## Context

ADR-0008 made gopsutil the source of host readings, under two rules: stateless reads
only, and only values the OS actually maintains. It placed the real implementation
inside the one module that needed it (`cpu`) behind a `Reader` interface that module
declares. That was right for a single consumer.

Spec 005's `memory` module is the second consumer. Left as it is, each value module
would carry its own gopsutil file, and the two ADR-0008 rules would be re-stated,
re-reviewed and possibly relaxed in every module. `disk` and `net` would make it four.
The rules are about the **source**, not any one module, so they belong where the source
is used.

The same modules also repeat one small piece of logic: collecting the observations a
collection could not produce and reporting them in one log record (004 FR-016, 005
FR-013).

Constitution VI forbids module-to-module imports ("shared needs go through the core")
and requires every new package to be justified against a requirement: here, 005 NFR-006.

## Decision

`internal/hostread` is a core package and the **only** package that imports gopsutil.
It exposes one small, stateless, concrete reader type per area (`hostread.CPU`,
`hostread.Memory`), each returning plain structs. Its package documentation states
ADR-0008's rules, and every exported method follows them: no package-level baseline, no
goroutine that outlives the call, and no value the OS does not maintain unless the doc
comment says so and names who must gate it.

Value modules still **declare and own** their narrow `Reader` interfaces (ADR-0008 is
unchanged in this respect). The `hostread` type satisfies the interface structurally, so
tests keep faking the module's own interface and never touch `hostread`. The *meaning*
of a reading stays in the module: for example, which CPU states count as idle (004
FR-005) remains `cpu`'s decision, not `hostread`'s.

The one-record-per-collection omissions helper moves into the existing core package
`internal/module`, next to `Observation`, as `module.Omissions`.

## Alternatives considered

- **Keep a gopsutil file per module** (status quo). No new package. But the ADR-0008
  rules would be enforced module by module, and each new module's author would re-read
  gopsutil's platform files. Rejected at the second consumer, at the owner's request,
  rather than waiting for the third.
- **One wide `Host` interface in the core** that every module depends on. It would
  couple every module to every reading and make a module's fake implement methods it
  never calls. Rejected: consumer-owned interfaces keep each module's test surface
  exactly as wide as its needs.
- **Put the omissions helper in `hostread`.** It is about observations and logging, not
  host readings, and `hostname`/`machine-id` could use it without reading anything
  through gopsutil. Rejected in favour of `internal/module`.
- **Move the reading semantics (usage arithmetic, MiB rounding) into `hostread` too.**
  That would make the reading package the owner of spec decisions it cannot test against
  the spec. Rejected: `hostread` returns what the OS reports; modules decide what it means.

## Consequences

- Positive: gopsutil is imported in exactly one place, so an upgrade diff or a
  replacement for one platform touches one package, and the ADR-0008 rules are reviewed
  there once.
- Positive: a new value module adds one `hostread` file for its readings and otherwise
  looks exactly like `cpu` and `memory`.
- Negative / accepted trade-offs: `cpu`'s reading types move out of the `cpu` package.
  `cpu` keeps type aliases (`cpu.Times`, `cpu.Load`), so its tests and API are unchanged,
  but the struct definitions now live one package away from the arithmetic that uses
  them.
- Negative: `hostread` is harder to unit-test than the modules. It can only be
  smoke-tested on the platform running `go test` (Linux in CI). The other platforms are
  covered by the cross-compile gate, as before.
- Follow-ups: if a later module needs a reading gopsutil gets wrong or does not expose,
  implement it in `hostread` behind the same per-area type (ADR-0008 follow-up), not in
  the module.
