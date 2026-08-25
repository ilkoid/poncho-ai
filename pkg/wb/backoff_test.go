package wb

import (
	"testing"
	"time"
)

// transientBackoff: ретрай при 5xx не быстрее собственного ритма тул-а + 3с.
// Быстрые эндпоинты (интервал < 5с) получают прежний пол 5с + 3с; медленные
// (order-feed 1 req/min) — 63с: ретрай «через 5с» там ловит 429 того же
// минутного окна и сжигает попытку.
func TestTransientBackoff(t *testing.T) {
	cases := []struct {
		name   string
		minInt time.Duration
		want   time.Duration
	}{
		{"feed 1 req/min", time.Minute, 63 * time.Second},
		{"analytics 3 req/min", 20 * time.Second, 23 * time.Second},
		{"fast floor", 200 * time.Millisecond, 8 * time.Second},
		{"zero interval", 0, 8 * time.Second},
	}
	for _, tc := range cases {
		if got := transientBackoff(tc.minInt); got != tc.want {
			t.Errorf("%s: transientBackoff(%v) = %v, want %v", tc.name, tc.minInt, got, tc.want)
		}
	}
}
