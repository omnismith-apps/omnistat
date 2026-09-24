package cpu

// Percent is the share of CPU time that was not idle between two readings of
// the cumulative counters, as a percentage clamped to [0, 100] (spec 004
// FR-005). It reports ok == false when the pair cannot yield a trustworthy
// percentage (FR-013), in which case the caller keeps cur as the new baseline
// and simply produces no observation this tick:
//
//   - the total did not advance — two readings inside one kernel tick;
//   - any counter went backwards — a suspend/resume, a CPU hotplug event or a
//     container whose counters were reset. A negative or wildly large
//     percentage is worse than a gap in the series.
//
// The result is aggregate across every logical CPU because the counters
// already are: a fully busy 8-core host reports 100, not 800.
func Percent(prev, cur Times) (float64, bool) {
	if regressed(prev, cur) {
		return 0, false
	}
	total := cur.Total() - prev.Total()
	if total <= 0 {
		return 0, false
	}
	busy := total - (IdleTime(cur) - IdleTime(prev))
	pct := busy / total * 100
	switch {
	case pct < 0:
		return 0, true
	case pct > 100:
		return 100, true
	default:
		return pct, true
	}
}

// regressed reports whether any counter is lower than it was. Checking every
// field, not just the total, catches a reset that happens to leave the sum
// larger.
func regressed(prev, cur Times) bool {
	return cur.User < prev.User ||
		cur.Nice < prev.Nice ||
		cur.System < prev.System ||
		cur.Idle < prev.Idle ||
		cur.Iowait < prev.Iowait ||
		cur.Irq < prev.Irq ||
		cur.Softirq < prev.Softirq ||
		cur.Steal < prev.Steal
}
