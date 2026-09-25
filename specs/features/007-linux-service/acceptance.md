# Acceptance runbook: omnistat as a systemd service (spec 007, NFR-005/2)

The owner runs this by hand, with `sudo`, on their Fedora workstation, against the
local development API and its test project from `.env`. The agent has already run the
same scenarios in disposable containers (NFR-005/1; results in `spec.md`). This run adds
what containers cannot show: a real host, SELinux labels, a real login user, and
optionally a real reboot. Fill in the results table at the end and send it back.

About 20 minutes, plus an optional reboot. At the end the host is as it was, apart from
`/etc/omnistat` (step 12 removes that too, if you want).

## 0. Before you start

- [ ] The local API is up: `curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8100/`
      prints a status (404 is fine).
- [ ] A static build: `make build`, then `file bin/omnistat` says *statically linked*.
- [ ] Two terminals in the repo root: yours (a normal user) and one where you run the
      `sudo` commands. Nothing else of yours is named `omnistat.service`
      (`systemctl status omnistat` says *could not be found*).

**Typing the token.** Paste it at install's hidden prompt; never put it in a command.
The project id is not a secret: install asks for it, or pass it as below.

## 1. Not root → refused — US-1/4, FR-002

```bash
./bin/omnistat service install; echo "exit $?"
```
Expect: `needs root: run it with sudo`, exit `1`, nothing created (`ls /etc/omnistat`
fails).

## 2. Dry run — US-1/6, FR-024

```bash
sudo OMNISMITH_BASE_URL=http://localhost:8100 ./bin/omnistat service install --dry-run
```
Answer the project id (from `.env`) and paste the token. Expect:
- the identity (`linux-machine-id`) and entity lines;
- `would install … as /usr/local/bin/omnistat (root, 0755)`, `would create /etc/omnistat`,
  `would create /etc/omnistat/omnistat.yaml: a commented starter…`,
  `would write /etc/omnistat/omnistat.env (root, 0600…): OMNISMITH_ACCESS_TOKEN,
  OMNISMITH_BASE_URL, OMNISMITH_PROJECT_ID`;
- the **complete unit**, indented, starting with `# Written by \`omnistat service install\``;
- `daemon-reload`, `enable`, `start`;
- no token anywhere, and still nothing created.

## 3. Install — US-1/1, FR-005–FR-015

```bash
sudo OMNISMITH_BASE_URL=http://localhost:8100 ./bin/omnistat service install; echo "exit $?"
```
Expect: the same prompts, `done:` for each step, `service is active (running)`,
`omnistat is installed and running`, `Logs: journalctl -u omnistat …`, exit `0`.

## 4. What install created — contract, FR-006–FR-009, NFR-002, SELinux edge case

```bash
ls -l /usr/local/bin/omnistat /etc/omnistat /etc/systemd/system/omnistat.service
ls -Z /usr/local/bin/omnistat /etc/omnistat/omnistat.env /etc/systemd/system/omnistat.service
systemctl is-enabled omnistat; systemctl status omnistat --no-pager | head -5
systemd-analyze security omnistat | tail -1
```
Expect:
- modes and owners: `root 755`, `root 644` (yaml), `root 600` (env), `root 644` (unit);
- SELinux types: `bin_t` for the binary, `etc_t` for the env file, `systemd_unit_file_t`
  for the unit (never `user_home_t`, the label of where the binary came from);
- `enabled`, `active (running)`, with the status line naming `omnistat.service`;
- exposure `1.1 OK` or better (NFR-002).

## 5. Least privilege — US-2/1, FR-008

```bash
PID=$(systemctl show -p MainPID --value omnistat)
ps -o user,uid,cmd -p "$PID"
grep -E '^(CapEff|NoNewPrivs)' /proc/$PID/status
```
Expect: a user that is not root and not in `/etc/passwd` (named `omnistat` or a numeric
uid 61184–65519), `CapEff: 0000000000000000`, `NoNewPrivs: 1`.

## 6. Your normal user cannot get in — NFR-001

In **your** terminal (not sudo):

```bash
cat /etc/omnistat/omnistat.env                                   # Permission denied
cat /proc/$(systemctl show -p MainPID --value omnistat)/environ  # Permission denied
systemctl show omnistat | grep -c omni_                          # 0
echo x >> /etc/omnistat/omnistat.yaml                            # Permission denied
touch /etc/omnistat/new                                          # Permission denied
echo x >> /usr/local/bin/omnistat                                # Permission denied
echo x >> /etc/systemd/system/omnistat.service                   # Permission denied
```

## 7. It publishes, and the journal has priorities — US-2/3, US-4, FR-022

```bash
journalctl -u omnistat --no-pager | tail -20
journalctl -u omnistat -o verbose --no-pager | grep -E '^ +(PRIORITY|MESSAGE)=' | head -8
set -a; . ./.env; set +a; ./bin/omnistat identity
```
Expect:
- `module scheduled`, `publish scheduled`, `msg=published`;
- `PRIORITY=6` on those (`PRIORITY=4`/`3` would only appear for warnings and errors);
- `identity` in your terminal prints the same identity and the **existing** entity id
  (not "would create"): the service created it. Compare `mem_total_mib`/`cpu_cores` in
  the project with `free -m` and `nproc`.

