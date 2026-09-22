# Architecture Decision Records

Short, immutable records of decisions with lasting consequences.
Numbered `NNNN-kebab-title.md`; never edit a decision after acceptance — supersede it.

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-modular-schema-owning-exporter.md) | omnistat is a modular, schema-owning exporter, not a blueprint client | accepted |
| [0002](0002-bind-via-attribute-side-patch.md) | Bind existing attributes with attribute-side PATCH | accepted |
| [0003](0003-additive-only-api-interface.md) | The core's API interface has no destructive methods | accepted |
| [0004](0004-identity-derivation.md) | Host identity is HMAC-SHA256 of the OS machine id under a fixed key | accepted |
| [0005](0005-core-owns-the-clock.md) | The core owns the clock; providers are on-demand collect functions | accepted |
| [0006](0006-rate-providers-keep-their-previous-reading.md) | Rate providers keep their previous reading and prime themselves | accepted |
| [0007](0007-platform-gating-declares-schema-everywhere.md) | Platform support is declared per attribute; the schema is declared everywhere | accepted |
| [0008](0008-gopsutil-as-the-host-reading-source.md) | Host readings come from gopsutil, not from hand-written per-OS sources | accepted |

Start from `../templates/adr.md`.
