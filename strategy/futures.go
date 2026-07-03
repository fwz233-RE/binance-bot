package strategy

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wferreirauy/binance-bot/ai"
	"github.com/wferreirauy/binance-bot/config"
	"github.com/wferreirauy/binance-bot/exchange"
	"github.com/wferreirauy/binance-bot/indicator"
	"github.com/wferreirauy/binance-bot/storage"
	"github.com/wferreirauy/binance-bot/tui"
)

// FuturesTrade runs a long/short strategy on Binance USDT-M perpetual futures.
// direction: "auto" follows tendency, "long"/"short" wait for a matching one.
func FuturesTrade(
	configFile string,
	symbol string,
	qty float64,
	stopLoss float64,
	takeProfit float64,
	roundPrice uint,
	roundAmount uint,
	max_ops uint,
	direction string,
) {
	var c config.Config
	cfg, err := c.Read(configFile)
	if err != nil {
		log.Fatal(err)
	}

	direction = strings.ToLower(direction)
	if direction != "auto" && direction != "long" && direction != "short" {
		log.Fatal("error: --direction must be 'auto', 'long', or 'short'")
	}
	if re := regexp.MustCompile(`(?m)^[0-9A-Z]{1,8}/[0-9A-Z]{2,8}$`); !re.Match([]byte(symbol)) {
		log.Fatal("error parsing ticker: must match ^[0-9A-Z]{1,8}/[0-9A-Z]{2,8}$")
	}
	scoin, dcoin, found := strings.Cut(symbol, "/")
	if !found {
		log.Fatal("error parsing ticker: \"/\" is missing ")
	}
	ticker := strings.Replace(symbol, "/", "", -1)

	fc := exchange.NewFuturesClient()

	// AI orchestrator, same wiring as the spot strategies: enabled providers
	// analyze each scan tick and entries require consensus approval.
	var aiOrch *ai.Orchestrator
	if cfg.AI.Enabled {
		aiOrch = ai.NewOrchestrator(
			os.Getenv("OPENAI_API_KEY"),
			os.Getenv("DEEPSEEK_API_KEY"),
			os.Getenv("ANTHROPIC_API_KEY"),
			cfg.AI.Providers.OpenAI.Model,
			cfg.AI.Providers.DeepSeek.Model,
			cfg.AI.Providers.Claude.Model,
		)
		if !aiOrch.IsEnabled() {
			aiOrch = nil
		}
	}

	refreshSecs := cfg.RefreshInterval
	if refreshSecs <= 0 {
		refreshSecs = 10
	}
	refreshInterval := time.Duration(refreshSecs) * time.Second

	mode := "FUTURES " + strings.ToUpper(direction)
	dash := tui.NewDashboard(mode, symbol)
	dash.SetConfig(cfg)

	fl, err := tui.NewFileLogger(sessionLogFile(ticker))
	if err != nil {
		log.Printf("Warning: could not open log file: %v", err)
	} else {
		defer fl.Close()
		dash.SetFileLogger(fl)
	}

	go func() {
		defer dash.Stop()
		dash.SetRefreshInterval(refreshInterval)
		dash.SetParams(&tui.TradeParams{
			Amount: qty, StopLoss: stopLoss, TakeProfit: takeProfit,
			RoundPrice: roundPrice, RoundAmt: roundAmount, MaxOps: max_ops,
		})
		dash.LogInfo("[red::b]FUTURES MAINNET[-] — real funds and liquidation risk")
		logStartupStatus(dash, cfg, aiOrch)
		futuresSetup(dash, fc, cfg, ticker)
		futuresTradeLoop(dash, fc, cfg, aiOrch, symbol, ticker, scoin, dcoin, qty, stopLoss, takeProfit, roundPrice, roundAmount, max_ops, refreshInterval, direction)
	}()

	if err := dash.Run(); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}

// futuresSetup applies leverage and margin type once per session.
func futuresSetup(dash *tui.Dashboard, fc *exchange.FuturesClient, cfg *config.Config, ticker string) {
	leverage := cfg.Futures.Leverage
	if leverage <= 0 {
		leverage = 2
	}
	marginType := cfg.Futures.MarginType
	if marginType == "" {
		marginType = "isolated"
	}
	if err := fc.SetMarginType(ticker, marginType); err != nil && !exchange.IsFuturesCode(err, -4046) {
		// -4046 = "No need to change margin type" — already what we asked for
		dash.LogError(fmt.Sprintf("Set margin type: %v", err))
	}
	if err := fc.SetLeverage(ticker, leverage); err != nil {
		dash.LogError(fmt.Sprintf("Set leverage: %v", err))
	} else {
		dash.LogInfo(fmt.Sprintf("[cyan]Futures setup[-] %s margin, leverage %dx", strings.ToUpper(marginType), leverage))
	}
}

// closeFill carries what actually happened when a position was closed: the
// close order id and the average fill price (0 when unknown, e.g. the
// position was already flat or the response omitted the fill).
type closeFill struct {
	OrderID int64
	Price   float64
}

