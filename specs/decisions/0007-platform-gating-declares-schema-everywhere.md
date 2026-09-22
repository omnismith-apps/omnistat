---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-09-22
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0007: Platform support is declared per attribute; the schema is declared everywhere

## Context

Constitution V says a module that cannot support a platform "says so in its manifest and
is skipped there, not stubbed". Spec 003 deferred the mechanism until a module needed it;
spec 004's `cpu` module is that module — and it needs a finer granularity than "module".

With a cross-platform reading source (ADR-0008), `cpu` can report usage, model, core
count and architecture on Linux, macOS and Windows. It cannot report load averages on
Windows: that OS maintains none. What exists there is a sampled processor-queue-length
average, a different quantity that would be a lie under the slug `load_avg_1`. So the
unit of support is the **attribute**, not the module. A module-level switch would force
a choice between dropping three useful metrics on Linux and macOS, publishing a
fabricated number on Windows, or splitting `cpu` into two modules for one platform's
sake.

"Skipped" is also ambiguous about the *schema*. omnistat reconciles the schema from the
union of the enabled modules' manifests, from whichever host happens to run first. If an
uncollectable attribute contributed nothing, a project first reconciled from a Windows
host would lack the load-average attributes until a Linux host ran, and `schema verify`
would report a different "complete" schema depending on where it ran. That collides with
constitution III: "with no configuration, the same set of modules yields the same schema
in any project and across redeployments."

## Decision

A manifest may declare, per attribute, the platforms on which that attribute can be
collected; declaring none means everywhere. On a platform where an attribute is not
collectable:

- the core does **not** expect it and the provider does **not** return it, in any run
  mode, and the skip is logged once at startup with the attribute and the reason;
- a module **all** of whose attributes are uncollectable here is not scheduled or called
  at all;
- every enabled module **still** contributes its whole manifest to the desired schema, so
  reconciliation is identical from every host in a mixed fleet.

Support is a property of the platform, not of the configuration: explicitly enabling a
module whose attributes are uncollectable here yields the same skip, never an error.
Disabling a module in config remains distinct and still removes both its schema and its
values.

## Alternatives considered

- **Module-level support only** — the granularity spec 004 first assumed, back when the
  plan was to hand-write Linux-only readings. Once the readings became cross-platform it
  would have cost either the load averages everywhere or an invented number on Windows.
  Rejected; the attribute is the honest unit, and the module-level case survives as the
  degenerate one (all attributes uncollectable ⇒ module not scheduled).
- **Uncollectable ⇒ contributes nothing to the schema either** — the literal reading of
  constitution V. Rejected: it makes the project schema depend on which host reconciled
  first, which constitution III forbids, and it makes `schema verify` non-deterministic
  across a mixed fleet. It would also surprise the operator who applies the schema from a
  laptop and then deploys to hosts with a token that cannot write schema.
- **Collect the nearest available substitute** (Windows processor queue length as a load
  average) — rejected outright. A metric series that means one thing on some hosts and
  another on others is worse than an absent one, and it cannot be detected downstream.
- **No gating: let the provider fail every tick** — a Windows host would log a failure
  every 10 seconds forever and report every run as partial (exit 2), training operators
  to ignore a status that exists to be noticed. Rejected.
- **Build tags: compile the attribute only into supported builds** — the registry, the
  schema and the help output would then differ per binary, `schema apply` from the wrong
  build would silently omit attributes, and the same non-determinism returns with no way
  to test the decision on the other platform. Rejected; the platform check stays a
  runtime value so it is testable without the platform (004 NFR-005).
- **A config flag to force collection where it is unsupported** — an escape hatch for a
  situation nobody has. Rejected on the simplicity gate; a module that gains support for
  a platform declares it.

## Consequences

- Positive: one schema across a mixed fleet, whichever host reconciles; a host collects
  everything it honestly can and stays quiet about the rest; the mechanism is
  declarative, so `memory`, `disk` and `net` inherit it for free.
- Positive: the skip is visible where an operator looks — the startup schedule line and
  the dry-run output — and is distinguishable from "disabled".
- Negative / accepted trade-offs: a project reconciled from a fleet that can never
  collect a given attribute still carries it, empty. It is additive and harmless, and an
  operator who objects disables the module.
- Negative: support is declared per platform, not per host capability, so a Linux host
  that happens to lack a source (an exotic container) still collects the attribute and
  reports ordinary per-observation omissions (004 FR-015). That is the right signal — it
  is a deficient host, not an unsupported platform.
- Negative: a chart of `load_avg_1` across a mixed fleet has gaps for the Windows hosts.
  That is the truth about those hosts, and it is preferable to a fabricated line.
- Follow-ups: nothing here expresses "supported but degraded". If a source ever needs
  that, prefer a separate attribute with honest semantics over a quality flag.
