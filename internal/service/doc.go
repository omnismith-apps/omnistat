// Package service holds what `omnistat service install|uninstall` does the
// same way on every platform (specs 006 and 007): the plan of steps that the
// dry-run prints and the real run applies, the settings stored for the
// service and the prompts that complete them, the pre-check result, and the
// watch after start. The platform backends (winsvc, systemd) decide the steps.
package service
