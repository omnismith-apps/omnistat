---
status: proposed         # proposed | accepted | deprecated | superseded
date: 2026-09-26
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0013: Where `omnistat upgrade` gets a binary, and what it trusts

## Context

Spec 009 adds `omnistat upgrade`, which replaces the binary of an installed service.
That binary runs from boot, holds the host's access token, and is run by root (Linux) or
SYSTEM's service manager (Windows) during install. Whatever upgrade accepts as "the new
omnistat" gets the same position that ADR-0011 and ADR-0012 protect for the installed
binary. Constraints:
- releases are published by GoReleaser to GitHub with a `checksums.txt` and no
  signatures (`.goreleaser.yaml`);
- fleets can be large and sit behind one NAT address, while the GitHub REST API allows
  60 unauthenticated requests per hour per address;
- some hosts reach the internet only through a proxy, and some not at all;
- hardened Linux hosts mount `/tmp` with `noexec`;
- the service itself is unprivileged and read-only, so it cannot upgrade itself.

## Decision

Upgrade trusts **the releases location over HTTPS** and verifies integrity, not
authorship:

1. **Source.** The default is `https://github.com/omnismith-apps/omnistat/releases`,
   through the web download URLs (`latest/download/…`, `download/<tag>/…`), not the REST
   API. The version comes from the archive names in `checksums.txt`.
   `OMNISTAT_RELEASES_URL` may name a mirror with the same layout. A mirror is exactly
   as trusted as GitHub.
2. **Transport.** HTTPS only, with no redirect down to HTTP. Plain HTTP is allowed for a
   loopback mirror only.
3. **Integrity.** The archive's SHA-256 must match its line in the same release's
   `checksums.txt`. The unpacked binary must report the target version when run.
4. **Staging.** The download is unpacked into a new 0700 directory next to the installed
   binary (root- or Administrators-only, and executable). It is never unpacked in the
   system temporary directory.
5. **Hand-over.** The verified binary's own `service install` does the replacement. The
   security model of ADR-0011 and ADR-0012 is therefore applied by the version being
   installed, not by the old one.
6. **Who triggers it.** An operator with root or Administrator rights. The service never
   upgrades itself.

## Alternatives considered
- **GitHub REST API** (`/repos/…/releases/latest`): richer metadata, but it is
  rate-limited per address and needs a JSON contract that a static mirror cannot serve.
- **Verify signatures now** (cosign keyless or minisign with a key pinned in the
  binary): this is the right end state, but it needs signing in the release workflow
  and a key or identity policy the owner has not chosen. Without it, a signature check
  would be theatre. Deferred, not rejected.
- **Install with the running binary's logic** (copy the download over the installed
  binary): then the new version's unit, sandbox and registration would ship only one
  upgrade later, and the result would differ from the documented manual flow.
- **Self-upgrade from the service**: impossible without giving the service write access
  to its own binary. That would undo ADR-0011 and ADR-0012.

## Consequences
- Positive: one command per host. A corrupted, truncated or proxy-tampered download
  never reaches the service. Fleets are not throttled by API limits, and air-gapped
  sites can mirror with static files.
- Negative / accepted trade-offs: someone able to publish a GitHub release of
  `omnismith-apps/omnistat`, or to control a configured mirror, can ship a binary that
  upgrade accepts. This is the same trust the manual flow places in the releases page.
- Follow-ups: sign releases in `.github/workflows/release.yml` and verify the signature
  in upgrade before the hand-over. That needs its own spec, and it supersedes point 3 of
  this ADR.
