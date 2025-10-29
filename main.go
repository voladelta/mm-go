package main

import (
	"flag"
	"fmt"
	"mm/pkg/alpha"
	"mm/pkg/bn"
)

func main() {
	paramsFile := flag.String("p", "params.json", "Strategy parameters")
	showTrades := flag.Bool("s", false, "Show trades")
	isTesting := flag.Bool("t", false, "Backtest mode")
	flag.Parse()

	params := alpha.LoadParams(*paramsFile)
	fmt.Printf("Params loaded: %+v\n", params)

	strategy := alpha.NewMmStrat(params)
	paper := alpha.NewPaperEngine()

	fmt.Printf("Fetching data for %s (%s, limit=%d)...\n", params.Symbol, params.Interval, params.BarsCount)
	candles := bn.FetchKlines(params.Symbol, params.Interval, params.BarsCount, params.EndTime)
	barsCount := params.BarsCount - 1 // ignore last, incomplete bar
	for i := range barsCount {
		c := candles[i]
		fills := paper.ApplyFills(c)
		ok, quote := strategy.Process(c, paper.Inventory())
		if !ok {
			continue
		}
		row := paper.FinalizeCandle(c, quote, fills)
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

	trader := bn.NewBinance(params)
	trader.Sync(params.TradeSymbol)

	kline := candles[barsCount] // use last as prev bar
	bn.WsKline(params.Symbol, func(c alpha.Candle) {
		if c.Time > kline.Time {
			ok, quote := strategy.Process(kline, trader.Inventory())
			if ok {
				trader.Apply(quote)
			}
		}
		kline = c
	})
}
