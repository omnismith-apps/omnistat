#!/usr/bin/env bash
# Container acceptance of `omnistat upgrade` (spec 009 NFR-006). Manual and
# opt-in, like scripts/e2e-systemd.sh, whose harness it uses (same settings,
# images and isolation): Docker, a reachable Omnismith API, a throwaway project,
# and internet access for the real GitHub release v0.3.0.
#
#   scripts/e2e-upgrade.sh [distro ...]     # default: fedora44 debian12 rocky8
#
# Each container installs the published v0.3.0, then upgrades it from a
# loopback mirror (scripts/e2e-mirror) that serves two local builds: v0.99.0,
# the latest, and v0.98.0 with a tampered checksum. It rolls back to v0.3.0
# from the real GitHub releases.
set -uo pipefail
. "$(dirname "$0")/e2e-systemd.sh"

DEFAULT_DISTROS="fedora44 debian12 rocky8"
ARCH=$(go env GOARCH)
OLD=v0.3.0 NEW=v0.99.0 TAMPERED=v0.98.0
GH=https://github.com/omnismith-apps/omnistat/releases
MIRROR_URL=http://127.0.0.1:8099
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# prepare builds the mirror tree and fetches v0.3.0, verified, into $WORK.
prepare() {
	local v n dir old="omnistat_${OLD#v}_linux_$ARCH.tar.gz"
	mkdir -p "$WORK/old" "$WORK/mirror/latest"
	curl -fsSL -o "$WORK/$old" "$GH/download/$OLD/$old" &&
		curl -fsSL "$GH/download/$OLD/checksums.txt" | grep " $old\$" | (cd "$WORK" && sha256sum -c --quiet) &&
		tar -xzf "$WORK/$old" -C "$WORK/old" omnistat || { echo "fetching $OLD failed" >&2; return 1; }
	for v in $NEW $TAMPERED; do
		n="omnistat_${v#v}_linux_$ARCH.tar.gz"
		dir="$WORK/mirror/download/$v"
		mkdir -p "$dir" "$WORK/build-$v"
		(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$v" -o "$WORK/build-$v/omnistat" ./cmd/omnistat) || return 1
		tar -czf "$dir/$n" -C "$WORK/build-$v" omnistat
		(cd "$dir" && sha256sum "$n" >checksums.txt)
	done
	sed -i -E 's/^[0-9a-f]{64}/0000000000000000000000000000000000000000000000000000000000000000/' "$WORK/mirror/download/$TAMPERED/checksums.txt"
	cp -r "$WORK/mirror/download/$NEW" "$WORK/mirror/latest/download"
	(cd "$ROOT" && CGO_ENABLED=0 go build -o "$WORK/e2e-mirror" ./scripts/e2e-mirror) || return 1
}

# up runs `omnistat upgrade` in the container as root, as after `sudo`: no
# OMNISMITH_* settings, the mirror unless given another URL; prints "exit=N".
up() {
	local bin=$1 url=$2
	shift 2
	docker exec -e OMNISTAT_RELEASES_URL="$url" "$C" "$bin" upgrade "$@" 2>&1
	echo "exit=$?"
}
installed_version() { in_c /usr/local/bin/omnistat version; }
fingerprint() { in_c sha256sum /etc/omnistat/omnistat.env /etc/omnistat/omnistat.yaml /etc/systemd/system/omnistat.service; }
no_leftovers() { in_c sh -c '! ls -a /usr/local/bin | grep -q "^\.omnistat-upgrade-"'; }

run_upgrade() {
	boot "$1" || return
	local out id1 before
	in_c mkdir -p /opt/old /opt/dl /opt/mirror
	docker cp -q "$WORK/old/omnistat" "$C:/opt/old/omnistat"
	docker cp -q "$BIN" "$C:/opt/dl/omnistat"
	docker cp -q "$WORK/mirror/." "$C:/opt/mirror/"
	docker cp -q "$WORK/e2e-mirror" "$C:/usr/bin/e2e-mirror"
	docker exec -d "$C" /usr/bin/e2e-mirror -addr 127.0.0.1:8099 -dir /opt/mirror
	check "/tmp is noexec in the container (FR-011 precondition)" in_c sh -c 'grep -E " /tmp .*noexec" /proc/mounts'

	# The released v0.3.0, installed by its own `service install`.
	out=$(with_api /opt/old/omnistat service install 2>&1)
	has "v0.3.0 installed and running" "installed and running" "$out"
	has "installed version is $OLD" "omnistat $OLD\$" "$(installed_version)"
	id1=$(identity)
	check "host entity exists" test -n "$id1"

	# FR-002: not root → refused before any download.
	out=$(docker exec -u alice -e OMNISTAT_RELEASES_URL="$MIRROR_URL" "$C" /opt/dl/omnistat upgrade 2>&1)
	has "not root: refused with the sudo hint (FR-002)" "run it with sudo" "$out"

	# US-2/1, FR-017: --check reports and exits 2.
	out=$(up /opt/dl/omnistat "$MIRROR_URL" --check)
	has "--check: upgrade available, exit 2 (US-2/1)" "An upgrade is available.*exit=2" "$(tr '\n' ' ' <<<"$out")"

	# US-3/1: the dry-run shows the new version's install and changes nothing.
	before=$(fingerprint)
	out=$(up /opt/dl/omnistat "$MIRROR_URL" --dry-run)
	has "dry-run: new install's plan, staged next to the binary (US-3/1, FR-011)" "would replace /usr/local/bin/omnistat with /usr/local/bin/\.omnistat-upgrade-[^/]+/omnistat" "$out"
	has "dry-run: the full new unit (US-3/1)" "      DynamicUser=yes" "$out"
	has "dry-run: exit 0" "exit=0" "$out"
	secret_free "dry-run prints no setting value (FR-016)" "$out"
	has "dry-run: still $OLD" "omnistat $OLD\$" "$(installed_version)"
	check "dry-run: settings, config and unit untouched" test "$(fingerprint)" = "$before"
	check "dry-run: no staging leftovers (FR-011)" no_leftovers

	# US-1/1, US-5/1: the upgrade, run as after sudo (the stored settings are used).
	out=$(up /opt/dl/omnistat "$MIRROR_URL")
	has "upgrade: verified (FR-009)" "verified:  SHA-256 matches $MIRROR_URL/latest/download/checksums.txt; the binary reports $NEW" "$out"
	has "upgrade: the new install ran (FR-013)" "update of the installed service" "$out"
	has "upgrade: summary, exit 0 (FR-014)" "upgraded from $OLD to $NEW.*exit=0" "$(tr '\n' ' ' <<<"$out")"
	secret_free "upgrade prints no setting value (FR-016)" "$out"
	has "upgrade: installed version is $NEW" "omnistat $NEW\$" "$(installed_version)"
	check "upgrade: service active" wait_state active 20
	check "upgrade: stored settings and config kept (US-1/1)" test "$(head -2 <<<"$before")" = "$(in_c sha256sum /etc/omnistat/omnistat.env /etc/omnistat/omnistat.yaml)"
	check "upgrade: no staging leftovers (FR-011)" no_leftovers
	check "upgrade: same host entity (US-1/1)" test "$(identity)" = "$id1"

	# NFR-003: again → up to date, nothing downloaded.
	out=$(up /usr/local/bin/omnistat "$MIRROR_URL")
	has "again: up to date, exit 0 (NFR-003)" "up to date \($NEW\).*exit=0" "$(tr '\n' ' ' <<<"$out")"
	out=$(up /usr/local/bin/omnistat "$MIRROR_URL" --check)
	has "--check after: exit 0 (US-2/2)" "exit=0" "$out"

	# FR-006, FR-007 against the real GitHub releases: the installed build is newer.
	out=$(up /usr/local/bin/omnistat "" --check)
	has "GitHub: installed $NEW is newer than the latest release (FR-007)" "is newer than the latest release v[0-9.]+.*exit=0" "$(tr '\n' ' ' <<<"$out")"

	# US-1/3, FR-009: a tampered checksum is refused; nothing changes.
	before=$(fingerprint)
	out=$(up /usr/local/bin/omnistat "$MIRROR_URL" --version "$TAMPERED")
	has "tampered: refused, exit 1 (US-1/3)" "does not match its SHA-256.*exit=1" "$(tr '\n' ' ' <<<"$out")"
	has "tampered: still $NEW" "omnistat $NEW\$" "$(installed_version)"
	check "tampered: nothing changed" test "$(fingerprint)" = "$before"
	check "tampered: service still active" wait_state active 5
	check "tampered: no staging leftovers (FR-011)" no_leftovers

	# FR-015: plain HTTP to another host is refused.
	out=$(up /usr/local/bin/omnistat "http://example.invalid/omnistat" --check)
	has "http mirror refused (FR-015)" "must be an https.*exit=1" "$(tr '\n' ' ' <<<"$out")"

	# US-4/1: roll back to the published v0.3.0 from GitHub, run from the installed copy.
	out=$(up /usr/local/bin/omnistat "" --version "$OLD")
	has "rollback: a downgrade, from GitHub (US-4/1)" "this is a downgrade" "$out"
	has "rollback: summary, exit 0" "upgraded from $NEW to $OLD.*exit=0" "$(tr '\n' ' ' <<<"$out")"
	has "rollback: installed version is $OLD" "omnistat $OLD\$" "$(installed_version)"
	check "rollback: service active" wait_state active 20
	check "rollback: same host entity" test "$(identity)" = "$id1"
	out=$(up /opt/dl/omnistat "" --check)
	has "GitHub --check after rollback: up to date or behind" "exit=(0|2)" "$out"
	check "no staging leftovers at the end (FR-011)" no_leftovers
}

prepare || exit 1
for d in ${*:-$DEFAULT_DISTROS}; do
	run_upgrade "$d"
	[[ -n "${E2E_KEEP:-}" ]] || docker rm -f "omnistat-e2e-$d" >/dev/null 2>&1
done
summary
