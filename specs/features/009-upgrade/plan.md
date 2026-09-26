---
feature: 009-upgrade
status: approved          # draft | approved | done
approved: 2026-09-26
spec: ./spec.md
created: 2026-09-26
depends_on: [006-windows, 007-linux-service]
---

# Plan: `omnistat upgrade`

## Constitution check
- [x] Uses the SDK for all API access (II). Upgrade makes no Omnismith call. The
  handed-over `service install` makes its usual read-only pre-check through the SDK.
  Downloads from GitHub or a mirror are not Omnismith API calls, so `net/http` is right
  for them.
- [x] Every template/attribute lives in exactly one module manifest with default slugs (III).
  No schema change.
- [x] Reconciliation stays additive; no destructive API call anywhere (III, IV). The only
  host-side deletion is upgrade's own staging directory.
- [x] No secret can reach a commit, a flag, or a log line (IV).
  - Stored settings are read only to pick a proxy. The proxy is passed to the HTTP
    transport and never formatted into output.
  - Messages name the proxy variable, never its value (FR-016).
  - The CLI tests assert that the sentinel token and proxy password never appear.
- [x] Every mutation is covered by dry-run (IV). `upgrade --dry-run` verifies the
  download and runs the new binary's `service install --dry-run`, which is 007's
  complete description of the change. `--check` is read-only.
- [x] Every FR/NFR has a test strategy using a fake API (V). The release location is an
  `httptest` server. The binaries are run through a `Runner` seam faked in CLI tests,
  and the real runner is tested against a helper process. Both service backends use
  their existing fakehosts.
- [x] Each new package/dependency is justified by a requirement ID; no module-to-module
  imports (VI).
  - The new package `internal/upgrade` covers release resolution, download,
    verification, staging and running binaries (FR-005–FR-013, FR-015).
  - The new dependency `golang.org/x/net` (only `http/httpproxy`) covers FR-012. The
    proxy has to come from a map (the stored settings), and `http.ProxyFromEnvironment`
    reads only the process environment, once. `httpproxy` is the standard library's own
    implementation of the same rules, including `NO_PROXY` and the loopback bypass, so
    re-implementing it would be worse.

## Technical context
- Go 1.26, `CGO_ENABLED=0`, SDK `v1.0.15` (unchanged), `golang.org/x/net` added at the
  version matching `x/sys v0.41.0`.
- The GitHub release layout was checked on 2026-09-26 against v0.3.0:
  - `…/releases/latest/download/checksums.txt` returns 302 to
    `…/releases/download/v0.3.0/checksums.txt`, then 302 to
    `release-assets.githubusercontent.com` (HTTPS throughout);
  - `checksums.txt` has lines `<sha256>  omnistat_<ver>_<os>_<arch>.<ext>`, where `<ver>`
    has no leading `v` (GoReleaser `.Version`), `<ext>` is `tar.gz`, or `zip` on
    Windows;
  - a pre-release tag `v0.5.0-rc.1` gives `<ver>` = `0.5.0-rc.1`;
  - `latest` never resolves to a pre-release or a draft.
- The archive holds `omnistat` (`omnistat.exe`), `README.md`, `CHANGELOG.md` and
  `.env.example`, at the top level (`.goreleaser.yaml`).
- `omnistat version` prints `omnistat <version>\n`. Local builds print a `git describe`
  string (`v0.3.0-3-gd38c574-dirty`), or `dev`.
- Docker's `--tmpfs` mounts are `noexec` by default. The e2e container's `/tmp` is
  therefore `noexec`, which checks FR-011 for free.

## Approach

### Flow (`internal/cli/upgrade.go`)
1. Parse `--check`, `--dry-run` and `--version`. `--check` with `--dry-run` is a usage
   error (FR-001).
2. `a.installer()` picks the backend, as `service` does. An unsupported platform gets
   upgrade's version of the `service` message (FR-004).
3. `inst.Installation(ctx)`:
   - the backend checks root or elevation (FR-002) and systemd (007 FR-004), and that
     the unit or service is omnistat's own (FR-003);
   - it returns `service.Installation{Exists, Binary, Settings}`;
   - `!Exists` fails with the "not installed" message (FR-003).
4. The installed version is `runner.Version(installed binary)`, parsed with
   `upgrade.ParseVersion`. A binary that fails or reports a non-release version gives
   *unknown* (FR-005).
5. `upgrade.ReleasesURL(getenv)` applies the URL policy (FR-015).
   `upgrade.NewClient(upgrade.ProxySettings(getenv, stored))` builds the client
   (FR-010, FR-012).
6. `upgrade.Resolve(ctx, client, base, --version, goos, goarch)` returns a `Release`:
   version, tag, asset name, expected SHA-256 and archive URL (FR-006, FR-008).
7. Decide (FR-007): up to date, installed newer, or downgrade. `--check` prints and
   exits 0 or 2 (FR-017).
