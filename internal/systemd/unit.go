package systemd

import (
	"bytes"
	"strings"
)

// Names and paths of the service (spec 007 "Data & integration contract").
const (
	UnitName   = "omnistat.service"
	UnitPath   = "/etc/systemd/system/" + UnitName
	BinaryPath = "/usr/local/bin/omnistat"
	ConfigDir  = "/etc/omnistat"
	ConfigPath = ConfigDir + "/omnistat.yaml"
	EnvPath    = ConfigDir + "/omnistat.env"
)

// Marker is the unit's first line; a unit without it is not omnistat's (FR-019).
const Marker = "# Written by `omnistat service install`; every install rewrites this file."

// LogHint is where the service logs (FR-022).
const LogHint = "journalctl -u omnistat (warnings and errors only: journalctl -u omnistat -p warning)"

// unitTemplate is the unit install writes; {BIN}, {CONFIG} and {ENV} are the
// paths above. systemd has no trailing comments, so comments stand on their
// own lines.
const unitTemplate = Marker + `
# Local changes belong in a drop-in, which install keeps: systemctl edit omnistat
# https://github.com/omnismith-apps/omnistat

[Unit]
Description=omnistat (Omnismith exporter)
Documentation=https://github.com/omnismith-apps/omnistat
Wants=network-online.target
After=network-online.target
# Keep trying after failures, however many (spec 007 FR-009).
StartLimitIntervalSec=0

[Service]
# omnistat tells systemd when it is ready: config loaded, schema reconciled,
# host identity resolved (FR-010). On stop it extends the stop timeout itself
# to cover its final publish (FR-023).
Type=notify
ExecStart={BIN} --config {CONFIG} run --daemon
# The access token and other settings; readable by root only (FR-013).
EnvironmentFile={ENV}
# Restart one minute after any failure; never after a deliberate stop (FR-009).
Restart=on-failure
RestartSec=60
TimeoutStartSec=180

# Least privilege (FR-008): an unprivileged user allocated while the service
# runs, no capabilities, a read-only system, and systemd's sandbox.
DynamicUser=yes
CapabilityBoundingSet=
AmbientCapabilities=
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
PrivateUsers=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
ProtectProc=invisible
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
SystemCallErrorNumber=EPERM
UMask=0077
RemoveIPC=yes
DevicePolicy=closed

[Install]
WantedBy=multi-user.target
`

// UnitText is the unit file install writes (FR-006–FR-010, FR-013, FR-023).
func UnitText() string {
	return strings.NewReplacer("{BIN}", BinaryPath, "{CONFIG}", ConfigPath, "{ENV}", EnvPath).Replace(unitTemplate)
}

// OwnUnit reports whether a unit file is the one install writes (FR-019).
func OwnUnit(data []byte) bool {
	return bytes.HasPrefix(data, []byte(Marker+"\n"))
}
