---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-09-25
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0010: Windows is a supported platform

## Context

Constitution V builds omnistat for `linux/amd64`, `linux/arm64`, `darwin/amd64` and
`darwin/arm64`. Windows sits outside that list, but omnistat has been built for it
anyway:

- ADR-0008 chose a host-reading source partly because it covers Windows.
- Specs 004 and 005 specify the `cpu` and `memory` readings for Windows and declare which
  attributes Windows cannot supply (ADR-0007).
- `make crosscheck` has built `windows/amd64` and `windows/arm64` since 004 (NFR-005).
- The release pipeline publishes Windows archives.

Spec 004 says omnistat cannot run on Windows "until host identity … and service shutdown …
are specified, and until constitution V's build matrix is amended by ADR". That is now
needed, because the first Windows host will belong to a client.

Adding two entries to the matrix is not enough. "Supported" also needs a definition. The
project has one Linux workstation and CI. It has no Mac and no permanent Windows machine.
If every platform had to pass full acceptance for every feature, each feature would cost
three machines. macOS is in the matrix today and no omnistat build has ever run on it.

## Decision

Windows becomes a supported platform on `amd64` and `arm64`, with a defined verification
bar. Constitution V is amended (1.0.0 → 1.1.0) to read:

> - One static binary (`CGO_ENABLED=0`), cross-compiled for `linux/amd64`, `linux/arm64`,
>   `darwin/amd64`, `darwin/arm64`, `windows/amd64` and `windows/arm64`. Linux is the
>   primary target; a module that cannot support a platform says so in its manifest and
>   is skipped there, not stubbed.
> - Windows is supported (ADR-0010): every feature specifies its Windows behaviour or
>   declares it unsupported. The unit tests run on Windows in CI. A feature that adds or
>   changes platform-specific behaviour is accepted on a `windows/amd64` host as well as
>   on Linux. `windows/arm64` is built and released but not accepted.

The supported Windows versions are those the pinned Go toolchain supports: Windows 10,
Windows Server 2016 and later.

## Alternatives considered

- **Keep Windows out and tell clients to pin a static identity and run a console.** It
  costs nothing now, but omnistat would stay unusable on Windows. The Windows readings
  were already paid for in 004/005.
- **Support `windows/amd64` only.** `arm64` builds with no extra work and the code has
  no architecture-specific paths. Dropping it would save no effort. It only goes
  unaccepted, and the ADR says so.
- **Full acceptance on every platform for every feature.** This is the strongest
  guarantee, but it needs a Windows machine and a Mac for every change. Most features
  (value modules, the publisher) behave the same on every OS once the readings are
  unit-tested. So the bar is set by whether a feature touches platform-specific
  behaviour, not by the platform.
- **Unit tests on Linux only (cross-compilation as the Windows gate).** Compiling is not
  running. Path handling, line endings, console signals and anything else that only
  differs at runtime would never be exercised. A Windows CI runner costs one job.

## Consequences

- Positive: omnistat can be delivered to Windows hosts with a stated guarantee, and the
  Windows readings of 004/005 finally run for real (spec 006).
- Positive: every spec now has to say what happens on Windows. A gap becomes a visible
  decision.
- Negative / accepted trade-offs: CI gains a Windows job, which is slower and costs more
  minutes than Linux. `windows/arm64` ships unaccepted.
- Negative: macOS is now the least-verified platform. It is in the matrix, and no build
  has ever run on it or is required to. That asymmetry is recorded here, not fixed.
- Follow-ups: the constitution was amended on acceptance (2026-09-25, `version: 1.1.0`,
  `amended_by: [0010]`), and `specs/templates/spec.md` gained the platform line. The
  Windows CI job comes with spec 006 (NFR-004).
