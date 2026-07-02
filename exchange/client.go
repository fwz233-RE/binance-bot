package exchange

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	binance "github.com/binance/binance-connector-go"
)

var (
	APIKey    string = os.Getenv("BINANCE_API_KEY")
	SecretKey string = os.Getenv("BINANCE_SECRET_KEY")
	BaseURL   string = "https://api1.binance.com"
)

// timeOffset holds local-clock minus Binance-server-clock in milliseconds.
// The connector subtracts it from every signed request timestamp, so signed
// calls stay valid even when the host clock drifts beyond Binance's 1000ms
// tolerance (APIError -1021).
var timeOffset atomic.Int64

var timeSyncOnce sync.Once

const (
	timeSyncInterval = 30 * time.Minute // re-sync cadence after a good sync
	timeSyncRetry    = time.Minute      // retry cadence until the first good sync
	timeSyncSamples  = 5                // probes per sync round; lowest-RTT one wins
	maxUsableRTT     = 2 * time.Second  // samples above this are too distorted to trust
	// Binance rejects timestamps >1s ahead of server time but tolerates up to
	// recvWindow (default 5s) behind. Biasing the compensated timestamp toward
	// the "behind" side keeps residual RTT-estimation error on the tolerant side.
	aheadSafetyBiasMs = 200
)

// NewClient is the single factory for Binance API clients. It applies the
// current server-clock offset and lazily starts the background re-sync loop.
func NewClient() *binance.Client {
	timeSyncOnce.Do(startTimeSync)
	c := binance.NewClient(APIKey, SecretKey, BaseURL)
	c.TimeOffset = timeOffset.Load()
	return c
}

// SyncServerTime measures the local-vs-server clock offset and stores it for
// subsequent clients. It probes several times and keeps the lowest-RTT sample:
// the first request through a cold connection (TLS handshake, proxy warm-up)
// can take seconds, which would poison a single midpoint estimate.
func SyncServerTime(ctx context.Context) (int64, error) {
	c := binance.NewClient("", "", BaseURL)

	bestRTT := time.Duration(-1)
	bestOffset := int64(0)
	var lastErr error

	for i := 0; i < timeSyncSamples; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				i = timeSyncSamples // stop probing, evaluate what we have
				continue
			case <-time.After(200 * time.Millisecond):
			}
		}
		start := time.Now()
		st, err := c.NewServerTimeService().Do(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		rtt := time.Since(start)
		if bestRTT < 0 || rtt < bestRTT {
			bestRTT = rtt
			bestOffset = start.Add(rtt/2).UnixMilli() - int64(st.ServerTime)
		}
	}

	if bestRTT < 0 {
		return 0, fmt.Errorf("exchange: all server time probes failed: %w", lastErr)
	}
	if bestRTT > maxUsableRTT {
		return 0, fmt.Errorf("exchange: server time probes too slow to trust (best RTT %s)", bestRTT)
	}

	offset := bestOffset + aheadSafetyBiasMs
	timeOffset.Store(offset)
	return offset, nil
}

func startTimeSync() {
	syncNow := func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		offset, err := SyncServerTime(ctx)
		if err != nil {
			log.Printf("exchange: server time sync failed (using offset %dms): %v", timeOffset.Load(), err)
			return false
		}
		if offset > 500 || offset < -500 {
			log.Printf("exchange: local clock is %+dms off Binance server time — compensating", offset)
		}
		return true
	}

	synced := syncNow()
	go func() {
		for {
			interval := timeSyncInterval
			if !synced {
				interval = timeSyncRetry
			}
			time.Sleep(interval)
			synced = syncNow()
		}
	}()
}