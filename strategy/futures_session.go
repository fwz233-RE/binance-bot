package strategy

import (
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/wferreirauy/binance-bot/config"
	"github.com/wferreirauy/binance-bot/exchange"
	"github.com/wferreirauy/binance-bot/storage"
	"github.com/wferreirauy/binance-bot/tui"
)

// futuresSession owns the lifecycle of a futures trading process around the
// trade/exit loops: startup reconciliation (adopt a position the exchange
// already holds), journal-based recovery (operation counter, orphan entry
// metadata), and shutdown (close the live position and journal the exit no
// matter how the process ends). The loops stay pure trading logic; everything
// that deals with "the process started/stopped around an open position"
// lives here.
type futuresSession struct {
	fc     *exchange.FuturesClient
	cfg    *config.Config
	ticker string

	mu           sync.Mutex
	feeRoundTrip float64
	op           *futuresOp // identity of the currently open position, nil when flat
	once         sync.Once
}

func newFuturesSession(fc *exchange.FuturesClient, cfg *config.Config, ticker string, feeRoundTrip float64) *futuresSession {
	return &futuresSession{fc: fc, cfg: cfg, ticker: ticker, feeRoundTrip: feeRoundTrip}
}

// setFee records the session's round-trip fee once the trade loop resolved it
// (the lookup needs the dashboard, which outlives session construction).
func (s *futuresSession) setFee(feeRoundTrip float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feeRoundTrip = feeRoundTrip
}

// setOpen registers the position the loops just opened (or adopted) so the
// shutdown path can close and journal it.
func (s *futuresSession) setOpen(op futuresOp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := op
	s.op = &cp
}

// clear marks the session flat again after an exit was journaled.
func (s *futuresSession) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.op = nil
}

// watchSignals closes the position and journals the exit on SIGINT/SIGTERM,
// then stops the TUI so the process can unwind. The same shutdown routine
// runs (once) when the TUI quits normally — see FuturesTrade.
func (s *futuresSession) watchSignals(dash *tui.Dashboard) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		s.shutdown()
		dash.Stop()
		os.Exit(0)
	}()
}

// shutdown closes any live position with reduce-only market orders and writes
// the exit record (reason "futures-shutdown"). Runs at most once; safe to call
// from the signal handler and from the post-TUI path. It deliberately logs via
// the global logger, never the dashboard: after the TUI event loop stops,
// QueueUpdateDraw blocks forever.
func (s *futuresSession) shutdown() {
	s.once.Do(func() {
		s.mu.Lock()
		op := s.op
		feeRoundTrip := s.feeRoundTrip
		s.mu.Unlock()
		if op == nil {
			return
		}
		// The exchange is the source of truth: the exit loop may have closed
		// the position between our snapshot and now.
		pos, err := s.fc.FuturesGetPosition(s.ticker)
		if err != nil {
			log.Printf("shutdown: position check failed, attempting close anyway: %v", err)
		} else if pos.PositionAmt == 0 {
			return
		}
		closeSide := "SELL"
		if !op.IsLong {
			closeSide = "BUY"
		}
		log.Printf("shutdown: closing open %s position %s qty %f", op.direction(), s.ticker, op.Qty)
		fill := closePositionQuietly(s.fc, s.ticker, closeSide, op.Qty)
		exitPrice := fill.Price
		if exitPrice == 0 && err == nil && pos.EntryPrice > 0 {
			exitPrice = pos.EntryPrice // last resort so the record is not priceless
		}
		net := recordFuturesExit(s.cfg, s.ticker, closeSide, *op, fill, exitPrice, feeRoundTrip, "futures-shutdown")
		log.Printf("shutdown: %s closed, net P&L %+.3f%%", s.ticker, net)
	})
}

