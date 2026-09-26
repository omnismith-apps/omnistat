package network

import (
	"context"
	"errors"

	"github.com/omnismith-apps/omnistat/internal/hostread"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// stackKeys are the values the TCP/UDP reading produces.
var stackKeys = []string{KeyTCPEstablished, KeyTCPRetransPct, KeyTCPResetsPs, KeyUDPErrorsPs}

// stackRates computes the TCP/UDP rates and the retransmission share between
// two readings (FR-010, FR-012, FR-013, FR-015). Each value is dropped alone
// when a counter it depends on went backwards.
func stackRates(prev, cur reading, secs float64) []module.Observation {
	var obs []module.Observation
	rate := func(key string, p, c uint64) {
		if c >= p {
			obs = append(obs, module.Observation{Key: key, Value: round2(float64(c-p) / secs)})
		}
	}
	if prev.stack != nil && cur.stack != nil {
		p, c := *prev.stack, *cur.stack
		// FR-012: OutSegs excludes retransmissions on Linux and Windows, so
		// all segments sent is their sum. No segment sent is no share.
		if c.TCPOutSegs >= p.TCPOutSegs && c.TCPRetransSegs >= p.TCPRetransSegs {
			out, re := c.TCPOutSegs-p.TCPOutSegs, c.TCPRetransSegs-p.TCPRetransSegs
			if out+re > 0 {
				obs = append(obs, module.Observation{Key: KeyTCPRetransPct, Value: pct(float64(re), float64(out+re))})
			}
		}
		rate(KeyTCPResetsPs, p.TCPOutRsts, c.TCPOutRsts)
		rate(KeyUDPErrorsPs, p.UDPInErrors, c.UDPInErrors)
	}
	if prev.listen != nil && cur.listen != nil {
		rate(KeyTCPListenDropsPs, *prev.listen, *cur.listen)
	}
	return obs
}

// gauges returns the values that need no baseline, from the current reading
// or read once now (FR-011, FR-014, FR-016). Only Linux maintains TIME_WAIT
// and connection tracking, so only Linux is asked (FR-018).
func (m *Module) gauges(ctx context.Context, cur reading, linux bool, om *module.Omissions) []module.Observation {
	var obs []module.Observation
	if cur.stack != nil {
		obs = append(obs, module.Observation{Key: KeyTCPEstablished, Value: float64(cur.stack.TCPCurrEstab)})
	}
	if !linux {
		return obs
	}
	if n, err := m.Reader.TimeWait(ctx); err != nil {
		om.Add(KeyTCPTimeWait, err)
	} else {
		obs = append(obs, module.Observation{Key: KeyTCPTimeWait, Value: float64(n)})
	}
	ct, err := m.Reader.Conntrack(ctx)
	switch {
	case errors.Is(err, hostread.ErrNoConntrack) || (err == nil && ct.Max == 0):
		// FR-014: a steady state, said once; values start if it is loaded later.
		m.noConntrack.Do(func() {
			m.logger().Info("connection tracking not loaded; conntrack_used_pct is not published", "module", Name)
		})
	case err != nil:
		om.Add(KeyConntrackUsedPct, err)
	default:
		obs = append(obs, module.Observation{Key: KeyConntrackUsedPct, Value: pct(float64(ct.Count), float64(ct.Max))})
	}
	return obs
}
