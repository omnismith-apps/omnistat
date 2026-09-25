#!/usr/bin/env bash
# Container acceptance of `omnistat service install|uninstall` on systemd
# (spec 007 NFR-005/1). Manual and opt-in: it needs Docker, a reachable
# Omnismith API and a throwaway project. Not part of `make all`.
#
#   scripts/e2e-systemd.sh [distro ...]     # default: every distro below
#
# Settings come from the environment, or from ./.env when unset there:
# OMNISMITH_ACCESS_TOKEN, OMNISMITH_PROJECT_ID and OMNISMITH_BASE_URL. A base
# URL on localhost is rewritten to host.docker.internal, the host as the
# container sees it. The token reaches the container only through
# `docker exec -e NAME` (by name, never on a command line) and is never printed.
#
# Each distro runs in a disposable container that boots systemd. It must be
# privileged for systemd, DynamicUser, namespaces and seccomp to work, so it is
# kept away from the host: bridge network (network sysctls stay in its
# namespace), every unit that could touch the shared kernel, devices or clock
# masked, removed at the end (E2E_KEEP=1 keeps it for inspection).
set -uo pipefail

ALL_DISTROS="fedora44 debian12 ubuntu2404 rocky9 rocky8"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/bin/omnistat"

if [[ -f "$ROOT/.env" ]]; then
	while IFS='=' read -r k v; do
		[[ "$k" =~ ^OMNISMITH_(ACCESS_TOKEN|PROJECT_ID|BASE_URL)$ ]] || continue
		[[ -n "${!k:-}" ]] || export "$k=$v"
	done <"$ROOT/.env"
fi
: "${OMNISMITH_ACCESS_TOKEN:?set it or put it in .env}" "${OMNISMITH_PROJECT_ID:?}" "${OMNISMITH_BASE_URL:?}"
E2E_BASE_URL=$(sed -E 's#//(localhost|127\.0\.0\.1|0\.0\.0\.0)([:/]|$)#//host.docker.internal\2#' <<<"$OMNISMITH_BASE_URL")
export OMNISMITH_ACCESS_TOKEN OMNISMITH_PROJECT_ID E2E_BASE_URL
[[ -x "$BIN" ]] || { echo "build first: make build" >&2; exit 1; }

# Units that would reach the shared kernel, devices or clock of a privileged
# container, or have no business in one.
MASK="systemd-sysctl.service systemd-udevd.service systemd-udevd-control.socket systemd-udevd-kernel.socket
systemd-udev-trigger.service systemd-udev-settle.service systemd-modules-load.service systemd-binfmt.service
proc-sys-fs-binfmt_misc.automount proc-sys-fs-binfmt_misc.mount systemd-timesyncd.service chronyd.service
systemd-remount-fs.service getty.target console-getty.service systemd-hwdb-update.service systemd-pstore.service
systemd-oomd.service systemd-oomd.socket systemd-journald-audit.socket systemd-firstboot.service
sys-kernel-debug.mount sys-kernel-tracing.mount sys-kernel-config.mount sys-fs-fuse-connections.mount
dev-hugepages.mount dev-mqueue.mount systemd-random-seed.service kmod-static-nodes.service
systemd-tmpfiles-setup-dev.service systemd-tmpfiles-setup-dev-early.service ldconfig.service
systemd-logind.service systemd-networkd-wait-online.service NetworkManager-wait-online.service
systemd-resolved.service auditd.service"

dockerfile() {
	local base install
	case "$1" in
	fedora44) base=fedora:44 install="dnf -y install systemd procps-ng util-linux util-linux-script && dnf clean all" ;;
	debian12) base=debian:12 install="apt-get update && apt-get install -y --no-install-recommends systemd systemd-sysv procps util-linux && rm -rf /var/lib/apt/lists/*" ;;
	ubuntu2404) base=ubuntu:24.04 install="apt-get update && apt-get install -y --no-install-recommends systemd systemd-sysv procps util-linux && rm -rf /var/lib/apt/lists/*" ;;
	rocky9) base=rockylinux:9 install="dnf -y install systemd procps-ng util-linux && dnf clean all" ;;
	rocky8) base=rockylinux:8 install="dnf -y install systemd procps-ng util-linux && dnf clean all" ;;
	*) echo "unknown distro $1 (known: $ALL_DISTROS)" >&2; return 1 ;;
	esac
	cat <<EOF
