---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-09-25
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0011: The Windows service's security model

## Context

Spec 006 makes omnistat a Windows service installed by `omnistat service install`. A
service runs unattended with a long-lived access token, on hosts that often belong to a
client and have other local users. Four things a service touches can each be used to
take over what it does:

- the binary it executes;
- the config file it reads, which can set `base_url` and so decide where the token is
  sent;
- the place its token is kept;
- the account it runs as.

Constitution IV says secrets come "from the environment or a git-ignored file — never
from flags". Windows' defaults are too loose for this purpose:

- A folder under `%ProgramData%` lets users create files.
- A service's registry key is readable by every local user.
- The simplest service account, LocalSystem, is all-powerful.

The owner confirmed the resulting scope with the first Windows client (spec 006,
"Decisions taken during review").

## Decision

`service install` sets up the service with no path to its secrets or its behaviour for
a non-administrator. The owner of this design is `internal/winsvc` (the `Host` seam,
spec 006 NFR-003).

1. **Binary.** Install copies the running binary to `%ProgramFiles%\omnistat\omnistat.exe`
   and registers that copy. Program Files is writable only by administrators, and the
   directory inherits that. A service never runs from a user-writable path (FR-007).
2. **Account.** The service runs as the virtual account `NT SERVICE\omnistat`. It has
   low privilege, has no password to manage and gets its own SID. It needs no write
   access anywhere (FR-009).
3. **Settings, including the token.** The settings are stored as the service's own
   `Environment` (`REG_MULTI_SZ` under `HKLM\SYSTEM\CurrentControlSet\Services\omnistat`).
   The service manager adds them to the process environment at start, so omnistat reads
   them like any environment variable, and constitution IV holds without amendment.
   Before the value is written, the key gets a protected DACL: SYSTEM and Administrators
   only. The service manager runs as SYSTEM, and nothing else needs the key (FR-013,
   FR-015). Every stored setting is treated as a secret, because a proxy URL can carry
   credentials. Output only ever names them (FR-016).
4. **Configuration.** `%ProgramData%\omnistat` and its `omnistat.yaml` are owned by
   Administrators. They have a protected DACL: SYSTEM and Administrators full control,
   Authenticated Users read. The config is not secret, but it can redirect the token
   (FR-012).
5. **Token input.** The token comes from the environment or a no-echo prompt, never a
   flag. A prompt keeps it out of PowerShell's persistent history (FR-014).
6. **Inspectability.** Install and uninstall are computed as a plan of steps before
   anything changes. `--dry-run` prints that plan, and the real run applies the same plan
   (FR-028, constitution IV).

The token is stored in plain text behind the ACL, not encrypted. Anyone who can read it
is already an administrator of the host and could decrypt anything omnistat could.

## Alternatives considered

- **Token in a file under `%ProgramData%` with a restrictive ACL.** Equivalent
  protection, but a second place to secure and to forget at uninstall. It would also
  need the config file's `access_token` ban lifted or a second file format introduced.
  The service environment has no file and is deleted together with the service.
- **DPAPI or Credential Manager encryption.** The machine-scope DPAPI key is available
  to every process on the host, and user-scope DPAPI requires a user profile, which a
  virtual account does not have in a useful way. Either adds code and a decryption step
  at start, and protects against no one who could not already read the ACL-protected
  value.
- **LocalSystem or LocalService.** LocalSystem is unrestricted power for a process that
  reads CPU counters. LocalService is shared with other services, so an ACL granting it
  access grants every service that uses it.
- **Register the binary where it was downloaded.** It needs no copy, but a
  user-writable binary run by a service is a privilege-escalation hole, and upgrades
  would depend on where the operator unzipped.

## Consequences

- Positive: a standard local user can neither read the token nor change what the
  service executes, reads or where it sends data. Uninstall removes the token with the
  service.
- Positive: upgrading and rotating the token use the same command (spec 006 US-5).
- Negative / accepted trade-offs: the token is readable by administrators and in
  registry backups. That is the same trust level as a root-only `EnvironmentFile` on
  Linux.
- Negative: a change to stored settings takes effect only when the service restarts.
  Install restarts it.
- Follow-ups: verified on a real host by spec 006's acceptance run (T050, standard-user
  checks). A future Linux or macOS service-installation feature should be judged against
  the same four points: binary, account, secrets, config.
