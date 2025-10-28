package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

type Params struct {
	Symbol         string  `json:"symbol"`
	Interval       string  `json:"interval"`
	EndTime        string  `json:"endTime"`
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
	candles := FetchKlines(params.Symbol, params.Interval, params.Limit, params.EndTime)

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
