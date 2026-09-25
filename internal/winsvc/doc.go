// Package winsvc runs omnistat as a Windows service (spec 006): the install and
// uninstall plans, the service runtime and the Event Log handler. Every call
// into Windows goes through the Host interface (NFR-003), so everything but the
// raw OS calls is tested on any platform.
package winsvc