8. `upgrade.Stage(dir of the installed binary)` removes leftovers and creates a new
   0700 staging directory. The cleanup is deferred (FR-011). Tests override the
   directory with `App.StageDir`.
9. `upgrade.Fetch(ctx, client, rel, stage, goos)`:
   - stream the archive to disk while hashing it;
   - compare the hash;
   - unpack exactly the binary entry, with a size limit;
   - return its path (FR-009, FR-010).
10. `runner.Version(staged)` must equal the target (FR-009).
11. `runner.Install(staged, ["service", "install"] + ["--dry-run"])` runs with the
    terminal attached and the environment unchanged, and yields the exit status
    (FR-013).
12. Summary line and exit status (FR-014).

### `internal/upgrade`
| File | Contents | Requirement |
|------|----------|-------------|
| `version.go` | `Version`, `ParseVersion` (optional `v`, SemVer 2.0 core + pre-release; rejects `dev` and `git describe` suffixes `-N-g<hex>[-dirty]`), `Compare`, `ParseVersionOutput` | FR-005, FR-007 |
| `release.go` | `DefaultReleasesURL`, `ReleasesURL` (FR-015 policy), `Release`, `Resolve`, `AssetName`, checksums parsing | FR-006, FR-008, FR-015 |
| `client.go` | `ProxySettings` (environment first, else stored; both spellings), `NewClient` (cloned default transport, `httpproxy` proxy, no HTTPS→HTTP redirects, ≤10 redirects), `get` with size limit and status check | FR-010, FR-012 |
| `fetch.go` | `Fetch`: hash while streaming to `stage/archive`, compare, unpack `omnistat`/`omnistat.exe` from tar.gz or zip, mode 0755, remove the archive | FR-009, FR-010 |
| `stage.go` | `Stage(dir)`: remove `.omnistat-upgrade-*` leftovers, `os.MkdirTemp(dir, ".omnistat-upgrade-")`, return path and cleanup | FR-011 |
| `stepaside.go` (+ `_windows`, `_other`) | `RunsFrom`, `StepAside`, `Aside.Finish` | FR-013a |
| `runner.go` | `Runner` interface (`Version`, `Install`) and `ExecRunner`: `Version` uses `exec.CommandContext` with a 30s timeout. `Install` uses `exec.Command` without a context, so Ctrl-C reaches the child from the terminal and the parent never kills it. It inherits stdin and the environment. | FR-009, FR-013 |

Limits: `checksums.txt` ≤ 1 MiB and 1 minute. The archive and the unpacked binary are
each ≤ 256 MiB, with 15 minutes for the archive (about 8 MiB today, so the limits leave
room for slow links, not for abuse).

### Backends
- `service.Installation` (new type in `internal/service/plan.go`): what upgrade needs
  to know.
- `systemd.Installation(ctx, h)`: `preconditions`, then `inspect` (foreign unit →
  `ErrForeignUnit`), then `readStored`. `Binary` = `BinaryPath`.
- `winsvc.Installation(ctx, h)`: `Elevated`, then `ProgramDir`, then `Service`, then
  `ours` (foreign → `ErrForeignService`). `Binary` = the registered binary, and
  `Settings` = `Env`.
- The CLI's `installer` interface gains `Installation`. Both adapters forward to it.

### Windows: upgrade run from the installed binary (FR-013a)
When upgrade runs from `C:\Program Files\omnistat\omnistat.exe`, that file is mapped
while the child's install runs `CopyBinary`, so the rename over it fails. The fix sits in
the **parent**, not in install. A fix in install would only help targets that contain
it, and a rollback to 0.3.0 would stop the service and then fail to replace the binary.

`upgrade.RunsFrom(self, installed, goos)` detects the case: Windows, same path
case-insensitively. `upgrade.StepAside(self, stage)` renames the running binary into the
staging directory. That is allowed for a running image, and the staging directory is on
the same volume. After the install, `Aside.Finish()` runs:
- when the installed path is empty (the install failed before copying), it renames the
  previous binary back;
- otherwise it schedules the moved file and the staging directory for deletion at
  restart (`MoveFileEx(DELAY_UNTIL_REBOOT)`, `stepaside_windows.go`; a no-op elsewhere).
  The next `Stage` removes them as well.

A dry-run never steps aside. The logic is unit-tested on any OS with plain files; the
Windows behaviour is verified in owner acceptance, by an upgrade run from the installed copy. `CopyBinary` is unchanged.

