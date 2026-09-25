---
status: accepted         # proposed | accepted | deprecated | superseded
date: 2026-09-25
deciders: [evgenii, Claude]
supersedes: null
---

# ADR-0012: The Linux service's security model

## Context

Spec 007 makes omnistat a systemd service installed by `sudo omnistat service install`.
ADR-0011 asked that a Linux service be judged against the same four points as the
Windows one, because each can be used to take over what the service does:
- the binary it executes;
- the account it runs as;
- where its token is kept;
- the config file it reads, which can set `base_url` and so decide where the token is
  sent.

Linux hosts at clients range from RHEL 8 (systemd 239) to current Fedora (systemd 259).
The owner asked for a modern systemd approach, no SysV, and a service that does not run
as root.

## Decision

`service install` sets the service up so that a non-root user has no path to its
secrets or its behaviour, and the service itself can read what it reports and little
else. `internal/systemd` owns this design (the `Host` seam, spec 007 NFR-004).

1. **Binary.** Install copies the running binary to `/usr/local/bin/omnistat`, root-owned
   and 0755, and the unit runs that copy. The copy is written as a new file in
   `/usr/local/bin`, so it takes that directory's SELinux label instead of keeping the
   download location's.
2. **Account.** The unit sets `DynamicUser=yes`. systemd allocates an unprivileged user
   for the service's lifetime: there is no account to create, clean up or give a
   password, just as with `NT SERVICE\omnistat`. The unit adds:
   - no capabilities, and `NoNewPrivileges`;
   - `ProtectSystem=strict` (a read-only system), with home directories hidden;
   - private `/tmp` and devices, and private users;
   - kernel, clock, hostname and cgroup protection, `ProtectProc=invisible`, and the
     `@system-service` system-call set without `@privileged`/`@resources`;
   - `SystemCallErrorNumber=EPERM`, so a filtered call fails instead of killing the Go
     runtime;
   - sockets limited to IPv4, IPv6 and Unix (the notify socket).

   `systemd-analyze security` rates it 1.1 "OK" (1.2 on systemd 239, which ignores four
   of the options). What remains is inherent: the service needs the network and `/proc`.
3. **Settings, including the token.** They are stored in `/etc/omnistat/omnistat.env`,
   root:root 0600, and referenced by `EnvironmentFile=`. systemd reads the file as root
   before it drops privileges and passes the settings as the process environment, so
   constitution IV holds without amendment.
   - Only the file's path is in the unit, so `systemctl show` reveals no value.
   - The process environment is readable only by the dynamic user and root.
   - Every stored setting is treated as a secret, because a proxy URL can carry
     credentials; output only ever names them.
4. **Configuration.** `/etc/omnistat` and its `omnistat.yaml` are root-owned, not
   writable by group or others, and readable by others: the dynamic user is "other".
   Install fixes any other ownership or mode and says so. The config is not secret, but
   it can redirect the token.
5. **Token input.** The token comes from the environment, the stored file or a no-echo
   prompt, never from a flag. `sudo` drops the caller's environment by default, so the
   prompt is the usual path.
6. **Inspectability and ownership.** Install and uninstall are a plan computed before
   anything changes. `--dry-run` prints it, including the full unit. The unit's first
   line marks it as omnistat's: a unit without the marker, one from another fragment
   path (a package) or a masked one is refused. Drop-ins belong to the admin and survive
   install and uninstall.

As on Windows, the token is stored in plain text behind file permissions, not encrypted.
Anyone who can read it is already root on the host.

## Alternatives considered

- **systemd credentials (`LoadCredential=`, systemd ≥ 247).** The token would reach the
  process as a file in `$CREDENTIALS_DIRECTORY` instead of an environment variable.
  omnistat starts no child processes, and its environment is readable only by its own
  user and root, so the gain is small. It would:
  - drop RHEL/Rocky/Alma 8;
  - need a second store for the other settings;
  - add a new way for omnistat to read the token.

  The owner chose the environment file (spec 007, "Decisions").
- **Encrypted credentials (`systemd-creds`, `LoadCredentialEncrypted=`, ≥ 250).** This
  protects the token at rest against a backup leak of `/etc`, using the host key or
  TPM. It drops RHEL 8, makes a hand rotation harder, and costs more code and tests. It
  could come later as an option.
- **A created `omnistat` system user** (`useradd -r`, or `sysusers.d`). It is a real
  account to create, own files as and remove on uninstall. `DynamicUser` gives the same
  isolation, and the sandbox defaults come with it.
- **A root service with sandboxing.** Root plus `CapabilityBoundingSet=` could be made
  similarly tight. But the owner's requirement is "not root", and a dynamic user
  degrades more safely if an option is ignored on an old systemd.
- **Register the binary where it was unpacked.** It needs no copy, but a user-writable
  binary run by a system service is a privilege-escalation hole, and upgrades would
  depend on where the operator unpacked.
- **A per-user unit (`systemctl --user`).** It needs no root at all, but the config
  would not be in `/etc`, the service would run at boot only with lingering enabled, and
  it would have the user's privileges. Out of scope.

## Consequences

- Positive: a non-root user can neither read the token nor change what the service
  executes, reads or where it sends data. That was verified in containers on five
  distros, and by the owner on a real host (spec 007 NFR-005).
- Positive: install, upgrade, rotate and uninstall match Windows, and a mixed fleet has
  one procedure.
- Negative / accepted trade-offs: the token is readable by root and present in backups
  of `/etc`, the same trust level as ADR-0011's registry value. On systemd 239–246, a
  few sandbox options are ignored, with a warning at reload.
- Negative: a module that needs more access widens the sandbox in its own spec (for
  example, `AF_NETLINK` for network interfaces). The next install rewrites the unit, so
  an upgrade ships the change.
- Follow-ups: accepted on 2026-09-25, after the owner's acceptance run (spec 007
  NFR-005).
