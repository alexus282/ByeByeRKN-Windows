package engine

import (
	"fmt"
	"time"
)

// stallDetector watches the byte counters and handshake time and says when a session looks dead:
// either the handshake went stale (WireGuard drops sessions after 180 s), or we keep sending while
// nothing comes back, which is how DPI cutting a flow mid-way shows up.
//
// Keepalives alone (32 bytes every 25 s) stay far below minTx, so an idle machine is fine.
type stallDetector struct {
	samples []sample
}

type sample struct {
	at     time.Time
	rx, tx int64
}

const (
	stallWindow        = 20 * time.Second
	stallMinTx         = 4096
	stallHandshakeAge  = 200 * time.Second
	stallEnoughHistory = stallWindow * 3 / 4
)

func (d *stallDetector) reset() { d.samples = nil }

// update returns why the session should be checked, or "" while it looks healthy.
func (d *stallDetector) update(now time.Time, rx, tx int64, handshake time.Time) string {
	if !handshake.IsZero() {
		if age := now.Sub(handshake); age > stallHandshakeAge {
			return fmt.Sprintf("последний handshake %d с назад", int(age.Seconds()))
		}
	}
	// Counters restart when the tunnel is replaced; old samples are meaningless then.
	if len(d.samples) > 0 {
		last := d.samples[len(d.samples)-1]
		if rx < last.rx || tx < last.tx {
			d.reset()
		}
	}
	d.samples = append(d.samples, sample{now, rx, tx})
	for len(d.samples) > 1 && now.Sub(d.samples[0].at) > stallWindow {
		d.samples = d.samples[1:]
	}
	oldest := d.samples[0]
	if now.Sub(oldest.at) < stallEnoughHistory {
		return ""
	}
	if tx-oldest.tx >= stallMinTx && rx == oldest.rx {
		return "нет входящего трафика"
	}
	return ""
}