// closePositionRelentlessly market-closes a position with reduceOnly and never
// gives up: a leveraged position without exit management risks liquidation, so
// on error it backs off and retries until Binance accepts the order. Returns
// the actual close fill so the journal can record realized (not decision) P&L.
func closePositionRelentlessly(dash *tui.Dashboard, fc *exchange.FuturesClient, ticker, closeSide string, qty float64, label string) closeFill {
	backoff := 2 * time.Second
	for {
		order, err := fc.FuturesMarketOrder(ticker, closeSide, qty, true)
		if err == nil {
			dash.LogOrder(fmt.Sprintf("%s close order #%d status %s", label, order.OrderId, order.Status))
			fill := closeFill{OrderID: order.OrderId}
			if p, perr := parseFloatNonZero(order.AvgPrice); perr == nil {
				fill.Price = p
			}
			return fill
		}
		// Position already gone (e.g. reduce-only rejected because qty is 0)?
		if pos, perr := fc.FuturesGetPosition(ticker); perr == nil && pos.PositionAmt == 0 {
			dash.LogInfo(fmt.Sprintf("[yellow]%s close: position already flat[-]", label))
			return closeFill{}
		}
		dash.LogError(fmt.Sprintf("%s close failed (retrying in %s): %v", label, backoff, err))
		time.Sleep(backoff)
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

// futuresOp carries the identity of one open position from entry to exit so
// every exit record in the journal is self-sufficient for P&L reconstruction.
type futuresOp struct {
	ID         string
	EntryPrice float64
	EntryTime  time.Time
	IsLong     bool
	Qty        float64
}

func (op futuresOp) direction() string {
	if op.IsLong {
		return "long"
	}
	return "short"
}

// pnlPct is direction-normalized: positive = in profit for our side.
func (op futuresOp) pnlPct(price float64) float64 {
	if op.EntryPrice == 0 {
		return 0
	}
	if op.IsLong {
		return (price - op.EntryPrice) / op.EntryPrice * 100
	}
	return (op.EntryPrice - price) / op.EntryPrice * 100
}

// recordFuturesExit journals a position close, pairing it with its entry via
// OpID and embedding realized P&L, fees and holding time. P&L uses the actual
// fill price when available (slippage stays visible in the data) and falls
// back to the decision price. Every exit path must go through here so the
// journal alone reconstructs the session. Returns the realized net P&L %.
func recordFuturesExit(cfg *config.Config, ticker, closeSide string, op futuresOp, fill closeFill, decisionPrice, feeRoundTrip float64, reason string) float64 {
	exitPrice := fill.Price
	if exitPrice == 0 {
		exitPrice = decisionPrice
	}
	pnl := op.pnlPct(exitPrice)
	recordTrade(cfg, storage.TradeRecord{
		Symbol: ticker, Side: closeSide, OrderID: fill.OrderID,
		Quantity: op.Qty, Price: exitPrice, Reason: reason,
		Direction: op.direction(), EntryPrice: op.EntryPrice,
		PnLPct: pnl, PnLNetPct: pnl - feeRoundTrip, FeePct: feeRoundTrip,
		HoldSecs: time.Since(op.EntryTime).Seconds(), OpID: op.ID,
	})
	return pnl - feeRoundTrip
}

func futuresTradeLoop(
	dash *tui.Dashboard,
	fc *exchange.FuturesClient,
	cfg *config.Config,
	aiOrch *ai.Orchestrator,
	symbol, ticker, scoin, dcoin string,
	qty, stopLoss, takeProfit float64,
	roundPrice, roundAmount, max_ops uint,
	refreshInterval time.Duration,
	direction string,
) {
	period := cfg.HistoricalPrices.Period
	interval := cfg.HistoricalPrices.Interval
	var operation = 1
	var consecutiveLosses int

	// Round-trip taker fees as % of notional (~% of price move). Every exit
	// decision must clear this floor or a "profitable" close is a net loss.
	feeRoundTrip := futuresRoundTripFeePct(dash, fc, cfg, ticker)

	// max_ops == 0 means run until manually stopped (24/7 mode)
	for max_ops == 0 || operation <= int(max_ops) {
		dash.SetOperation(operation)
		qty = indicator.RoundFloat(qty, roundAmount)

		//// ENTRY: wait for a tradable direction, then score the entry ////
		dash.SetPhase("SCANNING ENTRY")
		var op futuresOp
		var lastWait string

		for {
			ohlcv, err := fc.FuturesKlines(ticker, interval, period)
			if err != nil || len(ohlcv.Closes) < 2 {
				if err != nil {
					dash.LogError(fmt.Sprintf("Futures klines: %v", err))
				}
				time.Sleep(refreshInterval)
				continue
			}
			price := ohlcv.Closes[len(ohlcv.Closes)-1]
			prevPrice := ohlcv.Closes[len(ohlcv.Closes)-2]
			dash.UpdatePrice(price, prevPrice, roundPrice)

			// Tendency from the configured tendency interval (falls back to
			// the in-hand trading-interval candles when the fetch fails).
			// The 1m close series was too noisy to steer auto direction.
			tendency := futuresTendencyAt(fc, cfg, ticker, ohlcv.Closes)
			candidateLong := tendency == "up"

			// Forced direction gate
			if direction == "long" && !candidateLong {
				lastWait = logWaitOnce(dash, lastWait, "long-wait:"+tendency,
					fmt.Sprintf("[yellow]Tendency is %s[-] — waiting for [green]UP[-] to open LONG", tendency))
				time.Sleep(refreshInterval)
				continue
			}
			if direction == "short" && tendency != "down" {
				lastWait = logWaitOnce(dash, lastWait, "short-wait:"+tendency,
					fmt.Sprintf("[yellow]Tendency is %s[-] — waiting for [red]DOWN[-] to open SHORT", tendency))
				time.Sleep(refreshInterval)
				continue
			}
			if tendency == "flat" {
				lastWait = logWaitOnce(dash, lastWait, "flat-wait",
					"[yellow]Tendency is flat[-] — waiting for a confirmed direction")
				time.Sleep(refreshInterval)
				continue
			}

			// Higher-timeframe trend gate, mirroring the spot strategies:
			// block LONG when the HTF trend is not up, SHORT when not down.
			if cfg.Tendency.HTFEnabled && cfg.Tendency.HTFInterval != "" {
				htp := cfg.HTFTendencyParams()
				htf, htfErr := futuresTendencyFor(fc, cfg, ticker, htp)
				if htfErr != nil {
					dash.LogError(fmt.Sprintf("HTF Tendency: %v", htfErr))
				} else if (candidateLong && htf != "up") || (!candidateLong && htf != "down") {
					label := "LONG"
					if !candidateLong {
						label = "SHORT"
					}
					lastWait = logWaitOnce(dash, lastWait, "htf-gate:"+htf,
						fmt.Sprintf("[red]HTF GATE[-] %s trend is [red]%s[-] on %s — skipping %s entry",
							ticker, htf, htp.Interval, label))
					time.Sleep(refreshInterval)
					continue
				}
			}

			// Funding filter: skip entries whose side would pay an outsized
			// funding rate (longs pay when positive, shorts when negative).
			if maxFunding := cfg.Futures.MaxFundingPct; maxFunding > 0 {
				if idx, fErr := fc.FuturesGetPremiumIndex(ticker); fErr == nil {
					paying := (candidateLong && idx.FundingRatePct > maxFunding) ||
						(!candidateLong && -idx.FundingRatePct > maxFunding)
					if paying {
						lastWait = logWaitOnce(dash, lastWait, "funding-gate",
							fmt.Sprintf("[yellow]FUNDING GATE[-] rate %+.4f%%/interval exceeds %.4f%% — skipping entry", idx.FundingRatePct, maxFunding))
						time.Sleep(refreshInterval)
						continue
					}
				}
			}

			// Indicators + scalp scoring (shared with spot strategies).
			rsi := indicator.CalculateSmoothedRSI(ohlcv.Closes, cfg.Indicators.Rsi.Length, cfg.Indicators.Rsi.SmoothLength)
			macdLine, signalLine := indicator.CalculateMACD(ohlcv.Closes, cfg.Indicators.Macd.FastLength, cfg.Indicators.Macd.SlowLength, cfg.Indicators.Macd.SignalLength)
			bb, bbErr := indicator.CalculateBollingerBands(ohlcv.Closes, cfg.Indicators.BollingerBands.Length, cfg.Indicators.BollingerBands.Multiplier)
			if bbErr != nil || len(rsi) == 0 || len(macdLine) < 2 {
				time.Sleep(refreshInterval)
				continue
			}
			var atrVal float64
			if cfg.Indicators.Atr.Period > 0 {
				atrSeries := indicator.CalculateATR(ohlcv.Highs, ohlcv.Lows, ohlcv.Closes, cfg.Indicators.Atr.Period)
				if len(atrSeries) > 0 {
					atrVal = atrSeries[len(atrSeries)-1]
				}
			}
			var adxVal float64
			adxStrong := true
			if cfg.Indicators.Adx.Period > 0 {
				adx := indicator.CalculateADX(ohlcv.Highs, ohlcv.Lows, ohlcv.Closes, cfg.Indicators.Adx.Period)
				if len(adx) > 0 {
					adxVal = adx[len(adx)-1]
					adxStrong = adxVal > float64(cfg.Indicators.Adx.Threshold)
				}
			}
			var currentVolume, avgVolume float64
			if cfg.Indicators.Volume.MaPeriod > 0 {
				volumeMA := indicator.CalculateSMA(ohlcv.Volumes, cfg.Indicators.Volume.MaPeriod)
				currentVolume = ohlcv.Volumes[len(ohlcv.Volumes)-1]
				if len(volumeMA) > 0 {
					avgVolume = volumeMA[len(volumeMA)-1]
				}
			}

			// Full indicator panel, matching the spot dashboards.
			dema := indicator.CalculateDEMA(ohlcv.Closes, cfg.Indicators.Dema.Length)
			var currentDema float64
			if len(dema) > 0 {
				currentDema = dema[len(dema)-1]
			}
			macdCross := "BEARISH"
			if macdLine[len(macdLine)-1] > signalLine[len(signalLine)-1] {
				macdCross = "BULLISH"
			}
			dash.UpdateIndicators(&tui.IndicatorData{
				RSI: rsi[len(rsi)-1], RSIUpperLimit: cfg.Indicators.Rsi.UpperLimit, RSILowerLimit: cfg.Indicators.Rsi.LowerLimit,
				MACDLine: macdLine[len(macdLine)-1], SignalLine: signalLine[len(signalLine)-1], MACDCross: macdCross,
				DEMA: currentDema, UpperBand: bb.UpperBand[len(bb.UpperBand)-1], LowerBand: bb.LowerBand[len(bb.LowerBand)-1],
				Tendency: tendency, ADX: adxVal, ADXThreshold: cfg.Indicators.Adx.Threshold,
				Volume: currentVolume, AvgVolume: avgVolume,
				ATR: atrVal, Price: price,
			})

			// AI analysis: same per-tick consensus gate as the spot strategies.
			// Long entries need BUY approval, short entries need SELL approval.
			var aiApproved = true
			if aiOrch != nil {
				snapshot := &ai.TechnicalSnapshot{
					Symbol: symbol, Price: price, PrevPrice: prevPrice,
					RSI: rsi[len(rsi)-1], MACDLine: macdLine[len(macdLine)-1], SignalLine: signalLine[len(signalLine)-1],
					PrevMACDLine: macdLine[len(macdLine)-2], PrevSignalLine: signalLine[len(signalLine)-2],
					UpperBand: bb.UpperBand[len(bb.UpperBand)-1], LowerBand: bb.LowerBand[len(bb.LowerBand)-1],
					DEMA: currentDema, Tendency: tendency,
					ADX: adxVal, Volume: currentVolume, AvgVolume: avgVolume,
				}
				aiMode := "BULL"
				if !candidateLong {
					aiMode = "BEAR"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				consensus, err := aiOrch.Analyze(ctx, snapshot, aiMode)
				cancel()
				if err != nil {
					dash.LogError(fmt.Sprintf("AI: %v", err))
				} else {
					updateDashAI(dash, consensus)
					if candidateLong {
						aiApproved = consensus.ShouldBuyWithMinConfidence(cfg.AI.MinConfidence)
					} else {
						aiApproved = consensus.ShouldSellWithMinConfidence(cfg.AI.MinConfidence)
					}
				}
			}

			eval := evaluateScalp(scalpEvalInput{
				IsBull: candidateLong, Cfg: cfg,
				Closes: ohlcv.Closes, RSI: rsi,
				MACDLine: macdLine, SignalLine: signalLine,
				BB: bb, Tendency: tendency,
				ADXStrong: adxStrong, ADXVal: adxVal,
				CurrentVolume: currentVolume, AvgVolume: avgVolume,
				ATRVal: atrVal, Price: price,
			})
			if eval.RegimeBlocked || eval.ExtremeBlocked || eval.Score < eval.MinScore {
				time.Sleep(refreshInterval)
				continue
			}
			if !aiApproved {
				lastWait = logWaitOnce(dash, lastWait, "ai-veto:"+tendency,
					"[yellow]Entry blocked[-] — scalp score passed but AI consensus below min confidence")
				time.Sleep(refreshInterval)
				continue
			}

			side := "BUY"
			label := "LONG"
			if !candidateLong {
				side = "SELL"
				label = "SHORT"
			}

			// Margin pre-check: skip the entry quietly when the futures wallet
			// cannot fund it, instead of spamming rejected orders (-2019).
			leverage := cfg.Futures.Leverage
			if leverage <= 0 {
				leverage = 2
			}
			requiredMargin := qty * price / float64(leverage) * 1.05 // 5% headroom for fees/mark-price gap
			if available, balErr := fc.FuturesBalance(dcoin); balErr == nil && available < requiredMargin {
				lastWait = logWaitOnce(dash, lastWait, fmt.Sprintf("margin-wait:%.2f", requiredMargin),
					fmt.Sprintf("[yellow]Entry skipped[-] — need ~%.2f %s margin, available %.2f; waiting for funds",
						requiredMargin, dcoin, available))
				time.Sleep(refreshInterval)
				continue
			}

			logEntryConditions(dash, label, eval.Conditions, eval.Score, eval.MaxScore, eval.MinScore, true)

			dash.SetPhase("OPENING " + label)
			order, err := fc.FuturesMarketOrder(ticker, side, qty, false)
			if err != nil {
				dash.LogError(fmt.Sprintf("OPEN %s failed: %v", label, err))
				time.Sleep(refreshInterval)
				continue // entry failure is recoverable: stay in the scan loop
			}
			// MARKET+RESULT responses carry the fill; fall back to position read.
			op = futuresOp{
				ID:         fmt.Sprintf("%s-%d-%d", ticker, operation, time.Now().Unix()),
				EntryPrice: parsePriceOrPosition(fc, ticker, order.AvgPrice, price),
				EntryTime:  time.Now(),
				IsLong:     candidateLong,
				Qty:        qty,
			}
			dash.SetTradeMode("FUTURES " + label)
			dash.LogOrder(fmt.Sprintf("[green::b]OPEN %s[-] %f %s @ [white::b]%.*f[-] %s (order #%d)",
				label, qty, scoin, roundPrice, op.EntryPrice, dcoin, order.OrderId))
			recordTrade(cfg, storage.TradeRecord{
				Symbol: ticker, Side: side, OrderID: order.OrderId, Status: order.Status,
				Quantity: qty, Price: op.EntryPrice, Reason: "futures-entry",
				Direction: op.direction(), Operation: operation, OpID: op.ID,
			})
			break
		}

		postDelay := 5
		if cfg.ScalpMode.PostBuyDelay > 0 {
			postDelay = cfg.ScalpMode.PostBuyDelay
		}
		time.Sleep(time.Duration(postDelay) * time.Second)

		//// EXIT: monitor TP / SL / trailing; close with reduceOnly ////
		dash.SetPhase("MONITORING EXIT")
		_, netPnL := futuresExitLoop(dash, fc, cfg, aiOrch, ticker, scoin, dcoin, op, stopLoss, takeProfit, roundPrice, roundAmount, refreshInterval, feeRoundTrip)

		// Loss cooldown keys off the realized outcome, not the exit
		// mechanism: a profitable break-even "stop" is not a loss, and a
		// losing max-hold exit is one.
		if netPnL < 0 {
			consecutiveLosses++
			maxConsec := cfg.ScalpMode.MaxConsecutiveSL
			if maxConsec <= 0 {
				maxConsec = 2
			}
			if cfg.ScalpMode.SLCooldown && consecutiveLosses >= maxConsec {
				baseSecs := cfg.ScalpMode.CooldownBaseSecs
				if baseSecs <= 0 {
					baseSecs = 60
				}
				cooldown := baseSecs * (1 << (consecutiveLosses - maxConsec))
				if cooldown > 600 {
					cooldown = 600
				}
				dash.LogInfo(fmt.Sprintf("[red]LOSS COOLDOWN[-] %d consecutive losing exits — waiting %ds", consecutiveLosses, cooldown))
				time.Sleep(time.Duration(cooldown) * time.Second)
			}
		} else {
			consecutiveLosses = 0
		}

		operation++
		// Re-entry cooldown: wait out N closed bars before scanning again so
		// an exit is never followed by an immediate same-price re-entry that
		// only pays another fee round-trip.
		if bars := cfg.ScalpMode.ReentryCooldownBars; bars > 0 {
			if barDur := intervalDuration(interval); barDur > 0 {
				wait := time.Duration(bars) * barDur
				dash.LogInfo(fmt.Sprintf("[yellow]Re-entry cooldown[-] waiting %s (%d closed bars)", wait, bars))
				time.Sleep(wait)
			}
		}
		interOpDelay := 10
		if cfg.ScalpMode.InterOpDelay > 0 {
			interOpDelay = cfg.ScalpMode.InterOpDelay
		}
		dash.LogInfo(fmt.Sprintf("Operation #%d complete. Next in %ds...", operation-1, interOpDelay))
		time.Sleep(time.Duration(interOpDelay) * time.Second)
	}
}

// futuresExitLoop watches an open position until an exit fires. Returns the
// exit type ("tp", "sl", "ts") and the realized net P&L % of the close.
// Closing uses reduceOnly and infinite retry. feeRoundTrip (% of notional)
// gates every in-profit exit so displayed wins survive the fee deduction.
func futuresExitLoop(
	dash *tui.Dashboard,
	fc *exchange.FuturesClient,
	cfg *config.Config,
	aiOrch *ai.Orchestrator,
	ticker, scoin, dcoin string,
	op futuresOp,
	stopLoss, takeProfit float64,
	roundPrice, roundAmount uint,
	refreshInterval time.Duration,
	feeRoundTrip float64,
) (string, float64) {
	period := cfg.HistoricalPrices.Period
	interval := cfg.HistoricalPrices.Interval
	isLong := op.IsLong
	qty := op.Qty
	entryPrice := op.EntryPrice
	closeSide := "SELL"
	if !isLong {
		closeSide = "BUY"
	}
	pnlPct := op.pnlPct

	// Bars are counted as *closed klines* derived from wall-clock time; the
	// legacy per-tick counter (one "bar" per refresh) only remains as a
	// fallback for unknown interval tokens.
	barDur := intervalDuration(interval)
	ticksSinceEntry := 0

	bestPrice := entryPrice
	peakPnL := 0.0
	breakevenActive := false

	for {
		ohlcv, err := fc.FuturesKlines(ticker, interval, period)
		if err != nil || len(ohlcv.Closes) < 2 {
			if err != nil {
				dash.LogError(fmt.Sprintf("Futures klines: %v", err))
			}
			time.Sleep(refreshInterval)
			continue
		}
		price := ohlcv.Closes[len(ohlcv.Closes)-1]
		prevPrice := ohlcv.Closes[len(ohlcv.Closes)-2]
		dash.UpdatePrice(price, prevPrice, roundPrice)

		// Exit decisions price on the mark price when available — it is what
		// the liquidation engine uses — while indicators keep the kline series.
		exitPrice := price
		if idx, mErr := fc.FuturesGetPremiumIndex(ticker); mErr == nil && idx.MarkPrice > 0 {
			exitPrice = idx.MarkPrice
		}

		pnl := pnlPct(exitPrice)
		if pnl > peakPnL {
			peakPnL = pnl
		}
		ticksSinceEntry++
		barsSinceEntry := ticksSinceEntry
		if barDur > 0 {
			barsSinceEntry = int(time.Since(op.EntryTime) / barDur)
		}

		var atrVal float64
		if cfg.Indicators.Atr.Period > 0 {
			atrSeries := indicator.CalculateATR(ohlcv.Highs, ohlcv.Lows, ohlcv.Closes, cfg.Indicators.Atr.Period)
			if len(atrSeries) > 0 {
				atrVal = atrSeries[len(atrSeries)-1]
			}
		}
		rsi := indicator.CalculateSmoothedRSI(ohlcv.Closes, cfg.Indicators.Rsi.Length, cfg.Indicators.Rsi.SmoothLength)
		macdLine, signalLine := indicator.CalculateMACD(ohlcv.Closes, cfg.Indicators.Macd.FastLength, cfg.Indicators.Macd.SlowLength, cfg.Indicators.Macd.SignalLength)
		if len(rsi) > 0 {
			dash.UpdateIndicators(&tui.IndicatorData{
				RSI: rsi[len(rsi)-1], RSIUpperLimit: cfg.Indicators.Rsi.UpperLimit, RSILowerLimit: cfg.Indicators.Rsi.LowerLimit,
				Tendency: fmt.Sprintf("P&L: %+.2f%% (net %+.2f%%)", pnl, pnl-feeRoundTrip),
				ATR:      atrVal, Price: price,
			})
		}

		// Track the best price seen in our favor (high for long, low for short).
		if (isLong && exitPrice > bestPrice) || (!isLong && exitPrice < bestPrice) {
			bestPrice = exitPrice
		}

		// Trailing stop, direction-normalized.
		if cfg.TrailingStop.Enabled {
			activated := pnlPct(bestPrice) >= cfg.TrailingStop.ActivationPct
			if activated {
				var trail float64
				var hit bool
				if isLong {
					trail = bestPrice * (1 - cfg.TrailingStop.TrailingPct/100)
					hit = exitPrice <= trail
				} else {
					trail = bestPrice * (1 + cfg.TrailingStop.TrailingPct/100)
					hit = exitPrice >= trail
				}
				if hit {
					dash.SetPhase("TRAILING STOP")
					dash.LogInfo(fmt.Sprintf("[fuchsia]Trailing-stop:[-] price %.*f vs trail %.*f (best %.*f, P&L %+.2f%%)",
						roundPrice, exitPrice, roundPrice, trail, roundPrice, bestPrice, pnl))
					fill := closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[fuchsia::b]TRAILING-STOP[-]")
					return "ts", recordFuturesExit(cfg, ticker, closeSide, op, fill, exitPrice, feeRoundTrip, "futures-trailing")
				}
			}
		}

		// ATR-aware effective TP/SL (same DSL as spot), then two fee gates:
		// 1. TP must clear round-trip fees + buffer, or an ATR-shrunk target
		//    would close "in profit" at a net loss.
		// 2. Break-even pins the exit at net zero (entry + fees), not entry.
		effectiveTP, effectiveSL := effectiveTPAndSL(cfg, takeProfit, stopLoss, atrVal, price)
		if minTP := feeRoundTrip + cfg.Fees.BufferPct; feeRoundTrip > 0 && effectiveTP < minTP {
			effectiveTP = minTP
		}
		if cfg.ScalpMode.BreakevenATRMult > 0 && atrVal > 0 && !breakevenActive {
			atrPct := (atrVal / entryPrice) * 100
			// The arm threshold must clear the exit floor (fees + buffer):
			// in low-ATR regimes the pure ATR threshold sits below the floor
			// and arming would realize a guaranteed micro-loss immediately.
			armAt := cfg.ScalpMode.BreakevenATRMult * atrPct
			if minArm := feeRoundTrip + cfg.Fees.BufferPct; armAt < minArm {
				armAt = minArm
			}
			// Only arm while the position is currently above the floor.
			// peakPnL is history — if ATR shrinks later, the threshold can be
			// met retroactively while the position is already under water,
			// and arming then would close a losing trade labeled break-even
			// (seen live: armed at P&L −0.25% and closed on the spot).
			if peakPnL >= armAt && pnl > feeRoundTrip {
				breakevenActive = true
				dash.LogInfo(fmt.Sprintf("[lime]BREAK-EVEN[-] peak P&L %.2f%% ≥ %.2f%% — exit floor pinned to net zero (fees %.2f%%)", peakPnL, armAt, feeRoundTrip))
			}
		}
		if breakevenActive {
			// Exit floor after break-even. Legacy: pinned at net zero, which
			// harvested winners at +fees. With breakeven-trail-atr-mult the
			// floor trails peak − mult × ATR%, giving winners room to reach TP
			// while never dropping back below net zero.
			floor := feeRoundTrip
			if mult := cfg.ScalpMode.BreakevenTrailATRMult; mult > 0 && atrVal > 0 {
				atrPct := (atrVal / entryPrice) * 100
				if trailed := peakPnL - mult*atrPct; trailed > floor {
					floor = trailed
				}
			}
			// SL triggers at pnl <= -effectiveSL, so -floor means "close when
			// gross profit falls back to the floor".
			if effectiveSL > -floor {
				effectiveSL = -floor
			}
		}

		// Stop-loss (liquidation protection is exactly this line running 24/7)
		if pnl <= -effectiveSL {
			dash.SetPhase("STOP LOSS")
			dash.LogInfo(fmt.Sprintf("[red]Stop-loss:[-] P&L %+.2f%% <= %+.2f%% (entry %.*f, price %.*f)",
				pnl, -effectiveSL, roundPrice, entryPrice, roundPrice, exitPrice))
			fill := closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[red::b]STOP-LOSS[-]")
			return "sl", recordFuturesExit(cfg, ticker, closeSide, op, fill, exitPrice, feeRoundTrip, "futures-sl")
		}

		// Max-hold: unconditional time exit. Losing positions previously had
		// no time-based way out and could bleed for hours toward the full SL;
		// this frees capital and risk after N closed bars regardless of P&L.
		if cfg.ScalpMode.MaxHoldBars > 0 && barsSinceEntry >= cfg.ScalpMode.MaxHoldBars {
			dash.SetPhase("MAX HOLD")
			dash.LogInfo(fmt.Sprintf("[yellow]Max-hold:[-] %d closed bars, P&L %+.2f%% gross / %+.2f%% net — closing unconditionally", barsSinceEntry, pnl, pnl-feeRoundTrip))
			fill := closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[yellow::b]MAX-HOLD[-]")
			return "ts", recordFuturesExit(cfg, ticker, closeSide, op, fill, exitPrice, feeRoundTrip, "futures-maxhold")
		}

		// Time-stop for flat positions: only exits once gross P&L at least
		// covers the fees, so freeing capital never converts flat to loss.
		if cfg.ScalpMode.TimeStopBars > 0 && barsSinceEntry >= cfg.ScalpMode.TimeStopBars && pnl >= feeRoundTrip && pnl < effectiveTP {
			dash.SetPhase("TIME STOP")
			dash.LogInfo(fmt.Sprintf("[yellow]Time-stop:[-] %d closed bars, P&L %+.2f%% gross / %+.2f%% net (TP not reached)", barsSinceEntry, pnl, pnl-feeRoundTrip))
			fill := closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[yellow::b]TIME-STOP[-]")
			return "ts", recordFuturesExit(cfg, ticker, closeSide, op, fill, exitPrice, feeRoundTrip, "futures-timestop")
		}

		// MACD-peak exit: lock gains when histogram rolls over, but only once
		// gross P&L covers the fees — unlike spot, never converts a micro-win
		// into a net loss.
		if pnl >= feeRoundTrip && shouldMACDPeakExit(cfg, macdLine, signalLine, isLong, pnl) {
			dash.SetPhase("MACD PEAK EXIT")
			dash.LogInfo(fmt.Sprintf("[fuchsia]MACD-peak exit:[-] histogram rolling over, P&L %+.2f%% gross / %+.2f%% net", pnl, pnl-feeRoundTrip))
			fill := closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[fuchsia::b]MACD-PEAK[-]")
			return "tp", recordFuturesExit(cfg, ticker, closeSide, op, fill, exitPrice, feeRoundTrip, "futures-macdpeak")
		}

		// Take-profit (effectiveTP already clears the fee floor).
		// Mirrors spot: when AI is enabled, the exit needs consensus approval
		// (long closes = SELL signal, short closes = BUY signal).
		if pnl >= effectiveTP {
			var aiExitApproved = true
			if aiOrch != nil {
				exitTendency := "sell-exit"
				exitSignal := ai.SignalSell
				aiMode := "BULL"
				if !isLong {
					exitTendency = "buy-exit"
					exitSignal = ai.SignalBuy
					aiMode = "BEAR"
				}
				snapshot := &ai.TechnicalSnapshot{
					Symbol: ticker, Price: price, PrevPrice: prevPrice,
					Tendency: exitTendency,
				}
				if len(rsi) > 0 {
					snapshot.RSI = rsi[len(rsi)-1]
				}
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				consensus, err := aiOrch.Analyze(ctx, snapshot, aiMode)
				cancel()
				if err != nil {
					dash.LogError(fmt.Sprintf("AI exit: %v", err))
				} else {
					updateDashAI(dash, consensus)
					aiExitApproved = consensus.AllowsExit(exitSignal, cfg.AI.MinConfidence)
				}
			}
			if !aiExitApproved {
				dash.LogInfo("[yellow]TP reached but AI vetoed exit — holding[-]")
				time.Sleep(refreshInterval)
				continue
			}
			dash.SetPhase("TAKE PROFIT")
			dash.LogInfo(fmt.Sprintf("[green]Take-profit:[-] P&L %+.2f%% gross / %+.2f%% net >= %.2f%% (entry %.*f, price %.*f)",
				pnl, pnl-feeRoundTrip, effectiveTP, roundPrice, entryPrice, roundPrice, exitPrice))
			fill := closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[green::b]TAKE-PROFIT[-]")
			return "tp", recordFuturesExit(cfg, ticker, closeSide, op, fill, exitPrice, feeRoundTrip, "futures-tp")
		}

		time.Sleep(refreshInterval)
	}
}

// futuresRoundTripFeePct resolves the taker fee for the symbol and returns the
// round-trip cost (2 legs) as % of notional. Returns 0 when fees are disabled
// in config; falls back to 0.05%/leg when the commission API is unavailable.
func futuresRoundTripFeePct(dash *tui.Dashboard, fc *exchange.FuturesClient, cfg *config.Config, ticker string) float64 {
	if cfg != nil && !cfg.Fees.Enabled {
		return 0
	}
	taker, err := fc.FuturesTakerFeePct(ticker)
	if err != nil || taker <= 0 {
		taker = 0.05 // Binance USDT-M VIP0 taker default
		if err != nil {
			dash.LogError(fmt.Sprintf("Fee lookup failed, assuming %.3f%%/leg: %v", taker, err))
		}
	}
	roundTrip := taker * 2
	dash.LogInfo(fmt.Sprintf("[yellow]Fee-aware exits[-] taker %.4f%%/leg — profits must clear %.4f%% round-trip", taker, roundTrip))
	return roundTrip
}

// intervalDuration converts a Binance kline interval token (1m, 5m, 1h, ...)
// into its wall-clock duration. Returns 0 for unknown tokens so callers can
// fall back to tick-based behavior.
func intervalDuration(interval string) time.Duration {
	if len(interval) < 2 {
		return 0
	}
	n, err := strconv.Atoi(interval[:len(interval)-1])
	if err != nil || n <= 0 {
		return 0
	}
	switch interval[len(interval)-1] {
	case 's':
		return time.Duration(n) * time.Second
	case 'm':
		return time.Duration(n) * time.Minute
	case 'h':
		return time.Duration(n) * time.Hour
	case 'd':
		return time.Duration(n) * 24 * time.Hour
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour
	}
	return 0
}

// futuresTendencyAt resolves the trading tendency on the configured
// tendency.interval, falling back to the in-hand closes (trading interval)
// when no dedicated interval is set or the fetch fails.
func futuresTendencyAt(fc *exchange.FuturesClient, cfg *config.Config, ticker string, fallbackCloses []float64) string {
	tp := cfg.TradingTendencyParams()
	if tp.Interval != "" && tp.Interval != cfg.HistoricalPrices.Interval {
		if t, err := futuresTendencyFor(fc, cfg, ticker, tp); err == nil {
			return t
		}
	}
	return futuresTendency(cfg, fallbackCloses)
}

// futuresTendencyFor fetches klines for the given tendency parameter set and
// applies the DEMA-vs-EMA rule. Used for both the trading tendency and the
// higher-timeframe gate.
func futuresTendencyFor(fc *exchange.FuturesClient, cfg *config.Config, ticker string, tp config.TendencyParams) (string, error) {
	frames := tp.Frames
	if frames <= 0 {
		frames = cfg.HistoricalPrices.Period
	}
	ohlcv, err := fc.FuturesKlines(ticker, tp.Interval, frames)
	if err != nil {
		return "", err
	}
	if len(ohlcv.Closes) == 0 {
		return "", fmt.Errorf("futures: no klines for %s@%s", ticker, tp.Interval)
	}
	return tendencyFromCloses(ohlcv.Closes, tp.FastLength, tp.SlowLength, tp.ConfirmBars), nil
}

// futuresTendency reuses the DEMA-vs-EMA tendency rule on candles already in hand.
func futuresTendency(cfg *config.Config, closes []float64) string {
	tp := cfg.TradingTendencyParams()
	return tendencyFromCloses(closes, tp.FastLength, tp.SlowLength, tp.ConfirmBars)
}

// tendencyFromCloses is the pure DEMA-vs-EMA tendency rule: "up" when DEMA
// stays above EMA for the confirmation window, "down" when below, else "flat".
func tendencyFromCloses(closes []float64, fastLen, slowLen, confirm int) string {
	if fastLen <= 0 {
		fastLen = 9
	}
	if slowLen <= 0 {
		slowLen = len(closes)
	}
	if confirm <= 0 {
		confirm = 1
	}
	dema := indicator.CalculateDEMA(closes, fastLen)
	ema, err := indicator.CalculateEMA(closes, slowLen)
	if err != nil || len(ema) == 0 || len(dema) == 0 {
		return "flat"
	}
	n := len(dema)
	if len(ema) < n {
		n = len(ema)
	}
	if confirm > n {
		confirm = n
	}
	up, down := true, true
	for i := n - confirm; i < n; i++ {
		if dema[i] <= ema[i] {
			up = false
		}
		if dema[i] >= ema[i] {
			down = false
		}
	}
	switch {
	case up:
		return "up"
	case down:
		return "down"
	default:
		return "flat"
	}
}

// logWaitOnce logs a waiting message only when the wait-state changes,
// avoiding log spam during long idle scans.
func logWaitOnce(dash *tui.Dashboard, last, state, msg string) string {
	if state != last {
		dash.LogInfo(msg)
	}
	return state
}

// parsePriceOrPosition extracts the fill price from an order response, falling
// back to the live position's entry price, then to the observed market price.
func parsePriceOrPosition(fc *exchange.FuturesClient, ticker, avgPrice string, fallback float64) float64 {
	if p, err := parseFloatNonZero(avgPrice); err == nil {
		return p
	}
	if pos, err := fc.FuturesGetPosition(ticker); err == nil && pos.EntryPrice > 0 {
		return pos.EntryPrice
	}
	return fallback
}

func parseFloatNonZero(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	if err != nil {
		return 0, err
	}
	if v == 0 {
		return 0, fmt.Errorf("zero value")
	}
	return v, nil
}