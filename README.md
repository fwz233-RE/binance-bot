# Binance Trade Bot

<img width="1343" height="863" alt="Image" src="https://github.com/user-attachments/assets/10b5cd6f-c80e-4661-b963-9f5b52641330" />


## Features

- **USDT-M Futures** — `futures-trade` opens leveraged long/short positions on Binance perpetual futures with isolated/crossed margin, configurable leverage, and reduce-only exits with relentless retry. v0.21.0 adds mark-price exit pricing, an HTF trend gate for entry direction, tendency evaluation on `tendency.interval`, and a funding-rate entry filter
- **24/7 Infinite Mode** — Set `--operations 0` to run continuously until manually stopped; the default remains 100 operations per session
- **Multi-Instance** — Run one process per ticker from the same directory: per-ticker log and journal files, aggregated history API, and rate-limit backoff (429/418/-1003 with Retry-After) on the shared IP weight pool
- **Server-Time Sync** — Continuously compensates local clock drift against Binance server time (min-RTT sampling), preventing `-1021` timestamp rejections on signed requests
- **Auto Trade** — Automatically detects market tendency and switches between bull/bear strategies per operation; supports forced strategy mode and waits when the account cannot fund the detected side
- **Bull Trade** — Buy-low-sell-high strategy for uptrending markets
- **Bear Trade** — Sell-high-buy-low strategy for downtrending markets
- **Scalp Mode** — High-frequency micro-trading using a scoring-based entry system. v0.14.0 adds pullback-in-trend RSI, anticipatory MACD with optional consecutive-bar confirmation, Bollinger price-touch + squeeze, RSI-divergence bonus, ATR regime filter, recent-extreme guard, ATR-based TP/SL, time-stop, break-even pin, and MACD-peak exit. v0.14.2 adds the MACD `min-separation` gate so entries only fire after a meaningful prior MACD/signal gap is now closing in. v0.20.0 reworks the exit state machine: bars count closed klines, unconditional `max-hold-bars` exit, post-break-even ATR trailing floor, and re-entry cooldown
- **Advanced Indicators** — RSI (+ optional SMA smoothing), MACD, DEMA, Bollinger Bands (+ width-ratio for squeeze), ADX, ATR, Stochastic RSI, swing-extrema divergence, and volume confirmation
- **Top Gainers Monitor** — Real-time TUI dashboard of the top 24h movers on Binance
- **Rotation Scout Mode** — Scans a configured asset basket and rotates through a bridge asset when relative ratios become fee-adjusted opportunities
- **Backtesting** — Runs registered strategy simulations over recent Binance candles before live trading
- **Managed Orders** — Optional buy/sell timeouts with partial-fill handling; unfilled entries return to scanning instead of advancing to exit monitoring
- **Pre-Order Balance Checks** — Reads Binance spot account balances before placing orders and blocks buys/sells that exceed available free funds
- **Persistent History API** — Stores trade/scout history in JSONL and serves it through a small local HTTP API
- **AI Multi-Agent System** — Concurrent analysis from OpenAI, DeepSeek, and Claude with weighted consensus; when enabled, entries require explicit AI approval at the configured confidence threshold
- **Sentiment Analysis** — Real-time news headlines and Fear & Greed Index integrated into AI decisions
- **Trailing Stop-Loss** — Dynamically locks in profits as price moves favorably
- **Full OHLCV Analysis** — Uses complete candlestick data instead of close-only prices
- **Auto-Notional Adjustment** — Automatically raises order quantity to meet Binance's minimum notional filter
- **Config Validation** — Checks the YAML config file before starting a trading session
- **Detailed Order Reasoning** — Activity Log shows which entry/exit conditions were met (✓/✗) before each trade
- **File Logging** — All trade events and errors are written to `binance-bot.log` alongside the TUI display
- **Fee-Aware Targets** — Take-profit thresholds can be adjusted by live Binance taker fees plus a configurable safety buffer

## Download

#### **Download Precompiled Binary**

You can **download the precompiled binary** from the repository's release artifacts.

