---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-10-05
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0015: A provider may query a local information service

## Context

ADR-0005 made providers pure, on-demand collect functions that never see "a timer, a
goroutine that outlives a call, a buffer, a timestamp or the API". Spec 003 FR-004
went further and forbade "any network access". Every module so far reads the host
through the OS (ADR-0008, ADR-0009).

Spec 012's `ups` module reads a UPS through apcupsd. apcupsd owns the USB connection to
the UPS, and its only machine interface is its Network Information Server (NIS): a
TCP request/response protocol, by default on loopback. Its status file is written only
when configured, and holds the same record. Its events file is a log for humans that
apcupsd trims from the front. A file watcher would be a long-lived goroutine, which
ADR-0005 rules out. So the module needs a network connection, but not the
Omnismith API.

## Decision

A provider **may** make one short-lived, read-only request to a local information
service (a daemon on the host that owns a device or a protocol) per collection, under
these rules:
- the address comes from the module's own settings (spec 012 FR-005), and the default
  is loopback;
- the connection is opened within `Collect`, bounded by the call's deadline (and by the
  module's own, shorter limit), and closed before `Collect` returns, on every path;
- the request only reads: no command that changes the service or the device;
- the reply is bounded in size and parsed defensively; a malformed reply fails that
  collection, never the process;
- never the Omnismith API, never a connection that outlives the call, never a listener.

The protocol client lives in the module that owns it, behind an injectable dialer. It
is not a host reading, so ADR-0009's single core package is not the place for it.
This amends 003 FR-004 and the "no network" reading of ADR-0005. The core keeps
scheduling, stamping, buffering and publishing.

## Alternatives considered
- **Read apcupsd's files.** The status file needs `STATTIME` > 0 in apcupsd's config and
  holds the same record. The events file needs tailing across truncation and parsing
  free text. Both need read access through the 007 sandbox, and neither shows a dead
  apcupsd any better.
- **Run `apcaccess`.** It is an external command, which every module so far has avoided,
  and it adds a binary dependency and process overhead every 10s.
- **A core "local service" package.** It would have one user and nothing to share yet
  (constitution VI). Extract it when a second protocol module arrives.
- **`mdlayher/apcupsd`.** An unmaintained 2023 pseudo-version; one unparseable field
  fails the whole status, it misreads `SELFTEST`, and a frame over 256 bytes panics.
  The protocol is about 120 lines.

## Consequences
- Positive: a UPS (and later other devices behind a daemon, such as NUT's `upsd`) can be
  monitored without privileges, external commands or file access. The
  007 sandbox already permits loopback TCP.
- Negative / accepted trade-offs: a collection's latency now includes a local round
  trip; a remote address sends the status in clear text (documented, opt-in).
- Follow-ups: when a second module needs a protocol client, decide whether to share it.
