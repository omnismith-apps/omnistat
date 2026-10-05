---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-10-05
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0014: Entities are identified by the platform's external key; modules may own entities

## Context

Until 0.5.0 omnistat wrote to one record, the host entity. It found that entity
itself (spec 002): it searched the host template for the `machine_id` attribute,
created the entity when the search came back empty, waited for search to catch up
with its own write (the platform processes writes asynchronously), and fell back to
"oldest wins" when two first starts raced.

Two things changed in October 2026:
- **Devices need records of their own.** A UPS on the host's USB port (spec 012), and
  later disks and network interfaces (deferred by 008 and 010), are not attributes of
  the host. The core could not identify a second entity, link it to the host or
  publish to it.
- **The platform gained `external_key`.** It is the id another system uses for a
  record, unique among a template's live records under a partial unique index on the
  entity row. Upsert by key creates the record holding a key, or updates the one that
  does, in one request. A lookup by key reads the same row, so it sees an upsert at
  once; only search lags.

## Decision

Every entity omnistat owns is identified by its **external key** within its template:
the host by the identity of ADR-0004, and a module-owned entity by the key its
provider supplies. The key is used verbatim, with no namespace.

- **Resolution:** look up by key; if no record holds the key, upsert it with only the
  identity attribute (host) or the host link (module entity). Lookup comes first, so a
  restart writes nothing.
- **Adoption:** a host entity created before keys existed is found once by
  `machine_id` and given its key. omnistat never overwrites or clears a key it finds
  set; it uses that entity and warns.
- **Module-owned entities:** a manifest may declare an *entity template* with exactly
  one *host link*, a reference to the host template. The core resolves these entities
  when it first publishes to them, buffers each entity separately, and writes the
  host link with every dimension update. Its display attribute is the host's
  highest-ranked *label* attribute (`hostname` 2, `machine_id` 1).
- Nothing is ever deleted. A module entity that disappears mid-run is created again;
  the host's disappearance still ends the run.

## Alternatives considered
- **Keep 002's attribute search and extend it to module entities.** This would carry
  the search lag, the bounded wait, the inconclusive outcome and the duplicate rule
  into every device, while the platform now guarantees uniqueness itself.
- **Keys for module entities only.** This would leave two identity mechanisms side by
  side, and hosts would keep the race window the key removes.
- **Namespaced keys (`omnistat:<id>`).** These rule out accidental collisions, but also
  intentional sharing: a UPS card's webhook keyed by serial, or a second UPS source.
  Auto-discovered host keys are opaque hashes and cannot collide by accident.
- **Upsert on every start.** This saves one request, but writes on every restart and
  may fire automations.
- **Drop `machine_id`.** The key makes it redundant, but its slug is a contract (001
  FR-002) and adoption needs it. Removing it is a separate, breaking decision.

## Consequences
- Positive: no duplicate host or device can be created, whatever the timing; one
  lookup per start; devices get their own records, history and automations, linked to
  their host; other systems can feed the same record by key.
- Negative / accepted trade-offs: an adopted host whose key someone else set is
  re-adopted (one search) at every start; a key changed by a user makes a module
  entity reappear as a new record; omnistat now depends on a platform feature
  introduced in October 2026; SDK v1.0.18 cannot decode an entity with no values, so
  the client reads the id from the raw body.
- Follow-ups: per-disk and per-interface entities reuse this; removing `machine_id`
  would need its own ADR.