FROM $base
RUN $install
RUN systemctl mask $(tr '\n' ' ' <<<"$MASK") && systemctl set-default multi-user.target \
 && useradd -m alice && rm -f /etc/machine-id && touch /etc/machine-id
ENV container=docker
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
EOF
}

pass=0 fail=0 failed=() notes=()
ok() { pass=$((pass + 1)); printf '  PASS %s\n' "$1"; }
ko() {
	fail=$((fail + 1)); failed+=("$DISTRO: $1"); printf '  FAIL %s\n' "$1"
	[[ -z "${2:-}" ]] || sed 's/^/       | /' <<<"$2" | tail -30
}
check() { # check "name" cmd... — PASS when cmd succeeds
	local name=$1 out
	shift
	if out=$("$@" 2>&1); then ok "$name"; else ko "$name" "$out"; fi
}
has() { if grep -qE -- "$2" <<<"$3"; then ok "$1"; else ko "$1" "$3"; fi; }         # has "name" regex text
secret_free() { if grep -qF -- "$OMNISMITH_ACCESS_TOKEN" <<<"$2"; then ko "$1" "(a setting's value was printed; output withheld)"; else ok "$1"; fi; }
in_c() { docker exec "$C" "$@"; }
# with_api: run cmd in the container with the API settings (passed by name).
with_api() { docker exec -e OMNISMITH_ACCESS_TOKEN -e OMNISMITH_PROJECT_ID -e OMNISMITH_BASE_URL="$E2E_BASE_URL" "$C" "$@"; }
state() { in_c systemctl is-active omnistat 2>/dev/null; }
prop() { in_c systemctl show -p "$1" --value omnistat; }
wait_state() { # wait_state state seconds
	local i
	for ((i = 0; i < $2 * 2; i++)); do
		[[ "$(state)" == "$1" ]] && return 0
		sleep 0.5
	done
	echo "omnistat is $(state)/$(prop SubState), not $1 after $2s"
	return 1
}
# typed: run cmd on a pseudo-terminal, as sudo leaves it — no OMNISMITH_* in its
# environment but the base URL — and type the values of the named container
# variables, each after a pause, so a secret reaches a prompt with echo off.
# printf is a shell builtin: the values never appear in a process list.
typed() {
	docker exec -e OMNISMITH_ACCESS_TOKEN -e OMNISMITH_PROJECT_ID -e OMNISMITH_BASE_URL="$E2E_BASE_URL" "$C" sh -c '
		(for v in $1; do sleep 2; eval "printf \"%s\\n\" \"\$$v\""; done; sleep 20) |
			env -u OMNISMITH_ACCESS_TOKEN -u OMNISMITH_PROJECT_ID script -qec "$2" /dev/null' sh "$1" "$2"
}
identity() { # the host entity id, as the service's own settings resolve it
	with_api sh -c 'set -a; . /etc/omnistat/omnistat.env; set +a; /usr/local/bin/omnistat --config /etc/omnistat/omnistat.yaml identity --json' 2>/dev/null |
		sed -n 's/.*"entity_id": *"\([0-9a-f-]*\)".*/\1/p'
}

