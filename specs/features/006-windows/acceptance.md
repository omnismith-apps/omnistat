# Acceptance runbook: omnistat on Windows (spec 006, T050)

The owner runs this by hand on their own `windows/amd64` VM (NFR-005). Each step has the
exact commands and what must be true. Fill in the results table at the end and send it
back. The agent records it in `spec.md` under "Implementation notes".

About 60–90 minutes, including two reboots.

## 0. Before you start

- [x] **A dedicated project** on the production Omnismith, not a client's project. It
      should be empty; the run creates omnistat's schema and one host entity.
- [x] **A throwaway token** for that project, allowed to write schema and entities. Step
      12 revokes it; step 13 needs a second one.
- [x] **The build under test.** Push a pre-release tag, e.g. `v0.1.0-rc.1`. The release
      workflow publishes it with the *Unreleased* changelog as notes. On the VM, download
      `omnistat_0.1.0-rc.1_windows_amd64.zip` and `checksums.txt` from the release, and
      unzip into `C:\Users\<you>\Downloads\omnistat`.
      ```powershell
      Get-FileHash .\omnistat_0.1.0-rc.1_windows_amd64.zip -Algorithm SHA256   # must match checksums.txt
      ```
- [x] **A standard (non-admin) local user** for step 7, created from an elevated prompt.
      Delete it at the end.
      ```powershell
      net user omnistd 'Choose-A-Passw0rd!' /add
      ```
- [x] Two PowerShell windows: one **normal**, one **elevated** (Run as administrator).
      Both `cd` to the unzipped folder.

**Typing the token.** Never write it into a command. Use
`$env:OMNISMITH_ACCESS_TOKEN = Read-Host 'token'`: PowerShell's history file then keeps
only that line, not the token. Where install asks for the token itself, paste it at its
hidden prompt.

## 1. Console commands (normal window) — US-7/1, US-2/1

```powershell
.\omnistat.exe version
$env:OMNISMITH_PROJECT_ID = '01a0c47a-8397-7406-b6e9-ce26508cd58e'
$env:OMNISMITH_ACCESS_TOKEN = Read-Host 'token'
.\omnistat.exe identity
.\omnistat.exe schema plan
.\omnistat.exe run --dry-run
```

Expect:
- `version` prints `omnistat v0.1.0-rc.1`.
- `identity` prints a 64-character hex identity with `source: windows-machine-guid`,
  then `(unresolved)` and "schema is not ready" (the project is still empty). Note the
  identity: it must never change during the run.
- `schema plan` lists the `hostname`, `machine_id`, `cpu_*`, `load_avg_*` and `mem_*`
  attributes and exits 2.
- `run --dry-run` prints values for `hostname`, `cpu_usage_pct`, `cpu_model`,
  `cpu_cores`, `cpu_arch`, `mem_used_pct`, `mem_available_mib` and `mem_total_mib`. It
  lists `load_avg_1/5/15` once under `not collectable on windows:`, not as failures.

## 2. Console daemon and stop keys — FR-024

Still in the normal window:

```powershell
.\omnistat.exe run --daemon
```
It applies the schema, creates the host entity and starts publishing.
1. Wait for a `msg=published` line, then press **Ctrl+C**. Expect `msg=stopped` after a
   final publish, and exit code 0 (`$LASTEXITCODE`).
2. Start it again and press **Ctrl+Break**. Expect the same.
3. Start it in a new window with `start powershell -ArgumentList '-NoExit','-Command','cd <folder>; .\omnistat.exe run --daemon'`
   (it inherits the variables from this window). Wait for `published`, then close that
   window with its **X**. You can't see the output. Instead, check in Omnismith that the
   host entity has a `cpu_usage_pct` point from just before you closed it. Windows allows
   a closed console about 5 seconds, so this may be cut short (spec edge case). Note what
   you see.

Then clear the token from this window:

```powershell
Remove-Item Env:OMNISMITH_ACCESS_TOKEN
```

## 3. Install needs elevation — US-1/4

Normal window:

```powershell
.\omnistat.exe service install; $LASTEXITCODE
```
Expect: a message saying to run it as administrator, and `1`. `Get-Service omnistat`
finds nothing.

## 4. Dry run — US-1/6, FR-028

Elevated window:

