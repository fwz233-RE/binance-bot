package exchange

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// USDT-M futures endpoint. The spot connector cannot speak /fapi, so the
// futures path uses this small purpose-built client instead of a new
// heavyweight dependency.
const FuturesMainnetURL = "https://fapi.binance.com"

// FuturesAPIKey/FuturesSecretKey fall back to the spot keys so users with
// unified API keys need no extra setup.
var (
	FuturesAPIKey    = envOr("BINANCE_FUTURES_API_KEY", APIKey)
	FuturesSecretKey = envOr("BINANCE_FUTURES_SECRET_KEY", SecretKey)
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// FuturesClient is a minimal signed REST client for Binance USDT-M futures.
// It reuses the package-level server-time offset so signed requests survive
// local clock drift, same as the spot client.
type FuturesClient struct {
	baseURL string
	http    *http.Client
}

// NewFuturesClient builds a mainnet futures client and lazily starts the
// shared server-time sync loop.
func NewFuturesClient() *FuturesClient {
	timeSyncOnce.Do(startTimeSync)
	return &FuturesClient{
		baseURL: FuturesMainnetURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// FuturesAPIError mirrors Binance's error payload so callers can branch on code.
type FuturesAPIError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (e *FuturesAPIError) Error() string {
	return fmt.Sprintf("<FuturesAPIError> code=%d, msg=%s", e.Code, e.Msg)
}

// IsFuturesCode reports whether err is a Binance futures API error with the given code.
func IsFuturesCode(err error, code int) bool {
	var apiErr *FuturesAPIError
	if err == nil {
		return false
	}
	if ok := errorsAs(err, &apiErr); ok {
		return apiErr.Code == code
	}
	return false
}

// errorsAs is a tiny local wrapper to keep the import surface small.
func errorsAs(err error, target **FuturesAPIError) bool {
	for err != nil {
		if e, ok := err.(*FuturesAPIError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// request performs one REST call. Signed requests get a drift-compensated
// timestamp and an HMAC-SHA256 signature, per Binance signing rules.
func (c *FuturesClient) request(method, path string, params url.Values, signed bool) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}
	if signed {
		// local minus offset ≈ Binance server clock
		ts := time.Now().UnixMilli() - timeOffset.Load()
		params.Set("timestamp", strconv.FormatInt(ts, 10))
		params.Set("recvWindow", "5000")
		mac := hmac.New(sha256.New, []byte(FuturesSecretKey))
		mac.Write([]byte(params.Encode()))
		params.Set("signature", hex.EncodeToString(mac.Sum(nil)))
	}

	endpoint := c.baseURL + path
	var req *http.Request
	var err error
	if method == http.MethodGet {
		req, err = http.NewRequest(method, endpoint+"?"+params.Encode(), nil)
	} else {
		req, err = http.NewRequest(method, endpoint, strings.NewReader(params.Encode()))
		if req != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("futures: build request: %w", err)
	}
	req.Header.Set("X-MBX-APIKEY", FuturesAPIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("futures: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("futures: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var apiErr FuturesAPIError
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != 0 {
			return nil, &apiErr
		}
		return nil, fmt.Errorf("futures: %s %s: HTTP %d: %s", method, path, resp.StatusCode, string(body))
	}
	return body, nil
}

// FuturesKlines fetches OHLCV candles from /fapi/v1/klines (public endpoint).
func (c *FuturesClient) FuturesKlines(symbol, interval string, limit int) (*OHLCV, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("interval", interval)
	params.Set("limit", strconv.Itoa(limit))
	body, err := c.request(http.MethodGet, "/fapi/v1/klines", params, false)
	if err != nil {
		return nil, err
	}
	var raw [][]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("futures: decode klines: %w", err)
	}
	ohlcv := &OHLCV{}
	for _, k := range raw {
		if len(k) < 6 {
			continue
		}
		o, err1 := klineField(k[1])
		h, err2 := klineField(k[2])
		l, err3 := klineField(k[3])
		cl, err4 := klineField(k[4])
		v, err5 := klineField(k[5])
		for _, e := range []error{err1, err2, err3, err4, err5} {
			if e != nil {
				return nil, fmt.Errorf("futures: parse kline: %w", e)
			}
		}
		ohlcv.Opens = append(ohlcv.Opens, o)
		ohlcv.Highs = append(ohlcv.Highs, h)
		ohlcv.Lows = append(ohlcv.Lows, l)
		ohlcv.Closes = append(ohlcv.Closes, cl)
		ohlcv.Volumes = append(ohlcv.Volumes, v)
	}
	return ohlcv, nil
}

func klineField(v any) (float64, error) {
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("unexpected kline field type %T", v)
	}
	return strconv.ParseFloat(s, 64)
}

// FuturesBalance returns the available balance for an asset (usually USDT).
func (c *FuturesClient) FuturesBalance(asset string) (float64, error) {
	body, err := c.request(http.MethodGet, "/fapi/v2/balance", url.Values{}, true)
	if err != nil {
		return 0, err
	}
	var balances []struct {
		Asset            string `json:"asset"`
		AvailableBalance string `json:"availableBalance"`
	}
	if err := json.Unmarshal(body, &balances); err != nil {
		return 0, fmt.Errorf("futures: decode balance: %w", err)
	}
	for _, b := range balances {
		if b.Asset == asset {
			return strconv.ParseFloat(b.AvailableBalance, 64)
		}
	}
	return 0, fmt.Errorf("futures: asset %s not found in balance", asset)
}

// SetLeverage sets the initial leverage for a symbol.
func (c *FuturesClient) SetLeverage(symbol string, leverage int) error {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("leverage", strconv.Itoa(leverage))
	_, err := c.request(http.MethodPost, "/fapi/v1/leverage", params, true)
	return err
}

// SetMarginType sets ISOLATED or CROSSED margin for a symbol. Binance returns
// -4046 when the margin type is already set; callers should tolerate it via
// IsFuturesCode(err, -4046).
func (c *FuturesClient) SetMarginType(symbol, marginType string) error {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("marginType", strings.ToUpper(marginType))
	_, err := c.request(http.MethodPost, "/fapi/v1/marginType", params, true)
	return err
}

// FuturesOrder is the subset of the order response the strategies need.
type FuturesOrder struct {
	OrderId     int64  `json:"orderId"`
	Status      string `json:"status"`
	AvgPrice    string `json:"avgPrice"`
	ExecutedQty string `json:"executedQty"`
	Side        string `json:"side"`
}

// FuturesMarketOrder places a MARKET order. reduceOnly=true guarantees the
// order can only close an existing position, never open or grow one — every
// exit path must use it so a retried close cannot accidentally flip the side.
func (c *FuturesClient) FuturesMarketOrder(symbol, side string, qty float64, reduceOnly bool) (*FuturesOrder, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", strings.ToUpper(side))
	params.Set("type", "MARKET")
	params.Set("quantity", strconv.FormatFloat(qty, 'f', -1, 64))
	params.Set("newOrderRespType", "RESULT")
	if reduceOnly {
		params.Set("reduceOnly", "true")
	}
	body, err := c.request(http.MethodPost, "/fapi/v1/order", params, true)
	if err != nil {
		return nil, err
	}
	var order FuturesOrder
	if err := json.Unmarshal(body, &order); err != nil {
		return nil, fmt.Errorf("futures: decode order: %w", err)
	}
	return &order, nil
}

// FuturesGetOrder queries an order's current state.
func (c *FuturesClient) FuturesGetOrder(symbol string, orderId int64) (*FuturesOrder, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("orderId", strconv.FormatInt(orderId, 10))
	body, err := c.request(http.MethodGet, "/fapi/v1/order", params, true)
	if err != nil {
		return nil, err
	}
	var order FuturesOrder
	if err := json.Unmarshal(body, &order); err != nil {
		return nil, fmt.Errorf("futures: decode order: %w", err)
	}
	return &order, nil
}

// FuturesTakerFeePct returns the account's taker commission rate for a symbol
// as a percentage (e.g. 0.05 for 0.05%), from /fapi/v1/commissionRate.
func (c *FuturesClient) FuturesTakerFeePct(symbol string) (float64, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	body, err := c.request(http.MethodGet, "/fapi/v1/commissionRate", params, true)
	if err != nil {
		return 0, err
	}
	var raw struct {
		TakerCommissionRate string `json:"takerCommissionRate"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, fmt.Errorf("futures: decode commission rate: %w", err)
	}
	rate, err := strconv.ParseFloat(raw.TakerCommissionRate, 64)
	if err != nil {
		return 0, fmt.Errorf("futures: parse commission rate: %w", err)
	}
	return rate * 100, nil
}

// FuturesPosition is the live position snapshot for one symbol (one-way mode).
type FuturesPosition struct {
	PositionAmt      float64 // signed: >0 long, <0 short, 0 flat
	EntryPrice       float64
	LiquidationPrice float64
	UnrealizedProfit float64
}

// FuturesGetPosition reads /fapi/v2/positionRisk for a symbol.
func (c *FuturesClient) FuturesGetPosition(symbol string) (*FuturesPosition, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	body, err := c.request(http.MethodGet, "/fapi/v2/positionRisk", params, true)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		LiquidationPrice string `json:"liquidationPrice"`
		UnRealizedProfit string `json:"unRealizedProfit"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("futures: decode position: %w", err)
	}
	if len(raw) == 0 {
		return &FuturesPosition{}, nil
	}
	p := raw[0]
	pos := &FuturesPosition{}
	pos.PositionAmt, _ = strconv.ParseFloat(p.PositionAmt, 64)
	pos.EntryPrice, _ = strconv.ParseFloat(p.EntryPrice, 64)
	pos.LiquidationPrice, _ = strconv.ParseFloat(p.LiquidationPrice, 64)
	pos.UnrealizedProfit, _ = strconv.ParseFloat(p.UnRealizedProfit, 64)
	return pos, nil
}