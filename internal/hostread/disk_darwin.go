package hostread

// classify: on macOS gopsutil reports only whole media driven by a block
// storage driver, i.e. physical disks; APFS synthesized disks are not among
// them (spec 008 FR-011, feature 008 spike).
func classify(string) DeviceKind { return KindDisk }