```powershell
$env:OMNISMITH_PROJECT_ID = '<project uuid>'
.\omnistat.exe service install --dry-run
```
It asks for the token without echoing it; paste the token. Expect:
- the identity (same as step 1), `entity: <uuid>` (created in step 2), binary
  `C:\Program Files\omnistat\omnistat.exe`, config
  `C:\ProgramData\omnistat\omnistat.yaml (not present — defaults apply)`, and settings
  `OMNISMITH_ACCESS_TOKEN, OMNISMITH_PROJECT_ID`;
- a list of `would …` lines;
- no token anywhere;
- `Get-Service omnistat` still finds nothing.

## 5. Install — US-1/1, US-1/2, FR-007–FR-011

```powershell
.\omnistat.exe service install
Get-Service omnistat
```
Paste the token at the prompt. Expect `done: …` lines ending with `service is running`
and `omnistat is installed and running`, then `Status: Running`.

## 6. What install registered — FR-008–FR-010, FR-015

Elevated window:

```powershell
sc.exe qc omnistat
sc.exe qfailure omnistat
sc.exe qfailureflag omnistat
(Get-Acl HKLM:\SYSTEM\CurrentControlSet\Services\omnistat).Access | Format-Table IdentityReference, RegistryRights, AccessControlType
(Get-Acl C:\ProgramData\omnistat).Access | Format-Table IdentityReference, FileSystemRights
(Get-Acl 'C:\Program Files\omnistat').Access | Format-Table IdentityReference, FileSystemRights
(Get-ItemProperty HKLM:\SYSTEM\CurrentControlSet\Services\omnistat).Environment | ForEach-Object { ($_ -split '=')[0] }
```
Expect:
- `qc`: `START_TYPE: 2 AUTO_START (DELAYED)`, `BINARY_PATH_NAME: "C:\Program Files\omnistat\omnistat.exe" run --daemon`
  and `SERVICE_START_NAME: NT SERVICE\omnistat`.
- `qfailure`: `RESET_PERIOD: 86400` and three `RESTART -- Delay = 60000 milliseconds`.
- `qfailureflag`: `FAILURE_ACTIONS_ON_NONCRASH_FAILURES: TRUE`.
- Service key ACL: `NT AUTHORITY\SYSTEM` and `BUILTIN\Administrators` with FullControl.
  `ALL APPLICATION PACKAGES` read entries may also appear; they grant nothing without a
  user grant (step 7 proves it). Nothing for Users or Authenticated Users.
- `C:\ProgramData\omnistat`: SYSTEM and Administrators with FullControl, Authenticated
  Users with ReadAndExecute. Nothing that lets Users write.
- `C:\Program Files\omnistat`: the usual Program Files entries. Users can read and
  execute, not write.
- The last command prints the setting **names** only. Don't send the values back.

## 7. A standard user cannot get in — FR-015, NFR-001

```powershell
runas /user:omnistd powershell
```
In the new window (as `omnistd`):

```powershell
reg query HKLM\SYSTEM\CurrentControlSet\Services\omnistat
sc.exe qc omnistat
Set-Content C:\ProgramData\omnistat\omnistat.yaml 'base_url: https://example.invalid'
New-Item -ItemType File C:\ProgramData\omnistat\probe.txt
Copy-Item C:\Windows\System32\notepad.exe 'C:\Program Files\omnistat\omnistat.exe'
```
Expect:
- `reg query`: **Access is denied**.
- `sc.exe qc`: works. It goes through the service manager, not the registry.
- The three writes: each **denied**.

Close the window.

## 8. Values in the project — FR-001, 004/005 Windows readings

Wait 2–3 minutes. In Omnismith, open the host entity (the `entity:` id from step 4).

| Attribute | Compare with |
|---|---|
| `hostname` | `hostname` |
| `machine_id` | the identity from step 1 |
| `cpu_model`, `cpu_cores`, `cpu_arch` | `Get-CimInstance Win32_Processor \| Select Name, NumberOfLogicalProcessors`; arch `amd64` |
| `cpu_usage_pct` (metric, every 10s) | Task Manager → Performance → CPU utilisation, roughly |
| `mem_total_mib` | `systeminfo \| findstr /C:"Total Physical Memory"` (MB) |
| `mem_available_mib`, `mem_used_pct` (metrics, every 30s) | Task Manager → Memory "Available" and "In use", roughly |
| `load_avg_1/5/15` | **no values** (not collected on Windows) |

