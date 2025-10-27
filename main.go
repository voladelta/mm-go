package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

const binanceFuturesBaseURL = "https://fapi.binance.com"

type Params struct {
	Symbol         string  `json:"symbol"`
	Interval       string  `json:"interval"`
	Limit          int     `json:"limit"`
	MeSpan         int     `json:"meSpan"`
	EmaSpan        int     `json:"emaSpan"`
	BaseSpread     float64 `json:"baseSpread"`
	InventoryLimit int     `json:"inventoryLimit"`
	LotSize        int     `json:"lotSize"`
	InventorySkewK float64 `json:"inventorySkewK"`
	TrendSkewK     float64 `json:"trendSkewK"`
	TrendBias      float64 `json:"trendBias"`
	TradeSymbol    string  `json:"tradeSymbol"`
	TradeSz        float64 `json:"tradeSz"`
	PxPrecision    int     `json:"pxPrecision"`
	SzPrecision    int     `json:"szPrecision"`
}

func LoadParams(path string) *Params {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("LoadParams: unable to read %s: %v", path, err)
	}

	var params Params
	if err := json.Unmarshal(data, &params); err != nil {
		log.Fatalf("LoadParams: invalid JSON in %s: %v", path, err)
	}

	return &params
}

func main() {
	paramsFile := flag.String("p", "params.json", "Strategy parameters")
	showTrades := flag.Bool("s", false, "Show trades")
	isTesting := flag.Bool("t", false, "Backtest mode")
	flag.Parse()

	params := LoadParams(*paramsFile)
	fmt.Printf("Params loaded: %+v\n", params)

	strategy := NewMmStrat(params)
	paper := NewPaperEngine()

	fmt.Printf("Fetching data for %s (%s, limit=%d)...\n", params.Symbol, params.Interval, params.Limit)
	candles := FetchKlines(params.Symbol, params.Interval, params.Limit)

	for _, candle := range candles {
		fills := paper.ApplyFills(candle)
		ok, quote := strategy.Process(candle, paper.Inventory())
		if !ok {
			continue
		}
		row := paper.FinalizeCandle(candle, quote, fills)
		if *showTrades && len(fills) > 0 {
			fmt.Printf("\n%v\n%v\n---", row, fills)
		}
	}

	finalPnL := paper.FinalPnL()
	trades := paper.Trades()

	fmt.Printf("Final PnL: %.2f\n", finalPnL)
	fmt.Printf("Trades executed: %d\n", len(trades))

	if *isTesting {
		return
	}

	trader := NewBinance(params)
	trader.Sync(params.TradeSymbol)

	WsKline(params.Symbol, func(c Candle, b bool) {
		if b {
			ok, quote := strategy.Process(c, trader.Inventory())
			if !ok {
				return
			}
			trader.Apply(quote)
		}
	})
}

func FetchKlines(symbol, interval string, limit int) []Candle {
	client := &fasthttp.Client{}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(binanceFuturesBaseURL + "/fapi/v1/klines")
	req.Header.SetMethod(fasthttp.MethodGet)
	queryArgs := req.URI().QueryArgs()
	queryArgs.Set("symbol", symbol)
	queryArgs.Set("interval", interval)
	queryArgs.Set("limit", strconv.Itoa(min(limit, 1500)))

	if err := client.Do(req, resp); err != nil {
		panic(err)
	}

	jsonResult := gjson.ParseBytes(resp.Body())
	if !jsonResult.IsArray() {
		panic("unexpected kline response format")
	}

	candles := make([]Candle, limit)

	for i, v := range jsonResult.Array() {
		row := v.Array()

		candles[i] = Candle{
			Time:   row[0].Int(),
			Open:   row[1].Float(),
			High:   row[2].Float(),
			Low:    row[3].Float(),
			Close:  row[4].Float(),
			Volume: row[5].Float(),
		}
	}

	return candles
}

func WsKline(symbol string, onTick func(Candle, bool)) {
	wsURL := fmt.Sprintf("wss://fstream.binance.com/ws/%s@kline_1m", strings.ToLower(symbol))

	for {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			panic(err)
		}

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				slog.Error("WsBbo", "WebSocket read error", err)
				conn.Close()
				break
			}

			k := gjson.GetBytes(message, "k")
			onTick(Candle{
				Time:   k.Get("t").Int(),
				Open:   k.Get("o").Float(),
				High:   k.Get("h").Float(),
				Low:    k.Get("l").Float(),
				Close:  k.Get("c").Float(),
				Volume: k.Get("v").Float(),
			}, k.Get("x").Bool())
		}

		slog.Info("WsBbo", "disconnected", "reconnect in a sec")
		time.Sleep(time.Second)
	}
}