1. Visit the [Releases](https://github.com/wferreirauy/binance-bot/releases) page of the repository.
2. Download the appropriate binary for your operating system (e.g., Linux, macOS, Windows).
3. Make the binary executable (if required):
   - On Linux or macOS:
     ```bash
     chmod +x binance-bot
     ```
4. Move the binary to a directory in your `$PATH` for global access:
   - On Linux
   ```bash
   sudo mv binance-bot /usr/local/bin/
   ```

## Usage

> [!WARNING]
> This bot is provided as-is. Use it at your own risk. Trading involves financial risks, and you may incur significant losses. Always test in a safe environment (e.g., a testnet and/or with small amounts) before deploying in live markets. The author is not responsible for any financial outcomes.

---

### Prerequisites

Before using the Binance Trade Bot, you need to configure your environment with the Binance API client credentials. These credentials allow the bot to interact securely with your Binance account. Follow these steps to set up:

1. **Obtain your Binance API Key and Secret**
   - Log in to your [Binance account](https://www.binance.com/).
   - Navigate to the [API Management section](https://www.binance.com/en/my/settings/api-management).
   - Create a new API key, choosing HMAC type and providing any label (e.g., `CLI_Bot`).
   - Save the **API Key** and **Secret Key** securely. You will not be able to view the secret again after closing the page.

2. **Set Environment Variables**
   Export the API credentials as environment variables in your terminal before executing the binance-bot cli:

   ```bash
   export BINANCE_API_KEY=<your-api-key>
   export BINANCE_SECRET_KEY=<your-secret-key>
   ```

   For the `futures-trade` command you can provide dedicated futures keys;
   they fall back to the spot keys when unset:

   ```bash
   export BINANCE_FUTURES_API_KEY=<your-futures-api-key>
   export BINANCE_FUTURES_SECRET_KEY=<your-futures-secret-key>
   ```

3. **Set AI Provider API Keys (optional)**
   To enable the AI multi-agent system, export one or more of the following API keys. The system works with any combination — you can use 1, 2, or all 3 providers:

   ```bash
   export OPENAI_API_KEY=<your-openai-api-key>
   export DEEPSEEK_API_KEY=<your-deepseek-api-key>
   export ANTHROPIC_API_KEY=<your-anthropic-api-key>
   ```

   | Variable | Provider | Default Model |
   |----------|----------|---------------|
   | `OPENAI_API_KEY` | OpenAI | `gpt-4o-mini` |
   | `DEEPSEEK_API_KEY` | DeepSeek | `deepseek-chat` |
   | `ANTHROPIC_API_KEY` | Claude | `claude-3-5-haiku-20241022` |

   > If no AI keys are set, the bot runs entirely on technical indicators — AI is fully optional.

4. **Create a config file**
   You can specify a custom configuration file to adjust the bot's parameters of trading indicators. <br />
   See the [sample configuration file](/sample-binance-config.yml).

#### Now you're ready to use the Binance Trade Bot! 🎉

### Run the Bot

#### Auto Trade (automatic tendency detection)

```bash
binance-bot -f binance-config.yml auto-trade -t "BTC/USDT" -a 0.001 -sl 2.0 -tp 2.5 -b 0.9998 -s 1.0003 -rp 2 -ra 5
```

This example:
- Automatically detects whether `BTC/USDT` is trending up or down before each operation.
- Enters **bull mode** (buy low, sell high) when tendency is "up", or **bear mode** (sell high, buy back low) when tendency is "down".
- Re-detects tendency between every operation, adapting to changing market conditions.
- If tendency flips during entry scanning, the bot dynamically switches mode without waiting.
- The TUI header shows the currently active mode (BULL/BEAR) updated in real-time.

#### Auto Trade with forced strategy

```bash
binance-bot -f binance-config.yml auto-trade -t "DOGE/USDT" -a 100 -sl 2.0 -tp 2.5 -b 0.9998 -s 1.0003 -rp 6 -ra 0 --strategy bull
```

This example:
- Forces the bot to only enter **bull** (buy-first) operations — useful when your account only holds USDT.
- The bot monitors the market and **waits** for an "up" tendency before placing any orders.
- If tendency flips away during scanning, the bot returns to waiting instead of switching to bear.
- Use `--strategy bear` to force sell-first operations (when you hold the base coin and want to sell first).
- Use `--strategy auto` (default) for fully automatic tendency detection.

#### Bull Trade (uptrending markets)

```bash
binance-bot -f binance-config.yml bull-trade -t "XRP/USDT" -a 50 -sl 1.5 -tp 2.0 -b 0.9998 -s 1.0003 -rp 4 -ra 0
```

This example:
- Trades the pair `XRP/USDT` with an amount of `50`.
- Sets a stop-loss of `1.5%` and a take-profit of `2%`.
- Adjusts buy and sell factors for the LIMIT order target price.
- Rounds the price to 4 decimals and the amount to 0 decimals.

#### Bear Trade (downtrending markets)

```bash
binance-bot -f binance-config.yml bear-trade -t "BTC/USDT" -a 0.001 -sl 2.0 -tp 3.0 -b 0.9998 -s 1.0003 -rp 2 -ra 5
```

This example:
- Sells `0.001 BTC` when bearish signals are detected.
- Sets a stop-loss of `2%` (price rises above entry) and take-profit of `3%` (price drops below entry).
- Buys back at a lower price to capture the difference as profit.

#### Scalp Mode (high-frequency micro-trading)

```bash
binance-bot -f sample-scalp-config.yml bull-trade -t "PEPE/USDT" -a 50 --sl 0.6 --tp 1.0 -b 0.9999 -s 1.0001 -rp 8 -ra 0 -o 500
```

This example:
- Uses 1-minute candles and a scoring-based entry (any 3 of 6 signals bullish).
- Sets tight stop-loss / take-profit suitable for volatile low-cap tokens.
- Runs up to 500 operations with only 10s between completed operations.
- See [sample-scalp-config.yml](/sample-scalp-config.yml) for the full config.

#### Top Gainers Monitor

```bash
binance-bot -f binance-config.yml top-gainers
```

Launches a real-time TUI listing the top 24h price-change gainers on Binance, filtered by quote asset, minimum volume, and an exclude list. Refreshes on the configured `poll-interval`. Press `q` to quit.

#### Rotation Scout Mode

```bash
binance-bot -f binance-config.yml rotate-trade
```

Scans the configured `rotation.supported-assets` basket against `rotation.bridge-asset` and records every scout comparison to `.binance-bot/scouts.jsonl`. When a relative-ratio opportunity beats fee-adjusted thresholds, the bot rotates from the current asset into the selected asset through the bridge. The sample config runs this mode as `dry-run: true`; switch it off only after validating behavior with small balances.

#### Backtest

```bash
binance-bot -f binance-config.yml backtest -t "BTC/USDT" --strategy classic-bull
```

Runs a registered strategy over recent Binance candles using the configured indicators, starting balance, and fee assumptions. Available strategies are `classic-bull` and `scalp-bull`.

#### History API

```bash
binance-bot -f binance-config.yml serve
```

Starts a local HTTP server using `api.address`. Endpoints include `/api/health`, `/api/trades`, `/api/scouts`, `/api/values`, and `/api/current-asset`. Use `?limit=100` on history endpoints to read only the most recent records.

#### Validate Configuration

```bash
binance-bot -f binance-config.yml validate-config
```

Reads the YAML configuration, validates required ranges and enum-like values, and exits without starting a trading session. Valid configs print `Config OK`; invalid configs print every issue found so you can fix them in one pass.

Modify these parameters based on your specific trading requirements.

---

#### Explanation of Command Arguments

These arguments apply to the `auto-trade`, `bull-trade`, and `bear-trade` commands:

| Option               | Short | Description                                                                                 | Default       |
|----------------------|-------|---------------------------------------------------------------------------------------------|---------------|
| `--ticker`           | `-t`  | The trading pair ticker in the format `ABC/USD` (e.g., `BTC/USDT`).                         | **Required**  |
| `--amount`           | `-a`  | Amount to trade.                                                                            | **Required**  |
| `--stop-loss`        | `-sl` | Stop-loss percentage (e.g., `1.5` for 1.5%).                                                | `3`           |
| `--take-profit`      | `-tp` | Take-profit percentage (e.g., `3.0` for 3%).                                                | `2.5`         |
| `--buy-factor`       | `-b`  | Factor to determine the target price for a LIMIT buy order.                                 | `0.9999`      |
| `--sell-factor`      | `-s`  | Factor to determine the target price for a LIMIT sell order.                                | `1.0001`      |
| `--round-price`      | `-rp` | Decimal precision for rounding price values.                                                | **Required**  |
| `--round-amount`     | `-ra` | Decimal precision for rounding amount values.                                               | **Required**  |
| `--operations`       | `-o`  | Number of operations to execute (`0` = infinite, run until manually stopped).              | `100`         |
| `--strategy`         | `-st` | *(auto-trade only)* Force entry strategy: `bull`, `bear`, or `auto`.                       | `auto`        |
| `--help`             | `-h`  | Show help for the command.                                                                  | -             |

#### Futures Trading (`futures-trade`, `ft`)

Trades USDT-M perpetual futures: opens leveraged LONG or SHORT positions and
closes them with reduce-only market orders (take-profit, stop-loss, trailing
stop, time-stop). Exit orders are retried indefinitely — a leveraged position
is never left unmanaged. Entries are skipped (with a single log line) while
the futures wallet cannot fund the required margin. Exits are fee-aware: the
live taker rate is fetched per session, take-profit targets are floored at
round-trip fees + buffer, time-stop and break-even close at net-zero or
better, and the dashboard shows both gross and net P&L. When `ai.enabled` is
set, the same AI multi-agent consensus used by the spot strategies gates
futures entries (long = BUY approval, short = SELL approval) and confirms
take-profit exits. The MACD-peak early exit locks gains on histogram
rollover once fees are covered. Leverage and margin type come from the
`futures` config section. **Orders go to the live exchange and trade real
funds.**

```bash
binance-bot -f binance-config.yml futures-trade -t BTC/USDT -a 0.002 -sl 1.0 -tp 1.5 -rp 2 -ra 3 -o 0 -d auto
```

| Option           | Short | Description                                                        | Default      |
|------------------|-------|--------------------------------------------------------------------|--------------|
| `--ticker`       | `-t`  | The trading pair ticker in the format `ABC/USD` (e.g., `BTC/USDT`). | **Required** |
| `--amount`       | `-a`  | Contract quantity in base asset.                                   | **Required** |
| `--stop-loss`    | `-sl` | Stop-loss percentage on position P&L.                              | `1.0`        |
| `--take-profit`  | `-tp` | Take-profit percentage on position P&L.                            | `1.5`        |
| `--round-price`  | `-rp` | Decimal precision for rounding price values.                       | **Required** |
| `--round-amount` | `-ra` | Decimal precision for rounding amount values.                      | **Required** |
| `--operations`   | `-o`  | Number of operations (`0` = infinite, run until manually stopped). | `100`        |
| `--direction`    | `-d`  | Position direction: `long`, `short`, or `auto` (follow tendency).  | `auto`       |

> **Risk note**: P&L percentages are on price movement; with leverage `L` the
> margin impact is `L×` that. A 1% stop-loss at 10x leverage costs 10% of the
> position margin. Keep leverage low and start with minimal quantities.

Every entry and every exit (take-profit, stop-loss, trailing stop, time-stop,
MACD-peak) is journaled to `data-dir/trades-<TICKER>.jsonl` (one file per
symbol). Exit records are
self-sufficient for P&L analysis: they carry `direction` (long/short),
`entry_price`, gross `pnl_pct`, fee-adjusted `pnl_net_pct`, `fee_pct`,
holding time `hold_secs`, and an `op_id` that pairs each exit with its entry.

#### Running Multiple Instances (one per ticker)

Each trading command drives exactly one symbol, so running several coins means
running several processes from the same directory — no extra setup:

```bash
binance-bot -f binance-config.yml futures-trade -t DOGE/USDT -a 134 -rp 5 -ra 0 -o 0 &
binance-bot -f binance-config.yml futures-trade -t ETH/USDT -a 0.01 -rp 2 -ra 3 -o 0 &
```

Isolation guarantees:

- Logs go to `binance-bot-<TICKER>.log` and trade journals to
  `data-dir/trades-<TICKER>.jsonl`, one file per instance.
- The `serve` command aggregates all `trades*.jsonl` files into `/api/trades`;
  run only one `serve` process per port.
- All instances behind one IP share Binance's request-weight pool. The futures
  client backs off exponentially on HTTP 429/418 and code -1003, honoring
  `Retry-After`, but keep `refresh-interval` at 10s or higher when running
  many instances.
- Instances share the account balance: size `--amount` so the combined margin
  requirements fit the wallet, or entries will be skipped.

### Help Commands

- For general help on the bot:
  ```bash
  binance-bot --help
  ```

  Output:
  ```
  NAME:
     binance-bot - A program bot to trade in Binance

  USAGE:
     binance-bot [global options] command <command args>

  VERSION:
     v0.22.0

  AUTHOR:
     Walter Ferreira <wferreirauy@gmail.com>

  COMMANDS:
     bull-trade, bt    Start a bull trade run
     bear-trade, brt   Start a bear trade run (sell high, buy back low)
     auto-trade, at    Automatically detect market tendency and trade accordingly (bull or bear)
     top-gainers, tg   Monitor top market gainers in real-time
     futures-trade, ft  Trade USDT-M perpetual futures (long/short with leverage, config: futures section)
     rotate-trade, rt  Scout a basket of assets and rotate through the configured bridge asset
     backtest, btst    Backtest a registered strategy on recent Binance candles
     serve, srv        Serve persisted trade, scout, and value history over HTTP
     validate-config, vc  Validate the configured YAML file without starting a trading session
     help, h           Shows a list of commands or help for one command

  GLOBAL OPTIONS:
     --config-file FILE, -f FILE  Load configuration from FILE (default: $HOME/binance-config.yml)
     --help, -h     show help
     --version, -v  print the version
  ```

- For help with the `bull-trade` command:
  ```bash
  binance-bot bull-trade --help
  ```

- For help with the `bear-trade` command:
  ```bash
  binance-bot bear-trade --help
  ```

- For help with the `auto-trade` command:
  ```bash
  binance-bot auto-trade --help
  ```

- For help with the `top-gainers` command:
  ```bash
  binance-bot top-gainers --help
  ```

- For help with the `validate-config` command:
  ```bash
  binance-bot validate-config --help
  ```

### TUI Keyboard Shortcuts

While the bot is running, the following keys are available inside the TUI:

| Key | Action |
|-----|--------|
| `q` / `Ctrl+C` | Quit the application |
| `h` | Toggle the help / keyboard shortcuts popup |
| `c` | Show loaded configuration popup |
| `Esc` | Close any open popup |

---

## Configuration

The bot is configured through a YAML file. See [sample-binance-config.yml](/sample-binance-config.yml) for a complete baseline and [sample-scalp-config.yml](/sample-scalp-config.yml) for a high-frequency profile.

### Validation

Run `validate-config` before trading to catch malformed or risky configuration values:

```bash
binance-bot -f binance-config.yml validate-config
```

The command validates every current config section in one pass, including Binance candle intervals, positive periods and polling cadences, RSI limit ordering, MACD length ordering, confidence ranges, top-gainers filters, rotation settings, backtest assumptions, and the API bind address. Valid configs print `Config OK: <file>`.

Valid Binance intervals are: `1s`, `1m`, `3m`, `5m`, `15m`, `30m`, `1h`, `2h`, `4h`, `6h`, `8h`, `12h`, `1d`, `3d`, `1w`, and `1M`.

### Core Settings

```yaml
base-url: "https://api1.binance.com"
data-dir: ".binance-bot"

historical-prices:
  period: 100
  interval: "1m"

refresh-interval: 10
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `base-url` | string | `https://api1.binance.com` | Binance API base URL. Leave empty to use the built-in production endpoint; use `https://testnet.binance.vision` for testnet. |
| `data-dir` | string | `.binance-bot` | Directory used for persisted trade history (`trades-<TICKER>.jsonl` per symbol), scout history, value records, and rotation state. |
| `historical-prices.period` | int | `100` | Number of candles fetched for indicators and backtests. |
| `historical-prices.interval` | string | `1m` | Candle interval used for the main OHLCV fetch. |
| `refresh-interval` | int | `10` | Seconds between live price polls and indicator recalculation. |

### Orders And Fees

```yaml
order-management:
  buy-timeout-minutes: 20
  sell-timeout-minutes: 20
  partial-fill-action: "keep"
  poll-interval-secs: 5

fees:
  enabled: true
  default-taker-pct: 0.1
  buffer-pct: 0.05
  buy-back-buffer-pct: 0.2
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `order-management.buy-timeout-minutes` | int | `20` | Minutes before an unfilled buy limit order is cancelled; `0` disables the timeout. |
| `order-management.sell-timeout-minutes` | int | `20` | Minutes before an unfilled sell limit order is cancelled; `0` disables the timeout. |
| `order-management.partial-fill-action` | string | `keep` | Partial timeout behavior: `keep` leaves the filled portion in place, `reverse` attempts a market order to unwind it. |
| `order-management.poll-interval-secs` | int | `5` | Seconds between order status polls. |
| `fees.enabled` | bool | `true` | Enables fee-aware take-profit decisions. |
| `fees.default-taker-pct` | float | `0.1` | Fallback taker fee percent when live fee lookup is unavailable. |
| `fees.buffer-pct` | float | `0.05` | Extra safety buffer subtracted from take-profit decisions. |
| `fees.buy-back-buffer-pct` | float | `0.2` | Percent withheld when sizing round-trip orders (buy-back, sell-back) and when scaling down orders after an insufficient-balance retry. Lower this when running with BNB fee discounts. Defaults to `0.2` when unset. |

Before submitting any order, the bot checks Binance spot free balances for the required base or quote asset. Fee-aware mode subtracts estimated round-trip taker fees and `buffer-pct` from take-profit decisions. When an order is rejected with insufficient balance, the bot automatically retries once with the quantity reduced to fit the available balance (minus `buy-back-buffer-pct`).

### Tendency And Indicators

```yaml
tendency:
  interval: "3m"
  htf-enabled: false
  htf-interval: "15m"

indicators:
  rsi:
    interval: "5m"
    length: 14
    upper-limit: 70
    middle-limit: 50
    lower-limit: 30
    smooth-length: 0      # >1 applies an SMA smoothing to RSI (e.g. 3)
  dema:
    length: 9
  macd:
    fast-length: 12
    slow-length: 26
    signal-length: 9
    consecutive-bars: 0   # require N consecutive bars of histogram direction (0=no requirement)
    min-separation: 0.0   # require |max hist| within lookback to reach this threshold before MACD signal fires (0=disabled)
    min-separation-lookback: 20  # bars to scan for prior peak histogram separation when min-separation > 0
  bollinger-bands:
    length: 20
    multiplier: 2.0
  atr:
    period: 14
  adx:
    period: 14
    threshold: 25
  volume:
    ma-period: 20
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `tendency.interval` | string | `3m` | Candle interval used to determine bull or bear tendency. |
| `tendency.period` | int | `0` | Frames fetched for trading-interval tendency. `0` falls back to `historical-prices.period`. Letting you decouple tendency depth from MACD/Bollinger warm-up depth. |
| `tendency.fast-length` | int | `0` | DEMA length for tendency fast MA. `0` → `9` (legacy default). |
| `tendency.slow-length` | int | `0` | EMA length for tendency slow MA. `0` → `tendency.period`. Must be greater than `fast-length`. |
| `tendency.confirm-bars` | int | `0` | Require last N bars to agree on the crossover. `0/1` keeps single-bar behavior; raise for anti-flicker on scalping. |
| `tendency.htf-enabled` | bool | `false` | Enables the higher-timeframe trend gate. |
| `tendency.htf-interval` | string | `15m` | Higher-timeframe interval used when `htf-enabled` is true. |
| `tendency.htf-period` | int | `0` | Frames fetched for HTF tendency. `0` → `tendency.period`. |
| `tendency.htf-fast-length` | int | `0` | HTF DEMA length. `0` → `tendency.fast-length`. |
| `tendency.htf-slow-length` | int | `0` | HTF EMA length. `0` → `tendency.slow-length`. |
| `tendency.htf-confirm-bars` | int | `0` | HTF bars that must agree to confirm direction. `0` → `tendency.confirm-bars`. |
| `indicators.rsi.interval` | string | `5m` | Candle interval used for RSI data. |
| `indicators.rsi.length` | int | `14` | RSI lookback length. |
| `indicators.rsi.upper-limit` | int | `70` | Overbought threshold; must be above `middle-limit`. |
| `indicators.rsi.middle-limit` | int | `50` | Neutral RSI threshold. |
| `indicators.rsi.lower-limit` | int | `30` | Oversold threshold; must be below `middle-limit`. |
| `indicators.rsi.smooth-length` | int | `0` | Applies an SMA smoothing of N to the RSI series (0/1 disables, behaves identically to v0.13.x). |
| `indicators.dema.length` | int | `9` | DEMA lookback length for trend/proximity checks. |
| `indicators.macd.fast-length` | int | `12` | Fast MACD EMA length; must be less than `slow-length`. |
| `indicators.macd.slow-length` | int | `26` | Slow MACD EMA length. |
| `indicators.macd.signal-length` | int | `9` | MACD signal EMA length. |
| `indicators.macd.consecutive-bars` | int | `0` | Require histogram direction to hold for N consecutive bars to award the MACD scalp signal (0=last-bar only). |
| `indicators.macd.min-separation` | float | `0` | When > 0, the MACD scalp signal additionally requires the histogram to have reached ≥ this threshold in the prior direction within the lookback (bull: hist ≤ −min-separation, bear: hist ≥ +min-separation). Filters out flat-MACD noise so entries only fire when a meaningful prior MACD/signal gap is now closing in. Typical value: `0.002`. |
| `indicators.macd.min-separation-lookback` | int | `20` | How many bars to scan for the prior peak histogram separation when `min-separation > 0`. |
| `indicators.bollinger-bands.length` | int | `20` | Bollinger moving average length. |
| `indicators.bollinger-bands.multiplier` | float | `2.0` | Standard deviation multiplier for band width. |
| `indicators.atr.period` | int | `14` | ATR volatility lookback used by dynamic stop-loss logic. |
| `indicators.adx.period` | int | `14` | ADX trend-strength lookback. |
| `indicators.adx.threshold` | int | `25` | Minimum ADX value for trend confirmation. |
| `indicators.volume.ma-period` | int | `20` | Volume moving-average period for entry confirmation. |

### Trailing Stop

```yaml
trailing-stop:
  enabled: true
  activation-pct: 1.5
  trailing-pct: 1.0
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `trailing-stop.enabled` | bool | `true` | Enables dynamic trailing exits. |
| `trailing-stop.activation-pct` | float | `1.5` | Favorable move required before trailing begins. |
| `trailing-stop.trailing-pct` | float | `1.0` | Distance from the peak/trough that triggers the exit. |

For bull trades, the stop tracks from the highest price after activation. For bear trades, it tracks from the lowest price after activation.

### Scalp Mode

Scalp mode uses a score instead of requiring all entry conditions at once. The default signals are RSI pullback-in-trend, anticipatory MACD histogram, tendency / MACD zero-line gate, Bollinger price-touch, ADX strength and volume confirmation. Optional weighted scoring boosts MACD/RSI/BB (×2) and divergence (×3) so quality signals dominate quantity.

```yaml
scalp-mode:
  enabled: false
  min-score: 3
  post-buy-delay: 30
  inter-op-delay: 60
  require-rsi-exit: true
  sl-cooldown: false
  max-consecutive-sl: 2
  cooldown-base-secs: 60
  atr-stop-loss: false
  atr-multiplier: 1.5
  # --- v0.14.0 indicator overhaul (all opt-in) ---
  weighted-scoring: false
  bb-squeeze-enabled: false
  bb-squeeze-ratio: 0.85
  bb-squeeze-window: 20
  volume-strong-multiplier: 0.0      # e.g. 1.5 → bonus if vol > 1.5×avg
  divergence-enabled: false
  divergence-lookback: 14
  divergence-swing-pad: 2
  fast-trend-gate: false             # accept tendency OR MACD zero-line
  tp-atr-multiplier: 0.0             # 0=use takeProfit %; >0 → max(takeProfit%, mult × ATR%)
  sl-atr-multiplier: 0.0             # 0=use atr-multiplier; >0 → max(stopLoss%, mult × ATR%)
  time-stop-bars: 0                  # exit flat positions after N bars (0=off)
  breakeven-atr-mult: 0.0            # pin SL to entry once peak P&L ≥ mult × ATR%
  min-atr-pct: 0.0                   # regime filter — skip entries when ATR% below this
  max-atr-pct: 0.0                   # regime filter — skip entries when ATR% above this
  macd-peak-exit: false              # exit in profit when MACD histogram rolls over
  recent-extreme-bars: 0             # skip BULL near recent high / BEAR near recent low
  # --- v0.20 exit-state-machine rework (bar = closed kline, all opt-in) ---
  max-hold-bars: 0                   # close position after N closed bars regardless of P&L (0=off)
  breakeven-trail-atr-mult: 0.0      # after break-even, trail exit floor at peak − mult × ATR%
  reentry-cooldown-bars: 0           # wait N closed bars after any exit before re-entering (0=off)
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `scalp-mode.enabled` | bool | `false` | Enables score-based scalp entries. |
| `scalp-mode.min-score` | int | `3` | Required score threshold (unweighted: out of 6; weighted: out of higher max with bonuses). |
| `scalp-mode.post-buy-delay` | int | `30` | Seconds to wait after fill before exit monitoring. |
| `scalp-mode.inter-op-delay` | int | `60` | Seconds to wait between completed operations. |
| `scalp-mode.require-rsi-exit` | bool | `true` | Requires RSI momentum confirmation for take-profit exits when true. |
| `scalp-mode.sl-cooldown` | bool | `false` | Enables exponential backoff after consecutive stop-losses. |
| `scalp-mode.max-consecutive-sl` | int | `2` | Consecutive stop-loss count before cooldown starts. |
| `scalp-mode.cooldown-base-secs` | int | `60` | Base cooldown seconds; doubles after additional consecutive stop-losses. |
| `scalp-mode.atr-stop-loss` | bool | `false` | Uses ATR as a dynamic stop-loss floor. |
| `scalp-mode.atr-multiplier` | float | `1.5` | Dynamic floor multiplier: `max(configured SL, atr-multiplier * ATR%)`. |
| `scalp-mode.weighted-scoring` | bool | `false` | Doubles MACD/RSI/BB weight (×2) and divergence (×3). |
| `scalp-mode.bb-squeeze-enabled` | bool | `false` | Adds a bonus signal when Bollinger band width is compressed (energy build-up). |
| `scalp-mode.bb-squeeze-ratio` | float | `0.85` | BB width must be ≤ this × average width to qualify as a squeeze. |
| `scalp-mode.bb-squeeze-window` | int | `20` | Lookback window for the BB-width average. |
| `scalp-mode.volume-strong-multiplier` | float | `0.0` | If >0, awards a bonus signal when current volume > mult × avg-volume. |
| `scalp-mode.divergence-enabled` | bool | `false` | Adds a (weighted) bonus signal on bullish/bearish RSI divergence. |
| `scalp-mode.divergence-lookback` | int | `14` | Bars to scan for swing extrema in divergence detection. |
| `scalp-mode.divergence-swing-pad` | int | `2` | Padding around extrema for swing detection. |
| `scalp-mode.fast-trend-gate` | bool | `false` | Accepts trend signal if either tendency aligns OR MACD line is on the bullish/bearish side of zero. |
| `scalp-mode.tp-atr-multiplier` | float | `0.0` | If >0, take-profit becomes `max(takeProfit%, mult × ATR%)`. |
| `scalp-mode.sl-atr-multiplier` | float | `0.0` | If >0, stop-loss becomes `max(stopLoss%, mult × ATR%)`. Overrides `atr-multiplier` when set. |
| `scalp-mode.time-stop-bars` | int | `0` | Exits flat (P&L≥0 but TP not reached) positions after N closed bars of the trading interval (previously counted refresh ticks). |
| `scalp-mode.breakeven-atr-mult` | float | `0.0` | Once peak P&L ≥ mult × ATR%, pins the stop-loss to the entry price. |
| `scalp-mode.min-atr-pct` | float | `0.0` | Regime filter — refuse entries when ATR% is below this threshold (dead market). |
| `scalp-mode.max-atr-pct` | float | `0.0` | Regime filter — refuse entries when ATR% is above this threshold (chaotic market). |
| `scalp-mode.macd-peak-exit` | bool | `false` | Exits in profit when MACD histogram rolls over for 3 consecutive bars. |
| `scalp-mode.recent-extreme-bars` | int | `0` | Blocks BULL entries near a recent high (BEAR near recent low) over the given lookback. |
| `scalp-mode.max-hold-bars` | int | `0` | Unconditional time exit: closes the position after N closed bars regardless of P&L, so losing positions cannot bleed for hours. |
| `scalp-mode.breakeven-trail-atr-mult` | float | `0.0` | After break-even activates, trails the exit floor at `peak P&L − mult × ATR%` (never below net zero) instead of pinning it at net zero, letting winners run toward TP. |
| `scalp-mode.reentry-cooldown-bars` | int | `0` | Waits N closed bars after any exit before scanning for re-entry, preventing immediate same-price re-entries that only pay fees. |

### AI

```yaml
ai:
  enabled: true
  providers:
    openai:
      model: "gpt-4o-mini"
    deepseek:
      model: "deepseek-chat"
    claude:
      model: "claude-3-5-haiku-20241022"
  min-confidence: 0.5
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `ai.enabled` | bool | `true` | Enables AI-gated entries and AI-aware take-profit exits. |
| `ai.providers.openai.model` | string | `gpt-4o-mini` | OpenAI model used when `OPENAI_API_KEY` is set. |
| `ai.providers.deepseek.model` | string | `deepseek-chat` | DeepSeek model used when `DEEPSEEK_API_KEY` is set. |
| `ai.providers.claude.model` | string | `claude-3-5-haiku-20241022` | Claude model used when `ANTHROPIC_API_KEY` is set. |
| `ai.min-confidence` | float | `0.5` | Minimum consensus confidence from 0.0 to 1.0. |

Each available provider receives technical indicators plus sentiment data, returns `BUY`, `SELL`, or `HOLD`, and is folded into the weighted consensus. Set `ai.enabled: false` for lower-latency technical-only trading, especially in scalp profiles.

### Top Gainers

```yaml
top-gainers:
  quote-asset: "USDT"
  limit: 20
  poll-interval: 60
  min-volume: 1000000
  exclude-symbols:
    - "USDCUSDT"
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `top-gainers.quote-asset` | string | `USDT` | Only include symbols ending in this quote asset. |
| `top-gainers.limit` | int | `20` | Number of rows to show in the TUI. |
| `top-gainers.poll-interval` | int | `60` | Seconds between 24h ticker refreshes. |
| `top-gainers.min-volume` | float | `1000000` | Minimum 24h quote volume required for inclusion. |
| `top-gainers.exclude-symbols` | list | `["USDCUSDT"]` | Symbols to omit from the monitor. |

### Rotation

```yaml
rotation:
  bridge-asset: "USDT"
  current-asset: "BTC"
  supported-assets:
    - "BTC"
    - "ETH"
    - "SOL"
  scout-multiplier: 5
  scout-margin-pct: 0.8
  use-margin: false
  scout-sleep-seconds: 5
  dry-run: true
  max-jumps: 0
  min-notional-buffer: 1.01
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `rotation.bridge-asset` | string | `USDT` | Bridge asset used to compare and rotate between supported assets. |
| `rotation.current-asset` | string | `BTC` | Initial current asset; persisted state overrides this after the first run. |
| `rotation.supported-assets` | list | `BTC`, `ETH`, `SOL` | Asset basket scanned by rotation mode. |
| `rotation.scout-multiplier` | float | `5` | Fee multiplier for relative-ratio opportunity thresholds when `use-margin` is false. |
| `rotation.scout-margin-pct` | float | `0.8` | Margin percent required when `use-margin` is true. |
| `rotation.use-margin` | bool | `false` | Switches scout opportunity calculation to margin-percent mode. |
| `rotation.scout-sleep-seconds` | int | `5` | Seconds between scout loops. |
| `rotation.dry-run` | bool | `true` | Records opportunities without placing live rotation orders. |
| `rotation.max-jumps` | int | `0` | Maximum completed rotations; `0` means run until stopped. |
| `rotation.min-notional-buffer` | float | `1.01` | Multiplier applied when satisfying Binance minimum notional filters. |

Rotation mode persists its current asset in `data-dir/current_asset.json` and records scout comparisons in `data-dir/scouts.jsonl`.

### Futures

```yaml
futures:
  leverage: 2              # initial leverage per symbol (1-125, keep it low)
  margin-type: "isolated"  # isolated (losses capped per position) or crossed
  max-funding-pct: 0.0     # >0 → skip entries whose side would pay more than this funding %/interval
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `futures.leverage` | int | `2` | Initial leverage applied per symbol at session start. |
| `futures.margin-type` | string | `isolated` | `isolated` caps losses per position; `crossed` shares the wallet margin. |
| `futures.max-funding-pct` | float | `0.0` | Funding gate — skips entries whose side would pay more than this funding % per interval (longs pay when the rate is positive, shorts when negative). |

The futures strategy prices exit decisions on the **mark price** (the
liquidation engine's reference) when available, and applies the same
higher-timeframe trend gate as the spot strategies when `tendency.htf-enabled`
is set: LONG entries require the HTF trend to be up, SHORT entries down. The
trading tendency itself is evaluated on `tendency.interval` candles.

### Backtest And API

```yaml
backtest:
  initial-balance: 1000
  fee-pct: 0.1

api:
  address: "127.0.0.1:8080"
```

| Field | Type | Sample | Description |
|-------|------|--------|-------------|
| `backtest.initial-balance` | float | `1000` | Starting quote-asset balance for simulations. |
| `backtest.fee-pct` | float | `0.1` | Fee assumption for backtest trades; if zero, the default taker fee is used. |
| `api.address` | string | `127.0.0.1:8080` | Bind address for the local history API server. |

Backtests use recent Binance candles and append simulated trade records. The API server exposes persisted JSONL history from the configured address.

### File Logging

All log levels (orders, info, errors) are automatically written to a
per-session log file in the working directory, alongside the TUI display:
`binance-bot-<TICKER>.log` for trading sessions (e.g.
`binance-bot-DOGEUSDT.log`) and `binance-bot-topgainers.log` for the gainers
monitor. Color tags are stripped before writing. Files are opened in append
mode so logs accumulate across sessions, and concurrent instances never
interleave lines in one file.

```
2026-04-07 12:30:00 [INFO]  Scalp entry: score 5/6 (min 5)
2026-04-07 12:30:00 [INFO]    ✓ RSI 28.4 < 30 (upper limit)
2026-04-07 12:30:00 [INFO]    ✓ MACD histogram rising (0.000012 > 0.000008)
2026-04-07 12:30:00 [INFO]    ✓ Tendency up = up
2026-04-07 12:30:00 [INFO]    ✓ Closer to lower BB (lower=0.0023, upper=0.0089)
2026-04-07 12:30:00 [INFO]    ✓ ADX strong (32.1 > 20)
2026-04-07 12:30:00 [INFO]    ✗ Volume confirmed (4200 > avg 5100)
2026-04-07 12:30:01 [ORDER] BUY 50.000000 PEPE @ 0.00001234 USDT = 0.000617 USDT
2026-04-07 12:30:05 [INFO]  BUY order filled!
2026-04-07 12:35:10 [INFO]  Take-profit triggered: price 0.00001250 >= TP 0.00001246 (buy 0.00001234, TP 1.00%, P&L +1.30%)
2026-04-07 12:35:10 [INFO]    ✓ RSI exit ok (RSI declining=true, scalp bypass=false)
```

### Auto-Notional Adjustment

Binance enforces a minimum notional value (`price × quantity`) per symbol — typically 5 USDT. If the configured `--amount` would produce a notional below this threshold (common with very cheap tokens like PEPE or SHIB), the bot automatically raises the quantity to meet the exchange's `NOTIONAL` and `LOT_SIZE` filters before placing the order. A message is logged when an adjustment occurs:

```
BUY qty adjusted from 50.00000000 to 405210.00000000 to meet exchange filters (minNotional=5.00)
```

No manual intervention is required — the adjustment is transparent and logged. After any adjustment, the bot checks the relevant account balance before submitting the order.

---

## Trading Strategy Logic

### Bull-Trade

The `bull-trade` command is designed to operate during **bull market trends**, leveraging upward momentum to execute profitable trades.

#### **Buy Conditions**

In **classic mode**, the bot places a buy order when **all** of the following conditions are true simultaneously. In **scalp mode**, the conditions are scored and entry triggers when `min-score` is reached (see [Scalp Mode Configuration](#scalp-mode-configuration)). Scalp mode uses *pullback-in-trend RSI* (RSI rising from below mid-line) and *Bollinger price-touch* (close at/below lower band) — both designed to catch turning points earlier than the classic extreme thresholds.

1. **RSI**: Classic: value below `lower-limit` (default 30). Scalp: pullback-in-trend (RSI below mid-line and rising for 2 bars), plus optional SMA smoothing via `indicators.rsi.smooth-length`.
2. **MACD Momentum**: The MACD line crosses above the Signal line (classic) or the MACD histogram (`macd − signal`) is rising bar-over-bar (scalp). The scalp variant fires anticipatorily and can require N consecutive bars of confirmation via `indicators.macd.consecutive-bars`. When `indicators.macd.min-separation` is set, the signal additionally requires the histogram to have been at least that far below zero within the lookback window — i.e. MACD must have meaningfully diverged below signal before now closing the gap, filtering out flat-MACD noise.
3. **Tendency Confirmation**: The trend direction is "up" (DEMA above EMA). With `scalp-mode.fast-trend-gate`, MACD line above zero alternatively satisfies the trend.
4. **Bollinger Position**: Classic: DEMA closer to Lower than Upper Band. Scalp: close at/below the lower band (price touch).
5. **ADX Trend Strength** *(if configured)*: ADX is above the threshold (default 25), confirming a strong trend.
6. **Volume Confirmation** *(if configured)*: Current volume exceeds its MA. `scalp-mode.volume-strong-multiplier` awards a bonus when volume exceeds mult × MA.
7. **Bollinger Squeeze** *(scalp, opt-in)*: Bonus signal when BB width compresses below its rolling average (energy build-up).
8. **Divergence** *(scalp, opt-in)*: Bullish RSI divergence vs. price awards a weighted-×3 bonus.
9. **AI Consensus** *(if enabled)*: The multi-agent system must explicitly approve the entry at or above `ai.min-confidence`.

Scalp mode also applies two **gates** that can veto an entry outright before scoring:
- **Regime filter** (`min-atr-pct` / `max-atr-pct`): refuses trades when ATR% indicates a dead or chaotic market.
- **Recent-extreme block** (`recent-extreme-bars`): blocks BULL entries within 0.1×ATR of a recent high (BEAR mirror).

Before submitting the buy order, the bot verifies that the account has enough free quote-asset balance, such as USDT for `XRP/USDT`.

#### **Sell Conditions**
The bot will exit a position through one of five mechanisms:

1. **Trailing Stop-Loss** *(if enabled)*: After the price rises by `activation-pct` above buy price, the stop trails from the highest price. Triggers when price drops by `trailing-pct` from the peak.
2. **Fixed Stop-Loss**: The price drops to the stop-loss percentage below buy price (widened by `atr-stop-loss`/`sl-atr-multiplier` if configured; pinned to entry by `breakeven-atr-mult` once peak P&L is reached). Executes immediately.
3. **Time-Stop** *(scalp, opt-in)*: Exits flat positions (P&L ≥ 0, TP not reached) after `time-stop-bars` bars.
4. **MACD-Peak Exit** *(scalp, opt-in)*: Exits in profit when the MACD histogram rolls over (3-bar peak), locking gains before reversal.
5. **Take Profit**: The price reaches `effectiveTP = max(takeProfit%, tp-atr-multiplier × ATR%)` AND RSI is declining (skipped in scalp mode when `require-rsi-exit: false`) AND the AI supports the exit (if enabled).

Before submitting an exit sell order, the bot verifies that the account has enough free base-asset balance for the quantity being sold.

---

### Bear-Trade

The `bear-trade` command is designed to operate during **bear market trends**, profiting from downward price movement by selling high and buying back low.

#### **Sell Entry Conditions**

In **classic mode**, all conditions must be met simultaneously. In **scalp mode**, `min-score` out of 6 signals must be bearish.

The bot will open a short position (sell) when:

1. **RSI**: Value is above the configured `upper-limit` (default 70), indicating the market is overbought and ripe for a reversal downward.
2. **MACD Momentum**: The MACD line crosses below the Signal line (classic) or the MACD histogram (`macd − signal`) is falling bar-over-bar (scalp). The scalp variant fires anticipatorily: it does not require MACD to already be below signal, only that the gap is closing (or already-negative gap widening) — entering *before* the bearish crossover when momentum begins shifting down. When `indicators.macd.min-separation` is set, the signal additionally requires the histogram to have been at least that far above zero within the lookback (mirror of the bull rule).
3. **Tendency**: The trend direction is "down" (DEMA below EMA).
4. **DEMA Proximity to Bollinger Bands**: The current DEMA is closer to the Upper Band than the Lower Band, suggesting a potential reversal from overbought conditions.
5. **ADX Trend Strength** *(if configured)*: ADX confirms the trend has strength.
6. **Volume Confirmation** *(if configured)*: Current volume exceeds its moving average.
7. **AI Consensus** *(if enabled)*: The multi-agent system must explicitly approve the entry at or above `ai.min-confidence`.

Before submitting the sell entry, the bot verifies that the account has enough free base-asset balance, such as BTC for `BTC/USDT`.

#### **Buy-Back Exit Conditions**
The bot will exit the bear position (buy back) through one of five mechanisms (same set as bull, mirrored):

1. **Trailing Stop** *(if enabled)*: After the price drops by `activation-pct` below sell price, the stop trails from the lowest price. Triggers when price rises by `trailing-pct` from the trough.
2. **Fixed Stop-Loss**: The price rises to the stop-loss percentage above sell price (widened or break-even-pinned as in bull). Executes immediately.
3. **Time-Stop** *(scalp, opt-in)*: Exits flat positions after `time-stop-bars` bars.
4. **MACD-Peak Exit** *(scalp, opt-in)*: Exits in profit when the bearish MACD histogram rolls over.
5. **Take Profit**: The price drops to `effectiveTP = max(takeProfit%, tp-atr-multiplier × ATR%)` AND RSI is rising (skipped in scalp mode when `require-rsi-exit: false`) AND the AI supports the exit (if enabled).

Before submitting a buy-back order, the bot verifies that the account has enough free quote-asset balance for the estimated cost.

---

### Auto-Trade (Dynamic Tendency Detection)

The `auto-trade` command removes the need to manually choose between bull and bear strategies. Before each operation, the bot evaluates the current market tendency using the same DEMA-vs-EMA analysis used by the individual modes.

#### **How It Works**

1. **Strategy Selection**: The `--strategy` flag determines behavior:
   - `auto` (default): Detects tendency automatically and trades in whichever direction the market is trending.
   - `bull`: Forces buy-first operations — the bot waits until tendency is "up" before entering. Ideal when you only hold the quote asset (e.g., USDT).
   - `bear`: Forces sell-first operations — the bot waits until tendency is "down" before entering. Ideal when you hold the base asset and want to sell first.
2. **Tendency Detection**: At the start of each operation, the bot fetches historical prices on the configured `tendency.interval` and compares DEMA(`fast-length`) to EMA(`slow-length`). If DEMA > EMA for the last `confirm-bars` bars the tendency is "up" (bull); if DEMA < EMA it's "down" (bear); otherwise the tendency is unconfirmed and entries are blocked.
3. **Waiting for Match**: When a strategy is forced (`bull` or `bear`), the bot continuously monitors tendency and only proceeds when it matches the required direction. The TUI shows the mode with "(waiting)" until tendency aligns.
4. **Balance-Aware Mode Selection**: Based on the detected/matched tendency, the bot switches to the appropriate strategy only if the account can fund that entry. Bull mode requires enough free quote asset for the buy, such as USDT for `XRP/USDT`; bear mode requires enough free base asset for the sell, such as XRP for `XRP/USDT`.
5. **Live Re-detection**: During entry scanning in `auto` mode, if the tendency flips, the bot adapts only when the account can fund the new side. If the detected side cannot be funded, the bot keeps monitoring instead of exiting. In forced strategy mode, a tendency flip causes the bot to return to waiting.
6. **Entry & Exit**: Once a mode is selected, the exact same entry conditions (classic or scalp scoring) and exit mechanisms (trailing stop, stop-loss, take-profit, AI confirmation) apply as in the standalone `bull-trade` or `bear-trade` commands.
7. **Per-Operation Adaptation**: After each completed operation (entry + exit), the bot re-detects tendency before the next one.

#### **TUI Display**

The TUI header dynamically shows the current mode:
- `BULL (waiting)` or `BEAR (waiting)` when a forced strategy is waiting for matching tendency
- `BULL (waiting balance)` or `BEAR (waiting balance)` when tendency matches but the account lacks the free asset required for that entry
- `AUTO MODE` in cyan at startup (when strategy is auto)
- Switches to `BULL MODE` (green) or `BEAR MODE` (red) once tendency is detected/matched
- Updates in real-time if tendency flips during scanning

> **Tip**: The `auto-trade` command uses the same config file and flags as `bull-trade` / `bear-trade`. The bot determines direction automatically from the live tendency.

---

### AI Multi-Agent Decision Flow

When AI is enabled, the decision flow operates as follows:

```
Technical Indicators ──┐
                       ├──> AI Agents (concurrent) ──> Weighted Consensus ──> Trade Decision
Sentiment Data ────────┘       │         │         │
                          OpenAI    DeepSeek    Claude
```

- **Entry signals**: Technical conditions must pass first, then AI must explicitly approve with the matching signal (`BUY` for bull entry, `SELL` for bear entry) at or above `ai.min-confidence`. `HOLD`, low-confidence, malformed, or opposing AI output blocks new exposure.
- **Stop-loss / trailing-stop exits**: Execute **immediately** without waiting for AI — safety first.
- **Take-profit exits**: AI is allowed to block the exit only when it gives a confident opposite signal. `HOLD` and low-confidence output do not prevent taking profit once the technical exit checks pass.

> [!NOTE]
> The AI consensus is considered alongside — not instead of — the technical indicators. Both must agree for a trade to execute. This dual-confirmation approach reduces false signals while preserving protective exits.

---

## Build from Source

To build the `binance-bot` from the source code, ensure you have the following prerequisites installed:

#### **Prerequisites**
1. **Go (Golang):**
   - Install Go from the [official website](https://go.dev/).
   - Ensure your Go version is at least **1.19** by running:
     ```bash
     go version
     ```

2. **Git:**
   - Clone the repository using Git. Install Git from [here](https://git-scm.com/) if you don't already have it.

#### **Steps to Build**

1. Clone the repository:
   ```bash
   git clone https://github.com/wferreirauy/binance-bot.git
   cd binance-bot
   ```

2. Build the project:
   ```bash
   go build -o binance-bot
   ```

3. Verify the executable:
   ```bash
   ./binance-bot --help
   ```

If the build succeeds, you should see the general help menu displayed, indicating that the bot has been built successfully.

---

> [!WARNING]
> Always test the bot in a safe environment (e.g., testnet or small amounts) before live trading. Ensure you understand the risks and implications of using automated trading strategies.

## References

### Binance API documentation

https://binance-docs.github.io/apidocs/spot/en/#general-info

### Binance GO library

https://github.com/binance/binance-connector-go

### AI Provider APIs

- [OpenAI API](https://platform.openai.com/docs)
- [DeepSeek API](https://platform.deepseek.com/api-docs)
- [Anthropic Claude API](https://docs.anthropic.com/en/docs)

### Sentiment Data Sources

- [CryptoCompare News API](https://min-api.cryptocompare.com/) — Free, no API key required
- [Alternative.me Fear & Greed Index](https://alternative.me/crypto/fear-and-greed-index/) — Free, no API key required
