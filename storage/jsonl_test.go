package storage

import (
	"testing"
	"time"
)

func TestTradesFileFor(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", TradesFile},
		{"DOGEUSDT", "trades-DOGEUSDT.jsonl"},
		{"DOGE/USDT", "trades-DOGEUSDT.jsonl"},
	}
	for _, c := range cases {
		if got := tradesFileFor(c.in); got != c.want {
			t.Errorf("tradesFileFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReadTradesAggregatesAndSorts(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	// Interleaved appends across two symbols plus a legacy symbol-less record.
	records := []TradeRecord{
		{Time: base.Add(2 * time.Minute), Symbol: "DOGEUSDT", Side: "SELL"},
		{Time: base, Symbol: "ETHUSDT", Side: "BUY"},
		{Time: base.Add(1 * time.Minute), Symbol: "", Side: "BUY"},
		{Time: base.Add(3 * time.Minute), Symbol: "ETHUSDT", Side: "SELL"},
	}
	for _, r := range records {
		if err := store.AppendTrade(r); err != nil {
			t.Fatal(err)
		}
	}

	all, err := ReadTrades(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("ReadTrades returned %d records, want 4", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Time.Before(all[i-1].Time) {
			t.Fatalf("records not time-sorted at index %d", i)
		}
	}

	limited, err := ReadTrades(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || !limited[1].Time.Equal(base.Add(3*time.Minute)) {
		t.Fatalf("limit=2 should keep the newest records, got %+v", limited)
	}
}