### CLI surface
- `omnistat upgrade [--check | --dry-run] [--version vX.Y.Z]`, added to usage.
- New `App` fields, for tests: `Runner upgrade.Runner` (nil means `upgrade.ExecRunner{}`)
  and `StageDir string` (empty means the installed binary's directory).
- `OMNISTAT_RELEASES_URL` is read through `getenv`, so tests point it at an `httptest`
  server over loopback HTTP.

## Package layout (delta)
| Package | Purpose | Justified by |
|---------|---------|--------------|
| `internal/upgrade` (new) | releases, download, verification, staging, running binaries | FR-005–FR-013, FR-015 |
| `internal/service` | `Installation` type | FR-003, FR-005, FR-012 |
| `internal/systemd` | `Installation` | FR-002, FR-003, FR-012 |
| `internal/winsvc` | `Installation` | FR-002, FR-003, FR-012 |
| `internal/cli` | `upgrade` command | FR-001–FR-017 |
| `scripts/e2e-mirror` (new, `package main`) | loopback static file server for the container acceptance | NFR-006 |

## Data flow
```
getenv, stored settings ──► ProxySettings ──► http.Client (HTTPS only, proxy, limits)
base URL ──► GET latest/download/checksums.txt (or download/<tag>/checksums.txt)
          ──► asset omnistat_<ver>_<os>_<arch>.<ext>, sha256, tag v<ver>
installed binary ──► `version` ──► compare ──► --check: exit 0/2 | up to date: exit 0
stage dir (next to installed binary, 0700) ◄── GET download/<tag>/<asset> (hash while writing)
          ──► sha256 == expected? ──► unpack omnistat[.exe] ──► `version` == target?
          ──► exec `<staged> service install [--dry-run]` (tty, env unchanged) ──► exit status
          ──► remove stage dir
```
No retries: a failed download is reported, and running again is the retry. The
operation is idempotent (NFR-003).

## Configuration
| Setting | Env var | Flag | Default | Requirement |
|---------|---------|------|---------|-------------|
| Target version | — | `--version` | latest stable | FR-006 |
| Releases location | `OMNISTAT_RELEASES_URL` | — | `https://github.com/omnismith-apps/omnistat/releases` | FR-015 |
| Proxy | `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` (either case) | — | the service's stored ones | FR-012 |

## Testing strategy
| Requirement | Test type | Where |
|-------------|-----------|-------|
| FR-001, FR-017 | CLI: flag conflict, `--check` exit statuses 0, 2 and 1 | `internal/cli/upgrade_test.go` |
| FR-002, FR-003 | backend units (not root/elevated, not installed, foreign) + CLI messages | `internal/systemd/installation_test.go`, `internal/winsvc/installation_test.go`, `internal/cli/upgrade_test.go` |
| FR-004 | CLI with `GOOS=darwin` | `internal/cli/upgrade_test.go` |
| FR-005, FR-007 | table: parse (dev, git describe, rc, v-less) and compare (SemVer pre-release order) | `internal/upgrade/version_test.go` |
| FR-006, FR-008 | httptest: latest, pinned, pre-release, 404, no asset for os/arch, malformed checksums | `internal/upgrade/release_test.go` |
| FR-009 | tar.gz and zip built in the test: good, hash mismatch, missing binary, oversized | `internal/upgrade/fetch_test.go` |
| FR-010, FR-015 | URL policy table; HTTPS→HTTP redirect refused (TLS httptest) | `internal/upgrade/release_test.go`, `client_test.go` |
| FR-011 | leftovers removed, 0700 (Unix), cleanup | `internal/upgrade/stage_test.go` |
| FR-012 | environment wins, stored fallback, lower-case, `NO_PROXY`, transport proxy for a non-loopback URL | `internal/upgrade/client_test.go` |
| FR-013 | fake runner gets `service install [--dry-run]`, its status is returned; real `ExecRunner` against a helper process (re-exec of the test binary) | `internal/cli/upgrade_test.go`, `internal/upgrade/runner_test.go` |
| FR-014, FR-016 | CLI output: summary, failure line, no token and no proxy password | `internal/cli/upgrade_test.go` |
| Windows flow | CLI with `winsvc` fakehost and `GOOS=windows` (zip asset) | `internal/cli/upgrade_test.go` |
| NFR-006 | container: from the v0.3.0 GitHub release to a local build via a loopback mirror; `--check`, dry-run, upgrade, idempotence, tampered checksum, rollback, non-loopback HTTP refused | `scripts/e2e-upgrade.sh` |

## Risks & unknowns
- **Windows step-aside** cannot be run locally (no Windows, no Wine). The Windows
  compile, vet and lint run locally; the unit tests run in CI. The behaviour is verified
  in owner acceptance, by an upgrade run from the installed copy.
- **The first real `latest` upgrade** needs a published release that contains this
  feature. Until then, acceptance uses `--version` with a release candidate, or the
  loopback mirror.
- **Supply chain**: checksums from the same origin do not authenticate a release
  (ADR-0013). The follow-up is signing.

## Decisions taken here that deserve an ADR
- **ADR-0013 (proposed)**: where upgrade gets a binary and what it trusts. That covers
  web URLs over the REST API, checksums without signatures for now, a mirror as trusted
  as GitHub, HTTPS-only, staging next to the installed binary, and handing over to the
  new binary's install.
