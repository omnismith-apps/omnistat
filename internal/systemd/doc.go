// Package systemd runs omnistat as a systemd service on Linux (spec 007): the
// install and uninstall plans, the unit and settings files they write, and the
// runtime side of systemd — readiness and stop notifications, and journal
// priorities. Every call into the OS goes through the Host interface
// (NFR-004), so everything but the raw OS calls is tested on any platform.
package systemd