## 9. Event Viewer — US-4/1, US-4/3, FR-025, FR-026

```powershell
Get-WinEvent -LogName Application -MaxEvents 30 -FilterXPath "*[System[Provider[@Name='omnistat']]]" |
  Format-List TimeCreated, LevelDisplayName, Message
```
Expect Information events whose message looks like a console line
(`time=… level=INFO msg=published dimensions=… observations=…`), including the
startup schedule, one `attribute skipped: not collectable on this platform` line per
`load_avg_*`, and an `identity source=windows-machine-guid` line.

Debug level through the service's config file:

```powershell
Set-Content C:\ProgramData\omnistat\omnistat.yaml "log:`n  level: debug"
Restart-Service omnistat
Start-Sleep 20
# run the Get-WinEvent command above again
```
Expect `level=DEBUG` lines, shown as Information events. Then remove the file and
restart the service:

```powershell
Remove-Item C:\ProgramData\omnistat\omnistat.yaml; Restart-Service omnistat
```

## 10. Stop publishes and is clean — US-3/1, FR-021

```powershell
Measure-Command { Stop-Service omnistat }
sc.exe query omnistat
Start-Service omnistat
```
Expect:
- `Stop-Service` completes in a few seconds, well under 16s (HTTP timeout + 1s).
- The last events before the stop show a final publish and `msg=stopped`.
- `sc query`: `STATE: 1 STOPPED`, `WIN32_EXIT_CODE: 0`.
- The service did **not** restart by itself.

## 11. A crash is restarted — US-3/2, FR-010

```powershell
Stop-Process -Name omnistat -Force
Start-Sleep 75
Get-Service omnistat
Get-WinEvent -LogName System -MaxEvents 5 -FilterXPath "*[System[Provider[@Name='Service Control Manager']]]" | Format-List TimeCreated, Message
```
Expect: `Running` again. The System log says the service terminated unexpectedly and
that a corrective action (restart) happens in 60000 ms.

## 12. A revoked token — US-4/2, FR-023

Revoke the token in Omnismith, then:

```powershell
Restart-Service omnistat
Start-Sleep 10
Get-Service omnistat
# run the Get-WinEvent command from step 9
Start-Sleep 70; Get-Service omnistat
```
Expect:
- An **Error** event that names the authorisation failure.
- The service stops, then is restarted about once a minute. Each attempt fails the
  same way.
- The Omnismith project is unchanged.

## 13. Rotate the token — US-5/2, FR-018

Create a second token, then in the elevated window:

```powershell
.\omnistat.exe service install --replace-token
```
Paste the new token. Expect `(update of the installed service)`, a running service and
new points in the project on the **same** entity.

## 14. Reboot with nobody logged in — US-1/3, US-2/2

Note the time and run `Restart-Computer`. **Don't log in** for 5 minutes. Watch the
entity in Omnismith from your own machine. Expect new `cpu_usage_pct` points about 2
minutes after boot (delayed start), on the same entity.

## 15. Upgrade by installing again — US-5/1, US-5/3, FR-017

Use a second build if you have one (e.g. `v0.1.0-rc.2`). Otherwise unzip the same
archive into another folder: the replace path is identical.

```powershell
cd <new folder>
.\omnistat.exe service install
& 'C:\Program Files\omnistat\omnistat.exe' version
```
Expect:
- No token prompt.
- `(update of the installed service)`, then `stop the running service`,
  `replace C:\Program Files\omnistat\omnistat.exe …` and a running service.
- `version` shows the new build.
- The same entity keeps receiving points.

**Proxy is honoured.** Point it at a dead proxy:

```powershell
$env:HTTPS_PROXY = 'http://127.0.0.1:9'
.\omnistat.exe service install; $LASTEXITCODE
Remove-Item Env:HTTPS_PROXY
```
Expect: install **fails** in its pre-check with a connection error through the proxy,
exit `1`, and nothing changes; the service keeps running. If the client network has a
real proxy, also do a successful install with it set.

## 16. Uninstall, then install again — US-6, US-2/2, FR-020

```powershell
& 'C:\Program Files\omnistat\omnistat.exe' service uninstall --dry-run
& 'C:\Program Files\omnistat\omnistat.exe' service uninstall
```
Expect:
- The dry run lists stop / remove service / remove event source / remove binary, then
  `would keep C:\ProgramData\omnistat`.
- The real run shows `done:` for each step and a note that the running program was
  moved to `C:\Windows\Temp\omnistat-uninstalled-<pid>.exe`, which Windows deletes at the
  next restart, and that `C:\Program Files\omnistat` is removed. The folder is gone
  **immediately**, with no reboot needed.
- **Reinstall before rebooting:** run `.\omnistat.exe service install` from the download
  folder, then `Restart-Computer`. After the reboot the service must be **running**. A
  pending deletion must not have removed the new binary.
- It ends with "the host entity remains".

```powershell
Get-Service omnistat                                   # not found
Restart-Computer
# after the reboot, elevated:
Test-Path 'C:\Program Files\omnistat'                  # False
Test-Path C:\ProgramData\omnistat                      # True
Get-WinEvent -ListProvider omnistat                    # not found
cd <download folder>; .\omnistat.exe service uninstall; $LASTEXITCODE   # "not installed", 0
.\omnistat.exe service install                         # token prompt again: the stored one went with the service
```
Expect: after reinstalling, the report shows the **same entity** id as before. Finish
with `.\omnistat.exe service uninstall` if the VM is not kept, and remove the test user
with `net user omnistd /delete`.

## 17. Amendments by spec 007 (next rc) — 007 FR-014, FR-017, NFR-006

Run on a VM where omnistat is not installed, in the elevated window, with **no**
`OMNISMITH_*` variables set (open a new window). If `C:\ProgramData\omnistat\omnistat.yaml`
exists from an earlier run, rename it first.

```powershell
.\omnistat.exe service install --dry-run
```
Expect: install **asks for the project id** (with echo), then the token (hidden). The
summary shows `config: C:\ProgramData\omnistat\omnistat.yaml (not present — install
creates a commented starter…)`, and the steps include `would create
C:\ProgramData\omnistat\omnistat.yaml: a commented starter configuration`.

```powershell
.\omnistat.exe service install
Get-Content C:\ProgramData\omnistat\omnistat.yaml -TotalCount 12
icacls C:\ProgramData\omnistat\omnistat.yaml
```
Expect: installed and running; the file starts with `# omnistat configuration`, every
setting commented out; its ACL is the directory's (SYSTEM and Administrators full,
Authenticated Users read), so the standard user of step 7 cannot write it. Then add a
line and install again:

