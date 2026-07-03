package strategy

import (
	"testing"
	"time"
)

func TestIntervalDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"1m", time.Minute},
		{"3m", 3 * time.Minute},
		{"15m", 15 * time.Minute},
		{"1h", time.Hour},
		{"4h", 4 * time.Hour},
		{"1d", 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"30s", 30 * time.Second},
		{"", 0},
		{"m", 0},
		{"1M", 0},  // month token unsupported → tick fallback
		{"0m", 0},
		{"-5m", 0},
	}
	for _, c := range cases {
		if got := intervalDuration(c.in); got != c.want {
			t.Errorf("intervalDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFuturesOpPnLPct(t *testing.T) {
	long := futuresOp{EntryPrice: 100, IsLong: true}
	short := futuresOp{EntryPrice: 100, IsLong: false}
	cases := []struct {
		name  string
		op    futuresOp
		price float64
		want  float64
	}{
		{"long profit", long, 101, 1},
		{"long loss", long, 99, -1},
		{"short profit", short, 99, 1},
		{"short loss", short, 101, -1},
		{"zero entry guard", futuresOp{IsLong: true}, 100, 0},
	}
	for _, c := range cases {
		if got := c.op.pnlPct(c.price); got != c.want {
			t.Errorf("%s: pnlPct(%v) = %v, want %v", c.name, c.price, got, c.want)
		}
	}
}

func TestTendencyFromCloses(t *testing.T) {
	up := make([]float64, 50)
	down := make([]float64, 50)
	for i := range up {
		up[i] = 100 + float64(i)
		down[i] = 100 - float64(i)
	}
	if got := tendencyFromCloses(up, 9, 21, 1); got != "up" {
		t.Errorf("rising closes: tendency = %q, want up", got)
	}
	if got := tendencyFromCloses(down, 9, 21, 1); got != "down" {
		t.Errorf("falling closes: tendency = %q, want down", got)
	}
}