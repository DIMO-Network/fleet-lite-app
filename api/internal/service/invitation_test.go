package service

import "testing"

// The webhook applies events monotonically: a status may only replace a
// strictly lower-ranked one, so Postmark retries and out-of-order arrivals
// (e.g. Open before Delivery) can never walk the status backwards, and a
// bounce is never masked by a later delivery/open of the same message.
func TestEmailStatusRank(t *testing.T) {
	order := []string{"", "unknown", EmailStatusSent, EmailStatusDelivered, EmailStatusOpened, EmailStatusBounced}
	for i := 1; i < len(order); i++ {
		prev, cur := order[i-1], order[i]
		if emailStatusRank(cur) < emailStatusRank(prev) {
			t.Errorf("rank(%q)=%d < rank(%q)=%d; want monotonic",
				cur, emailStatusRank(cur), prev, emailStatusRank(prev))
		}
	}
	if emailStatusRank("unknown") != emailStatusRank("") {
		t.Errorf("unknown status should rank like empty (lowest)")
	}
	if emailStatusRank(EmailStatusBounced) <= emailStatusRank(EmailStatusOpened) {
		t.Errorf("bounced must outrank opened so a bounce is never overwritten")
	}
}