// closePositionQuietly is the shutdown twin of closePositionRelentlessly:
// bounded retries and global-logger output, because the dashboard is gone and
// the process must eventually exit even if the exchange keeps erroring.
func closePositionQuietly(fc *exchange.FuturesClient, ticker, closeSide string, qty float64) closeFill {
	backoff := 2 * time.Second
	for attempt := 1; attempt <= 5; attempt++ {
		order, err := fc.FuturesMarketOrder(ticker, closeSide, qty, true)
		if err == nil {
			fill := closeFill{OrderID: order.OrderId}
			if p, perr := parseFloatNonZero(order.AvgPrice); perr == nil {
				fill.Price = p
			}
			return fill
		}
		if pos, perr := fc.FuturesGetPosition(ticker); perr == nil && pos.PositionAmt == 0 {
			return closeFill{}
		}
		log.Printf("shutdown: close attempt %d failed (retrying in %s): %v", attempt, backoff, err)
		time.Sleep(backoff)
		backoff *= 2
	}
	log.Printf("shutdown: POSITION STILL OPEN on %s — close it manually or restart the bot to reconcile", ticker)
	return closeFill{}
}

// reconcileFuturesPosition adopts a position the exchange already holds for
// this symbol — the process may have crashed or been killed while managing
// it. The exchange snapshot is the source of truth for direction, quantity
// and entry price; the journal only contributes identity metadata (op ID and
// entry time) when an unpaired entry record matches the live direction.
// Returns nil when the account is flat.
func reconcileFuturesPosition(dash *tui.Dashboard, fc *exchange.FuturesClient, cfg *config.Config, ticker string) *futuresOp {
	pos, err := fc.FuturesGetPosition(ticker)
	if err != nil {
		dash.LogError(fmt.Sprintf("Reconcile: position check failed: %v", err))
		return nil
	}
	if pos.PositionAmt == 0 {
		return nil
	}
	op := futuresOp{
		ID:         fmt.Sprintf("%s-recovered-%d", ticker, time.Now().Unix()),
		EntryPrice: pos.EntryPrice,
		EntryTime:  time.Now(),
		IsLong:     pos.PositionAmt > 0,
		Qty:        math.Abs(pos.PositionAmt),
	}
	if orphan := lastUnpairedEntry(cfg, ticker); orphan != nil {
		sameDirection := (orphan.Side == "BUY") == op.IsLong
		if sameDirection {
			op.ID = orphan.OpID
			op.EntryTime = orphan.Time
		}
	}
	dash.LogInfo(fmt.Sprintf("[orange::b]RECONCILED[-] adopted open %s position: qty %f @ %f (op %s) — managing its exit before scanning new entries",
		op.direction(), op.Qty, op.EntryPrice, op.ID))
	return &op
}

// lastUnpairedEntry scans the symbol's journal for the most recent
// futures-entry record that has no exit record sharing its op ID.
func lastUnpairedEntry(cfg *config.Config, ticker string) *storage.TradeRecord {
	records, err := storage.ReadTrades(dataDir(cfg), 0)
	if err != nil {
		return nil
	}
	exited := map[string]bool{}
	var last *storage.TradeRecord
	for i := range records {
		r := records[i]
		if r.Symbol != ticker || r.OpID == "" {
			continue
		}
		if r.Reason == "futures-entry" {
			last = &records[i]
		} else {
			exited[r.OpID] = true
		}
	}
	if last != nil && !exited[last.OpID] {
		return last
	}
	return nil
}

// lastJournalOperation returns the highest operation number the journal has
// recorded for the symbol, so a restarted process continues the sequence
// instead of resetting to 1 (which made per-op analysis ambiguous).
func lastJournalOperation(cfg *config.Config, ticker string) int {
	records, err := storage.ReadTrades(dataDir(cfg), 0)
	if err != nil {
		return 0
	}
	max := 0
	for _, r := range records {
		if r.Symbol == ticker && r.Operation > max {
			max = r.Operation
		}
	}
	return max
}

// dataDir resolves the journal directory consistently with dataStore.
func dataDir(cfg *config.Config) string {
	if cfg != nil && cfg.DataDir != "" {
		return cfg.DataDir
	}
	return ".binance-bot"
}