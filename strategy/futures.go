package strategy

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

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

	refreshSecs := cfg.RefreshInterval
	if refreshSecs <= 0 {
		refreshSecs = 10
	}
	refreshInterval := time.Duration(refreshSecs) * time.Second

	mode := "FUTURES " + strings.ToUpper(direction)
	dash := tui.NewDashboard(mode, symbol)
	dash.SetConfig(cfg)

	fl, err := tui.NewFileLogger("binance-bot.log")
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
		futuresSetup(dash, fc, cfg, ticker)
		futuresTradeLoop(dash, fc, cfg, symbol, ticker, scoin, dcoin, qty, stopLoss, takeProfit, roundPrice, roundAmount, max_ops, refreshInterval, direction)
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

// closePositionRelentlessly market-closes a position with reduceOnly and never
// gives up: a leveraged position without exit management risks liquidation, so
// on error it backs off and retries until Binance accepts the order.
func closePositionRelentlessly(dash *tui.Dashboard, fc *exchange.FuturesClient, ticker, closeSide string, qty float64, label string) {
	backoff := 2 * time.Second
	for {
		order, err := fc.FuturesMarketOrder(ticker, closeSide, qty, true)
		if err == nil {
			dash.LogOrder(fmt.Sprintf("%s close order #%d status %s", label, order.OrderId, order.Status))
			return
		}
		// Position already gone (e.g. reduce-only rejected because qty is 0)?
		if pos, perr := fc.FuturesGetPosition(ticker); perr == nil && pos.PositionAmt == 0 {
			dash.LogInfo(fmt.Sprintf("[yellow]%s close: position already flat[-]", label))
			return
		}
		dash.LogError(fmt.Sprintf("%s close failed (retrying in %s): %v", label, backoff, err))
		time.Sleep(backoff)
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

func futuresTradeLoop(
	dash *tui.Dashboard,
	fc *exchange.FuturesClient,
	cfg *config.Config,
	symbol, ticker, scoin, dcoin string,
	qty, stopLoss, takeProfit float64,
	roundPrice, roundAmount, max_ops uint,
	refreshInterval time.Duration,
	direction string,
) {
	period := cfg.HistoricalPrices.Period
	interval := cfg.HistoricalPrices.Interval
	var operation = 1
	var consecutiveSL int

	// max_ops == 0 means run until manually stopped (24/7 mode)
	for max_ops == 0 || operation <= int(max_ops) {
		dash.SetOperation(operation)
		qty = indicator.RoundFloat(qty, roundAmount)

		//// ENTRY: wait for a tradable direction, then score the entry ////
		dash.SetPhase("SCANNING ENTRY")
		var isLong bool
		var entryPrice float64
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

			// Tendency from futures candles (same DEMA-vs-EMA rule as spot).
			tendency := futuresTendency(cfg, ohlcv.Closes)
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

			dash.UpdateIndicators(&tui.IndicatorData{
				RSI: rsi[len(rsi)-1], RSIUpperLimit: cfg.Indicators.Rsi.UpperLimit, RSILowerLimit: cfg.Indicators.Rsi.LowerLimit,
				MACDLine: macdLine[len(macdLine)-1], SignalLine: signalLine[len(signalLine)-1],
				Tendency: tendency, ADX: adxVal, ADXThreshold: cfg.Indicators.Adx.Threshold,
				Volume: currentVolume, AvgVolume: avgVolume,
				ATR: atrVal, Price: price,
			})

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
			entryPrice = parsePriceOrPosition(fc, ticker, order.AvgPrice, price)
			isLong = candidateLong
			dash.SetTradeMode("FUTURES " + label)
			dash.LogOrder(fmt.Sprintf("[green::b]OPEN %s[-] %f %s @ [white::b]%.*f[-] %s (order #%d)",
				label, qty, scoin, roundPrice, entryPrice, dcoin, order.OrderId))
			recordTrade(cfg, storage.TradeRecord{
				Symbol: ticker, Side: side, OrderID: order.OrderId, Status: order.Status,
				Quantity: qty, Price: entryPrice, Reason: "futures-entry",
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
		exitType := futuresExitLoop(dash, fc, cfg, ticker, scoin, dcoin, qty, entryPrice, stopLoss, takeProfit, roundPrice, roundAmount, refreshInterval, isLong)

		if exitType == "sl" {
			consecutiveSL++
			maxConsec := cfg.ScalpMode.MaxConsecutiveSL
			if maxConsec <= 0 {
				maxConsec = 2
			}
			if cfg.ScalpMode.SLCooldown && consecutiveSL >= maxConsec {
				baseSecs := cfg.ScalpMode.CooldownBaseSecs
				if baseSecs <= 0 {
					baseSecs = 60
				}
				cooldown := baseSecs * (1 << (consecutiveSL - maxConsec))
				if cooldown > 600 {
					cooldown = 600
				}
				dash.LogInfo(fmt.Sprintf("[red]SL COOLDOWN[-] %d consecutive SLs — waiting %ds", consecutiveSL, cooldown))
				time.Sleep(time.Duration(cooldown) * time.Second)
			}
		} else {
			consecutiveSL = 0
		}

		operation++
		interOpDelay := 10
		if cfg.ScalpMode.InterOpDelay > 0 {
			interOpDelay = cfg.ScalpMode.InterOpDelay
		}
		dash.LogInfo(fmt.Sprintf("Operation #%d complete. Next in %ds...", operation-1, interOpDelay))
		time.Sleep(time.Duration(interOpDelay) * time.Second)
	}
}

// futuresExitLoop watches an open position until an exit fires. Returns the
// exit type ("tp", "sl", "ts"). Closing uses reduceOnly and infinite retry.
func futuresExitLoop(
	dash *tui.Dashboard,
	fc *exchange.FuturesClient,
	cfg *config.Config,
	ticker, scoin, dcoin string,
	qty, entryPrice, stopLoss, takeProfit float64,
	roundPrice, roundAmount uint,
	refreshInterval time.Duration,
	isLong bool,
) string {
	period := cfg.HistoricalPrices.Period
	interval := cfg.HistoricalPrices.Interval
	closeSide := "SELL"
	if !isLong {
		closeSide = "BUY"
	}
	// pnlPct is direction-normalized: positive = in profit for our side.
	pnlPct := func(price float64) float64 {
		if isLong {
			return (price - entryPrice) / entryPrice * 100
		}
		return (entryPrice - price) / entryPrice * 100
	}

	bestPrice := entryPrice
	barsSinceEntry := 0
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

		pnl := pnlPct(price)
		if pnl > peakPnL {
			peakPnL = pnl
		}
		barsSinceEntry++

		var atrVal float64
		if cfg.Indicators.Atr.Period > 0 {
			atrSeries := indicator.CalculateATR(ohlcv.Highs, ohlcv.Lows, ohlcv.Closes, cfg.Indicators.Atr.Period)
			if len(atrSeries) > 0 {
				atrVal = atrSeries[len(atrSeries)-1]
			}
		}
		rsi := indicator.CalculateSmoothedRSI(ohlcv.Closes, cfg.Indicators.Rsi.Length, cfg.Indicators.Rsi.SmoothLength)
		if len(rsi) > 0 {
			dash.UpdateIndicators(&tui.IndicatorData{
				RSI: rsi[len(rsi)-1], RSIUpperLimit: cfg.Indicators.Rsi.UpperLimit, RSILowerLimit: cfg.Indicators.Rsi.LowerLimit,
				Tendency: fmt.Sprintf("P&L: %+.2f%%", pnl),
				ATR:      atrVal, Price: price,
			})
		}

		// Track the best price seen in our favor (high for long, low for short).
		if (isLong && price > bestPrice) || (!isLong && price < bestPrice) {
			bestPrice = price
		}

		// Trailing stop, direction-normalized.
		if cfg.TrailingStop.Enabled {
			activated := pnlPct(bestPrice) >= cfg.TrailingStop.ActivationPct
			if activated {
				var trail float64
				var hit bool
				if isLong {
					trail = bestPrice * (1 - cfg.TrailingStop.TrailingPct/100)
					hit = price <= trail
				} else {
					trail = bestPrice * (1 + cfg.TrailingStop.TrailingPct/100)
					hit = price >= trail
				}
				if hit {
					dash.SetPhase("TRAILING STOP")
					dash.LogInfo(fmt.Sprintf("[fuchsia]Trailing-stop:[-] price %.*f vs trail %.*f (best %.*f, P&L %+.2f%%)",
						roundPrice, price, roundPrice, trail, roundPrice, bestPrice, pnl))
					closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[fuchsia::b]TRAILING-STOP[-]")
					return "ts"
				}
			}
		}

		// ATR-aware effective TP/SL, then break-even pinning (same DSL as spot).
		effectiveTP, effectiveSL := effectiveTPAndSL(cfg, takeProfit, stopLoss, atrVal, price)
		if cfg.ScalpMode.BreakevenATRMult > 0 && atrVal > 0 && !breakevenActive {
			atrPct := (atrVal / entryPrice) * 100
			if peakPnL >= cfg.ScalpMode.BreakevenATRMult*atrPct {
				breakevenActive = true
				dash.LogInfo(fmt.Sprintf("[lime]BREAK-EVEN[-] peak P&L %.2f%% — SL pinned to entry %.*f", peakPnL, roundPrice, entryPrice))
			}
		}
		if breakevenActive && effectiveSL > 0 {
			effectiveSL = 0
		}

		// Stop-loss (liquidation protection is exactly this line running 24/7)
		if pnl <= -effectiveSL {
			dash.SetPhase("STOP LOSS")
			dash.LogInfo(fmt.Sprintf("[red]Stop-loss:[-] P&L %+.2f%% <= -%.2f%% (entry %.*f, price %.*f)",
				pnl, effectiveSL, roundPrice, entryPrice, roundPrice, price))
			closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[red::b]STOP-LOSS[-]")
			recordTrade(cfg, storage.TradeRecord{Symbol: ticker, Side: closeSide, Quantity: qty, Price: price, Reason: "futures-sl"})
			return "sl"
		}

		// Time-stop for flat positions
		if cfg.ScalpMode.TimeStopBars > 0 && barsSinceEntry >= cfg.ScalpMode.TimeStopBars && pnl >= 0 && pnl < effectiveTP {
			dash.SetPhase("TIME STOP")
			dash.LogInfo(fmt.Sprintf("[yellow]Time-stop:[-] %d bars, P&L %+.2f%% (TP not reached)", barsSinceEntry, pnl))
			closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[yellow::b]TIME-STOP[-]")
			recordTrade(cfg, storage.TradeRecord{Symbol: ticker, Side: closeSide, Quantity: qty, Price: price, Reason: "futures-timestop"})
			return "ts"
		}

		// Take-profit
		if pnl >= effectiveTP {
			dash.SetPhase("TAKE PROFIT")
			dash.LogInfo(fmt.Sprintf("[green]Take-profit:[-] P&L %+.2f%% >= %.2f%% (entry %.*f, price %.*f)",
				pnl, effectiveTP, roundPrice, entryPrice, roundPrice, price))
			closePositionRelentlessly(dash, fc, ticker, closeSide, qty, "[green::b]TAKE-PROFIT[-]")
			recordTrade(cfg, storage.TradeRecord{Symbol: ticker, Side: closeSide, Quantity: qty, Price: price, Reason: "futures-tp"})
			return "tp"
		}

		time.Sleep(refreshInterval)
	}
}

// futuresTendency reuses the DEMA-vs-EMA tendency rule on candles already in hand.
func futuresTendency(cfg *config.Config, closes []float64) string {
	tp := cfg.TradingTendencyParams()
	fastLen := tp.FastLength
	if fastLen <= 0 {
		fastLen = 9
	}
	slowLen := tp.SlowLength
	if slowLen <= 0 {
		slowLen = len(closes)
	}
	confirm := tp.ConfirmBars
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