```powershell
Add-Content C:\ProgramData\omnistat\omnistat.yaml 'log: {level: debug}'
.\omnistat.exe service install
Get-Content C:\ProgramData\omnistat\omnistat.yaml -Tail 1      # still your line
```
Expect: no project or token prompt (both stored), no "create …omnistat.yaml" step, your
line kept, and debug events in the Application log after the restart.

## Results

Mark each row ✅ / ❌ / ⚠️ and add a note for anything that is not ✅.

| Step | Checks | Result | Notes |
|---|---|---|---|
| 1 | console commands, identity source, load_avg skipped | | |
| 2 | Ctrl+C / Ctrl+Break / window close | | |
| 3 | not elevated → refused | | |
| 4 | dry run, no token shown, nothing changed | | |
| 5 | install, hidden prompt, running | | |
| 6 | qc / qfailure / qfailureflag / ACLs | | |
| 7 | standard user denied (registry, config, binary) | | |
| 8 | values match the OS | | |
| 9 | Event Viewer, debug level via config | | |
| 10 | stop: final publish, clean, fast | | |
| 11 | kill → restarted after 60s | | |
| 12 | revoked token → error events, retries | | |
| 13 | --replace-token | | |
| 14 | reboot, nobody logged in | | |
| 15 | upgrade, no prompt; dead proxy → refused | | |
| 16 | uninstall, deferred delete, reinstall → same entity | | |
| 17 | 007: project prompt; starter config created once, ACL inherited, kept | | |

**Send back:**
- this table;
- the full console output of steps 4, 5, 15 and 16 (it contains no secrets by design;
  check anyway);
- the Event Viewer output of step 12;
- anything that surprised you.

Do **not** send the `Environment` values or any token.