## 8. Stop, crash, revoked token — US-3, FR-009, FR-023

```bash
time sudo systemctl stop omnistat
systemctl show -p Result --value omnistat; journalctl -u omnistat -n 3 -o cat --no-pager
```
Expect: the stop takes well under a second, `success`, and the last lines show the final
publish and `msg=stopped`.

```bash
sudo systemctl start omnistat && sudo systemctl kill -s KILL omnistat
sleep 2; systemctl show -p SubState --value omnistat        # auto-restart
sleep 65; systemctl is-active omnistat                      # active
```

Revoked token (simulated with a wrong one), then repaired by installing again:

```bash
sudo sed -i "s/^OMNISMITH_ACCESS_TOKEN=.*/OMNISMITH_ACCESS_TOKEN='omni_revoked'/" /etc/omnistat/omnistat.env
sudo systemctl restart omnistat; echo "exit $?"             # fails, exit ≠ 0
journalctl -u omnistat -p err -n 3 -o cat --no-pager        # the reason, at priority err
sudo ./bin/omnistat service install --replace-token         # paste the real token; running again
```

## 9. Configure, and a drop-in — FR-012, FR-014, US-5/4

```bash
head -20 /etc/omnistat/omnistat.yaml
echo 'log: {level: debug}' | sudo tee -a /etc/omnistat/omnistat.yaml
./bin/omnistat --config /etc/omnistat/omnistat.yaml schema plan    # still valid
sudo systemctl edit omnistat     # add:  [Service]  Environment=OMNISTAT_ACCEPTANCE=1
sudo systemctl restart omnistat
journalctl -u omnistat -p debug -n 5 -o verbose --no-pager | grep -c 'PRIORITY=7'   # > 0
```

## 10. Upgrade by installing again — US-5/1, US-5/3, FR-018

```bash
mkdir -p /tmp/omnistat-next && cp bin/omnistat /tmp/omnistat-next/
sudo NO_PROXY=example.invalid /tmp/omnistat-next/omnistat service install
sudo cat /etc/omnistat/omnistat.env | sed 's/=.*/=…/'        # names only
tail -1 /etc/omnistat/omnistat.yaml; ls /etc/systemd/system/omnistat.service.d/
```
Expect: no prompt, `(update of the installed service)`, `stop the running service`,
`replace /usr/local/bin/omnistat with /tmp/omnistat-next/omnistat`, running; the names
now include `NO_PROXY`; your `log: {level: debug}` line and your drop-in are still there;
`./bin/omnistat identity` shows the same entity.

## 11. Reboot (optional) — US-1/3

Only if convenient. After the reboot, without logging in to a desktop session first if
you can (SSH or a text console):

```bash
systemctl is-active omnistat; journalctl -u omnistat -b --no-pager | head -5
```
Expect: `active`, started after `network-online.target`, publishing; same entity.

## 12. Uninstall — US-6, FR-020, FR-021

```bash
sudo /usr/local/bin/omnistat service uninstall --dry-run
sudo /usr/local/bin/omnistat service uninstall; echo "exit $?"
ls /usr/local/bin/omnistat /etc/omnistat/omnistat.env /etc/systemd/system/omnistat.service
ls /etc/omnistat /etc/systemd/system/omnistat.service.d
sudo ./bin/omnistat service uninstall                          # not installed; nothing to do
```
Expect: stop, disable, reset-failed, remove the env file (with the token), the binary and
the unit, daemon-reload; `kept /etc/omnistat with omnistat.yaml` and your drop-in;
"the host entity remains"; the three removed paths are gone.

To leave the host exactly as before:

```bash
sudo rm -r /etc/omnistat /etc/systemd/system/omnistat.service.d && sudo systemctl daemon-reload
```

## Results

Mark each row ✅ / ❌ / ⚠️ and add a note for anything that is not ✅.

| Step | Checks | Result | Notes |
|---|---|---|---|
| 1 | not root → refused | | |
| 2 | dry run: unit shown, no token, nothing created | | |
| 3 | install with prompts, running | | |
| 4 | modes, SELinux labels, enabled, exposure score | | |
| 5 | dynamic user, no capabilities | | |
| 6 | your user denied (env, environ, show, config, binary, unit) | | |
| 7 | publishes; journal priorities; same entity via `identity` | | |
| 8 | stop clean; kill → restart after 60s; wrong token → err, repaired | | |
| 9 | starter config valid; debug at priority 7; drop-in | | |
| 10 | upgrade: no prompt, merged settings, config and drop-in kept | | |
| 11 | reboot (optional) | | |
| 12 | uninstall; only `/etc/omnistat` and the drop-in left | | |

**Send back:** this table, the output of steps 2, 3, 10 and 12 (no secrets by design;
check anyway), the exposure line of step 4, and anything that surprised you. Never send
the content of `omnistat.env`.
