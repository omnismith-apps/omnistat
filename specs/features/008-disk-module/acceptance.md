# Acceptance runbook: the `disk` module (spec 008, NFR-005)

The owner runs this by hand: part A on their Fedora workstation, part B on their Windows
VM. The agent has already run:
- the sandbox acceptance (`TestSandbox_Disk`);
- the systemd container checks (`make e2e-systemd`: the readings work inside the
  service sandbox).

This run adds what those cannot show:
- values checked against the OS's own tools on a real host;
- a real disk under load;
- Windows as the 006 service account, which is the main open risk (plan "Risks").

Fill in the results table at the end and send it back. The agent records it in
`spec.md` under "Implementation notes".

About 30 minutes on Linux and 20 on Windows.

## Part A — Linux host

### A0. Before you start

- [ ] The local API is up: `curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8100/`
      prints a status (404 is fine).
- [ ] A build of this tree: `make build`.
- [ ] `iostat` is available (`iostat -V`). It is in the `sysstat` package.
- [ ] Note your layout: `lsblk -o NAME,TYPE,FSTYPE,MOUNTPOINTS` and `findmnt -no SOURCE,FSTYPE /`.
      Write down the **disk** under `/` (e.g. `nvme1n1`) and the partition or LVM
      volume in between (e.g. `nvme1n1p3`, `dm-0`).
- [ ] Settings in your shell: `set -a; . ./.env; set +a`.

### A1. Schema — US-5/2, FR-001

```bash
./bin/omnistat schema plan
```
Expect `no changes` if the sandbox test ran before. Otherwise expect exactly nine
`+ attribute disk_…` lines: eight metrics and `disk_root_total_gib (number)`. In the
Omnismith UI the host template shows all nine.

### A2. System volume against `df` — US-1/2, US-2/1, FR-006…FR-008

```bash
./bin/omnistat run --dry-run 2>/dev/null | grep 'disk\.root'
df -B1 --output=size,used,avail / | tail -1 |
  awk '{printf "df: total %.2f GiB  available %.2f GiB  used %.2f%%\n",
        int($1*100/2^30)/100, int($3*100/2^30)/100, $2*100/($2+$3)}'
```
Expect `disk_root_total_gib` and `disk_root_available_gib` to equal the `df` line, give
or take a hundredth for writes in between. Expect `disk_root_used_pct` to match `df`'s
percentage to two decimals (`df -h /` itself rounds up to a whole percent).

### A3. Inodes — US-4

On btrfs:
```bash
./bin/omnistat run --dry-run 2>&1 | grep -c 'no inode limit'     # 1
./bin/omnistat run --dry-run 2>/dev/null | grep -c inodes_used    # 0
```
On ext4 or XFS (for example a Debian or Ubuntu VM, if you have one), copy the binary
there and compare `disk.root_inodes_used_pct` with `df -i /`'s `IUse%`.

### A4. Throughput and operations against `iostat` — US-3/1, US-3/2, FR-011…FR-013

Three terminals.

