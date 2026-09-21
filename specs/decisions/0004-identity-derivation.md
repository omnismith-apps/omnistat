---
status: accepted
date: 2026-09-22
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0004: Host identity is HMAC-SHA256 of the OS machine id under a fixed key

## Context
Spec 002 FR-007 requires the published identity of an autodiscovered host to be
derived, not the raw OS id: on Linux `/etc/machine-id` is documented as
confidential and applications are told to hash it with an application-specific
key (`sd_id128_get_machine_app_specific`). The derivation is a public contract:
every host's entity is keyed by it, so changing it silently would orphan every
existing host entity.

## Decision
`identity = hex(HMAC-SHA256(key = "omnistat/host-identity/v1", msg = trim(raw)))`,
64 lowercase hex characters. The source is reported as `linux-machine-id` or
`darwin-platform-uuid`. A static identity is published verbatim (source
`static`). The golden vector is pinned in `internal/module/machineid`'s tests;
a change to the key, hash or encoding requires a new source name (`…/v2`), a new
ADR and a migration note, never an in-place edit.

## Alternatives considered
- **Raw machine id** — simplest to cross-check by hand; leaks a value the OS
  says not to expose and lets a third party correlate hosts across systems.
- **Plain SHA-256 without a key** — still correlatable by anyone who knows the
  raw id; the key is what makes the value omnistat-specific.
- **Random id persisted on disk** — rejected by spec 002 FR-006: a lost state
  file would create a new entity per host, which is exactly the failure the
  identity module exists to prevent.

## Consequences
- Positive: stable across runs, reinstalls of omnistat and upgrades; fixed
  length; no raw id in logs, payloads or the platform.
- Negative: an operator cannot recognise a host from its identity alone; the
  `identity` command and the `hostname` attribute (feature 003) are the way to
  map it back. Reinstalling the OS changes the identity by design.
