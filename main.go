package main

import (
	"flag"
	"fmt"
	"mm/pkg/alpha"
	"mm/pkg/bn"
	"mm/pkg/x10"
)

func main() {
	x10.WsKline("BTC-USD", "1M", func(c alpha.Candle) {
		fmt.Printf("%v\n", c)
	})

	paramsFile := flag.String("p", "params.json", "Strategy parameters")
	showTrades := flag.Bool("s", false, "Show trades")
	isTesting := flag.Bool("t", false, "Backtest mode")
	flag.Parse()

	params := alpha.LoadParams(*paramsFile)
	fmt.Printf("Params loaded: %+v\n", params)

	strategy := alpha.NewMmStrat(params)
	paper := alpha.NewPaperEngine()

	fmt.Printf("Fetching data for %s (%s, limit=%d)...\n", params.Symbol, params.Interval, params.Limit)
	candles := x10.FetchKlines(params.Symbol, params.Interval, params.Limit, params.EndTime)
	fmt.Printf("%v\n", candles[0])
	fmt.Printf("%v\n", candles[10])
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

	trader := bn.NewBinance(params)
	trader.Sync(params.TradeSymbol)

	bn.WsKline(params.Symbol, func(c alpha.Candle, b bool) {
		if b {
			ok, quote := strategy.Process(c, trader.Inventory())
			if !ok {
				return
			}
			trader.Apply(quote)
		}
	})
}
