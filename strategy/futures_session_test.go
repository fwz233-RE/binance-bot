package strategy

import (
	"testing"
	"time"

	"github.com/wferreirauy/binance-bot/config"
	"github.com/wferreirauy/binance-bot/storage"
)

func journalFixture(t *testing.T, records []storage.TradeRecord) *config.Config {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	for _, r := range records {
		if err := store.AppendTrade(r); err != nil {
			t.Fatalf("AppendTrade: %v", err)
		}
	}
	cfg := &config.Config{}
	cfg.DataDir = dir
	return cfg
}

func TestLastJournalOperationResumesSequence(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	cfg := journalFixture(t, []storage.TradeRecord{
		{Time: base, Symbol: "DOGEUSDT", Side: "BUY", Reason: "futures-entry", Operation: 1, OpID: "DOGEUSDT-1-1"},
		{Time: base.Add(time.Minute), Symbol: "DOGEUSDT", Side: "SELL", Reason: "futures-sl", OpID: "DOGEUSDT-1-1"},
		{Time: base.Add(2 * time.Minute), Symbol: "DOGEUSDT", Side: "BUY", Reason: "futures-entry", Operation: 7, OpID: "DOGEUSDT-7-2"},
		{Time: base.Add(3 * time.Minute), Symbol: "XRPUSDT", Side: "BUY", Reason: "futures-entry", Operation: 42, OpID: "XRPUSDT-42-3"},
	})

	if got := lastJournalOperation(cfg, "DOGEUSDT"); got != 7 {
		t.Fatalf("expected max operation 7 for DOGEUSDT, got %d", got)
	}
	if got := lastJournalOperation(cfg, "BTCUSDT"); got != 0 {
		t.Fatalf("expected 0 for unknown symbol, got %d", got)
	}
}

func TestLastUnpairedEntryFindsOrphan(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	cfg := journalFixture(t, []storage.TradeRecord{
		// paired op: entry + exit
		{Time: base, Symbol: "XRPUSDT", Side: "BUY", Reason: "futures-entry", Operation: 1, OpID: "XRPUSDT-1-1"},
		{Time: base.Add(time.Minute), Symbol: "XRPUSDT", Side: "SELL", Reason: "futures-sl", OpID: "XRPUSDT-1-1"},
		// orphan: entry without exit (process died while monitoring)
		{Time: base.Add(2 * time.Minute), Symbol: "XRPUSDT", Side: "BUY", Reason: "futures-entry", Operation: 2, OpID: "XRPUSDT-2-2"},
	})

	orphan := lastUnpairedEntry(cfg, "XRPUSDT")
	if orphan == nil {
		t.Fatal("expected orphan entry, got nil")
	}
	if orphan.OpID != "XRPUSDT-2-2" {
		t.Fatalf("expected orphan op XRPUSDT-2-2, got %s", orphan.OpID)
	}
}

func TestLastUnpairedEntryNilWhenAllPaired(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	cfg := journalFixture(t, []storage.TradeRecord{
		{Time: base, Symbol: "SOLUSDT", Side: "SELL", Reason: "futures-entry", Operation: 1, OpID: "SOLUSDT-1-1"},
		{Time: base.Add(time.Minute), Symbol: "SOLUSDT", Side: "BUY", Reason: "futures-shutdown", OpID: "SOLUSDT-1-1"},
	})

	if orphan := lastUnpairedEntry(cfg, "SOLUSDT"); orphan != nil {
		t.Fatalf("expected nil, got orphan %s", orphan.OpID)
	}
}