1. `iostat -dmx 10`: every 10 s, per device, `rMB/s`, `wMB/s`, `r/s`, `w/s`, `%util`.
   (sysstat's MB is 2²⁰ bytes, the same MiB as omnistat.)
2. omnistat collecting every 10 s and printing each collection:
   ```bash
   printf 'modules:\n  disk:\n    interval: 10s\npublish:\n  interval: 10s\n' > /tmp/disk10.yaml
   ./bin/omnistat --config /tmp/disk10.yaml run --daemon --dry-run 2>/dev/null |
     grep -E 'disk\.(read|write|busy)'
   ```
3. A write that bypasses the page cache. Random data defeats btrfs compression:
   ```bash
   dd if=/dev/urandom of=$HOME/omnistat-dd.bin bs=4M count=1024 oflag=direct status=progress
   rm $HOME/omnistat-dd.bin
   ```

Compare a 10 s window in the middle of the write:
- `disk_write_mibps` ≈ `wMB/s` of **the disk** (e.g. `nvme1n1`), not the sum of the
  disk and its partition, and not the `dm-*` row. That is US-3/2: iostat shows the same
  I/O on each layer, and omnistat counts it once.
- `disk_write_iops` ≈ the disk's `w/s`.
- `disk_busy_pct` ≈ the highest `%util` among the whole disks.

The windows are not aligned to the second, so expect agreement within some percent
rather than to the digit. A value that is a multiple of iostat's (×2, ×3) is a failure.

### A5. As the service — NFR-003, NFR-005

Install as in spec 007 (or upgrade an existing install), then turn on debug:
```bash
sudo OMNISMITH_BASE_URL=http://localhost:8100 ./bin/omnistat service install
echo 'log: {level: debug}' | sudo tee -a /etc/omnistat/omnistat.yaml
sudo systemctl restart omnistat; sleep 70
journalctl -u omnistat -o cat | grep -E 'module=disk' | tail -5
journalctl -u omnistat -o cat | grep -c 'observations omitted.*module=disk'   # 0
```
Expect:
- `module scheduled module=disk interval=30s`;
- on btrfs, one `no inode limit … path=/`;
- `disk read module=disk path=/ devices=…` listing the same whole disks as A0 and no
  partitions;
- zero omission records.

In the Omnismith UI, the host entity's `disk_*` charts fill every 30 s. Leave the
service installed or `sudo omnistat service uninstall`, as you prefer.

## Part B — Windows VM

### B0. Before you start

- [ ] A pre-release build: push a tag such as `v0.3.0-rc.1`, and download and verify
      `omnistat_…_windows_amd64.zip` as in the spec 006 runbook, step 0.
- [ ] The 006 service installed from that build (an elevated
      `.\omnistat.exe service install` upgrades an existing one), with
      `log: {level: debug}` in `C:\ProgramData\omnistat\omnistat.yaml` and the service
      restarted (`Restart-Service omnistat`).
- [ ] An elevated and a normal PowerShell, both in the unzipped folder.

### B1. Startup: what Windows cannot collect — US-5/1, FR-016, FR-021

```powershell
Get-WinEvent -LogName Application -MaxEvents 60 |
  Where-Object { $_.ProviderName -eq 'omnistat' -and $_.Message -match 'disk' } |
  Format-List TimeCreated, Message
```
Expect:
- `module scheduled module=disk interval=30s`;
- two `attribute skipped: not collectable on this platform` records, for
  `busy_pct` and `root_inodes_used_pct`;
- `disk read module=disk path=C:\ devices=C:` (plus any other fixed drive letters);
- **no** `observations omitted … module=disk` record.

That last point is the virtual-account risk. If an omission names the read or write
keys with *access is denied*, stop here and send the message: T015 plans the fix
before anything else.

### B2. System volume against `Get-Volume` — US-1, US-2, FR-006, FR-008

```powershell
$v = Get-Volume -DriveLetter ($env:SystemDrive.TrimEnd(':'))
'{0:N2} GiB total, {1:N2} GiB free, {2:N2}% used' -f `
  ([math]::Floor($v.Size / 1GB * 100) / 100), ([math]::Floor($v.SizeRemaining / 1GB * 100) / 100), `
  (($v.Size - $v.SizeRemaining) / $v.Size * 100)
.\omnistat.exe run --dry-run 2>$null | Select-String 'disk\.root'
```
Expect the three `disk.root_*` values to match (PowerShell's `N2` rounds, omnistat
floors, so allow a hundredth), and no `root_inodes_used_pct` line.

### B3. Throughput against the performance counters — US-3/1, FR-011

In the normal window:
```powershell
Set-Content $env:TEMP\disk10.yaml "modules:`n  disk:`n    interval: 10s`npublish:`n  interval: 10s"
.\omnistat.exe --config $env:TEMP\disk10.yaml run --daemon --dry-run 2>$null | Select-String 'disk\.(read|write)'
```
In the elevated window, while copying a multi-GB file on `C:` (Explorer, or
`fsutil file createnew C:\omnistat-big.bin 4294967296` then `copy` it):
```powershell
Get-Counter '\LogicalDisk(C:)\Disk Write Bytes/sec','\LogicalDisk(C:)\Disk Writes/sec' -SampleInterval 10 -MaxSamples 6
```
Expect `disk_write_mibps` ≈ Write Bytes/sec ÷ 1 048 576, and `disk_write_iops` ≈
Writes/sec, both "within some percent" as in A4. With more than one fixed drive
letter, omnistat sums them, so compare with `\LogicalDisk(_Total)` instead. Delete the
test files afterwards.

## Results

| Step | Checks | Result | Notes |
|------|--------|--------|-------|
| A1 | nine attributes, 8 metric + 1 number | | |
| A2 | total / available / used % = `df` | | |
| A3 | btrfs: one notice, no inode value; ext4/XFS (optional): = `df -i` | | |
| A4 | write MiB/s, w/s, busy ≈ `iostat` for the disk; counted once | | |
| A5 | service: path `/`, disks listed, no omission, charts fill | | |
| B1 | busy and inodes skipped at startup; `path=C:\`; **no omission** | | |
| B2 | total / free / used % = `Get-Volume` | | |
| B3 | write MiB/s and writes/s ≈ `Get-Counter` | | |