run_distro() {
	DISTRO=$1
	C=omnistat-e2e-$DISTRO
	echo "== $DISTRO"
	dockerfile "$DISTRO" | docker build -q -t "$C" - >/dev/null || { ko "image build"; return; }
	docker rm -f "$C" >/dev/null 2>&1
	docker run -d --name "$C" --privileged --cgroupns=private --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
		--add-host host.docker.internal:host-gateway "$C" >/dev/null || { ko "container start"; return; }
	local i st out pid id1 id2
	for ((i = 0; i < 60; i++)); do
		st=$(in_c systemctl is-system-running 2>/dev/null)
		[[ "$st" == running || "$st" == degraded ]] && break
		sleep 1
	done
	in_c systemd-machine-id-setup --commit >/dev/null 2>&1 # keep the identity across the reboot below
	echo "   $(in_c sh -c 'systemctl --version | head -1'); system $st"
	in_c mkdir -p /opt/dl /opt/dl2
	docker cp -q "$BIN" "$C:/opt/dl/omnistat"
	docker cp -q "$BIN" "$C:/opt/dl2/omnistat"

	out=$(with_api /opt/dl/omnistat schema plan 2>&1)
	has "API reachable from the container" "changes|to create|no changes" "$out"

	# FR-002: not root → refused, nothing changed.
	out=$(docker exec -u alice "$C" /opt/dl/omnistat service install 2>&1)
	has "not root: refused with the sudo hint (FR-002)" "run it with sudo" "$out"

	# US-1/6, FR-024: the dry-run shows the unit, changes nothing and prints no value.
	out=$(with_api /opt/dl/omnistat service install --dry-run 2>&1)
	has "dry-run shows the full unit (US-1/6)" "      DynamicUser=yes" "$out"
	secret_free "dry-run prints no setting value (FR-016)" "$out"
	check "dry-run changed nothing (FR-024)" in_c sh -c '! test -e /etc/omnistat && ! test -e /etc/systemd/system/omnistat.service && ! test -e /usr/local/bin/omnistat'

	# US-1/1, FR-015, FR-017: install as after `sudo`, typing the project id and the token.
	out=$(typed "OMNISMITH_PROJECT_ID OMNISMITH_ACCESS_TOKEN" "/opt/dl/omnistat service install" 2>&1)
	has "install with prompts: installed and running (US-1/1)" "installed and running" "$out"
	secret_free "install prints no setting value (FR-016)" "$out"
	if [[ "$(state)" != active ]]; then
		ko "service active after install" "$(in_c journalctl -u omnistat --no-pager -o cat | tail -20)"
		return
	fi
	check "enabled and active (FR-007)" sh -c "[ \"\$(docker exec $C systemctl is-enabled omnistat)\" = enabled ]"
	check "files: owners and modes (contract)" in_c sh -c '
		[ "$(stat -c "%U %a" /usr/local/bin/omnistat)" = "root 755" ] &&
		[ "$(stat -c "%U %a" /etc/omnistat)" = "root 755" ] &&
		[ "$(stat -c "%U %a" /etc/omnistat/omnistat.yaml)" = "root 644" ] &&
		[ "$(stat -c "%U %a" /etc/omnistat/omnistat.env)" = "root 600" ] &&
		[ "$(stat -c "%U %a" /etc/systemd/system/omnistat.service)" = "root 644" ] &&
		grep -q "^# project_id:" /etc/omnistat/omnistat.yaml'

	# US-2/1, FR-008: dynamic unprivileged user, no capabilities, no new privileges, read-only system.
	pid=$(prop MainPID)
	check "runs as a dynamic user, not root (FR-008)" in_c sh -c "u=\$(stat -c %u /proc/$pid); [ \"\$u\" != 0 ] && ! grep -q \":x:\$u:\" /etc/passwd"
	check "no capabilities, no new privileges (FR-008)" in_c sh -c "grep -q '^CapEff:[[:space:]]*0000000000000000' /proc/$pid/status && grep -q '^NoNewPrivs:[[:space:]]*1' /proc/$pid/status"
	check "the service sees a read-only system (FR-008)" in_c sh -c "! nsenter -t $pid -m touch /etc/omnistat-e2e /usr/omnistat-e2e /var/omnistat-e2e 2>/dev/null"

	# US-2/3: it publishes; the host entity exists, found with the service's own settings.
	sleep 3
	out=$(in_c journalctl -u omnistat --no-pager -o cat)
	has "published (first publish logged at info)" "msg=published" "$out"
	id1=$(identity)
	check "host entity exists in the project (US-2/3)" test -n "$id1"

	# US-4, FR-022: journal priorities.
	out=$(in_c journalctl -u omnistat --no-pager -o json)
	has "journal: a record at priority info (FR-022)" '"PRIORITY" *: *"6".*msg=published|msg=published.*"PRIORITY" *: *"6"' "$out"

	# NFR-001: another user can neither read the settings nor change what the service runs or reads.
	check "alice cannot read omnistat.env (NFR-001)" docker exec -u alice "$C" sh -c '! cat /etc/omnistat/omnistat.env 2>/dev/null'
	check "alice cannot read the service's environment (NFR-001)" docker exec -u alice "$C" sh -c "! cat /proc/$pid/environ 2>/dev/null"
	out=$(docker exec -u alice "$C" systemctl show omnistat 2>&1)
	secret_free "systemctl show reveals no setting value (NFR-001)" "$out"
	check "alice cannot change binary, unit or config (NFR-001)" docker exec -u alice "$C" sh -c '
		! sh -c "echo x >> /usr/local/bin/omnistat" 2>/dev/null &&
		! sh -c "echo x >> /etc/systemd/system/omnistat.service" 2>/dev/null &&
		! sh -c "echo x >> /etc/omnistat/omnistat.yaml" 2>/dev/null &&
		! touch /etc/omnistat/new 2>/dev/null'

	# NFR-002: exposure (systemd-analyze security needs systemd 240+).
	out=$(in_c systemd-analyze security omnistat 2>&1 | tail -1)
	if grep -q "Overall exposure" <<<"$out"; then
		echo "   $out"
		has "exposure rated OK or better (NFR-002)" "(OK|SAFE)" "$out"
	else
		notes+=("$DISTRO: systemd-analyze security unavailable")
		echo "   (systemd-analyze security unavailable here)"
	fi

	# US-3/1, FR-023: a stop publishes what is buffered and is clean.
	in_c systemctl stop omnistat
	check "stop is clean: inactive, result success (US-3/1)" sh -c "[ \"\$(docker exec $C systemctl is-active omnistat)\" = inactive ] && [ \"\$(docker exec $C systemctl show -p Result --value omnistat)\" = success ]"
	out=$(in_c journalctl -u omnistat --no-pager -o cat -n 5)
	has "stop: final publish, then stopped (US-3/1)" "msg=stopped" "$out"

	# US-3/2, FR-009: after a kill, systemd restarts it one minute later.
	in_c systemctl start omnistat
	in_c systemctl kill -s KILL omnistat
	sleep 2
	check "killed: waiting to restart (FR-009)" sh -c "docker exec $C systemctl show -p SubState --value omnistat | grep -q auto-restart"
	check "killed: running again after a minute (FR-009)" wait_state active 75

	# US-3/3: a revoked token → the start fails, the reason at err, a retry later.
	in_c sed -i "s/^OMNISMITH_ACCESS_TOKEN=.*/OMNISMITH_ACCESS_TOKEN='omni_revoked_e2e'/" /etc/omnistat/omnistat.env
	check "revoked token: systemctl restart fails (US-3/3)" sh -c "! docker exec $C systemctl restart omnistat 2>/dev/null"
	check "revoked token: retried later (FR-009)" sh -c "docker exec $C systemctl show -p SubState --value omnistat | grep -q auto-restart"
	out=$(in_c journalctl -u omnistat --no-pager -p err -o cat -n 10)
	has "revoked token: the reason at priority err (US-4/2)" "omnistat" "$out"

	# US-5: install again from another path, with the token set (replacing the revoked one)
	# and a new NO_PROXY; a drop-in and an edited config stay; no prompt (no terminal here).
	in_c sh -c 'mkdir -p /etc/systemd/system/omnistat.service.d && printf "[Service]\nEnvironment=OMNISTAT_E2E_DROPIN=1\n" > /etc/systemd/system/omnistat.service.d/override.conf && echo "log: {level: debug}" >> /etc/omnistat/omnistat.yaml'
	out=$(docker exec -e OMNISMITH_ACCESS_TOKEN -e NO_PROXY=example.invalid -e OMNISMITH_BASE_URL="$E2E_BASE_URL" "$C" /opt/dl2/omnistat service install 2>&1)
	has "upgrade: updated and running (US-5/1)" "update of the installed service" "$out"
	has "upgrade: binary replaced (US-5/1)" "replace /usr/local/bin/omnistat with /opt/dl2/omnistat" "$out"
	secret_free "upgrade prints no setting value" "$out"
	check "upgrade: config and drop-in kept (US-5/4)" in_c sh -c 'grep -q "level: debug" /etc/omnistat/omnistat.yaml && test -f /etc/systemd/system/omnistat.service.d/override.conf'
	check "upgrade: settings merged (US-5/3)" in_c sh -c 'grep -q "^NO_PROXY=" /etc/omnistat/omnistat.env && grep -q "^OMNISMITH_PROJECT_ID=" /etc/omnistat/omnistat.env && ! grep -q omni_revoked_e2e /etc/omnistat/omnistat.env'
	pid=$(prop MainPID)
	check "upgrade: the drop-in applies (US-5/4)" in_c sh -c "tr '\\0' '\\n' </proc/$pid/environ | grep -q OMNISTAT_E2E_DROPIN=1"
	sleep 2
	out=$(in_c journalctl -u omnistat --no-pager -o json)
	has "debug level from the config reaches the journal at debug (FR-022)" '"PRIORITY" *: *"7"' "$out"
	check "same host entity after the upgrade (US-5/1)" test "$(identity)" = "$id1"

	# US-5/2: --replace-token asks for the token.
	out=$(typed "OMNISMITH_ACCESS_TOKEN" "/usr/local/bin/omnistat service install --replace-token" 2>&1)
	has "--replace-token: asked for the token (US-5/2)" "access token \(input hidden\)" "$out"
	has "--replace-token: installed and running (US-5/2)" "installed and running" "$out"
	secret_free "--replace-token prints no setting value" "$out"

	# Reboot: the container restarts, systemd boots, the service starts by itself.
	docker restart "$C" >/dev/null
	check "reboot: started at boot, nobody logged in (US-1/3)" wait_state active 60
	id2=$(identity)
	check "same host entity after the reboot (US-2)" test "$id2" = "$id1"

	# US-6: uninstall.
	out=$(in_c /usr/local/bin/omnistat service uninstall --dry-run 2>&1)
	has "uninstall dry-run: what goes (US-6/3)" "would remove /etc/omnistat/omnistat.env" "$out"
	has "uninstall dry-run: what stays (US-6/3)" "would keep /etc/omnistat" "$out"
	out=$(in_c /opt/dl/omnistat service uninstall 2>&1)
	has "uninstall: done (US-6/1)" "omnistat is uninstalled" "$out"
	check "uninstall: only /etc/omnistat and the drop-in remain (FR-020, FR-021)" in_c sh -c '
		! test -e /etc/omnistat/omnistat.env && ! test -e /usr/local/bin/omnistat && ! test -e /etc/systemd/system/omnistat.service &&
		test -f /etc/omnistat/omnistat.yaml && test -f /etc/systemd/system/omnistat.service.d/override.conf &&
		! pgrep -x omnistat >/dev/null'
	out=$(in_c /opt/dl/omnistat service uninstall 2>&1)
	has "uninstall again: not installed (US-6/2)" "not installed" "$out"

	# FR-019: a unit omnistat did not write is left alone.
	in_c sh -c 'printf "[Service]\nExecStart=/bin/true\n" > /etc/systemd/system/omnistat.service && systemctl daemon-reload'
	out=$(with_api /opt/dl/omnistat service install 2>&1)
	has "foreign unit: refused, its path named (FR-019)" "did not write: /etc/systemd/system/omnistat.service" "$out"
}

for d in ${*:-$ALL_DISTROS}; do
	run_distro "$d"
	[[ -n "${E2E_KEEP:-}" ]] || docker rm -f "omnistat-e2e-$d" >/dev/null 2>&1
done
echo
echo "passed $pass, failed $fail"
for n in "${notes[@]}"; do echo "  note: $n"; done
for f in "${failed[@]}"; do echo "  FAIL $f"; done
[[ $fail -eq 0 ]]
