package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math"
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
	Period         int     `json:"period"`
	BandPeriod     int     `json:"bandPeriod"`
	BandMultiplier float64 `json:"bandMultiplier"`
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

type Candle struct {
	Time   int64
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

type StrategyCandle struct {
	Candle
	Efficiency           float64
	NormalizedEfficiency float64
	IsBullish            bool
	HasEfficiency        bool
	BandMid              float64
	BandUpper            float64
	BandLower            float64
	BandWidth            float64
	BandZ                float64
	BandMidSlope         float64
	BandMidSlopeNorm     float64
	HasBollinger         bool
}

type Trade struct {
	Side  string
	Time  int64
	Price float64
	Size  int
}

type Quote struct {
	Time      int64
	BidPrice  float64
	BidSize   int
	BidActive bool
	AskPrice  float64
	AskSize   int
	AskActive bool
	Valid     bool
}

type Order struct {
	Side     string
	Price    float64
	Size     int
	PlacedAt int64
}

type PaperEngine struct {
	inventory     int
	cash          float64
	pendingOrders []Order
	pnlHistory    []float64
	trades        []Trade
	results       []ResultRow
	lastClose     float64
}

func NewPaperEngine() *PaperEngine {
	return &PaperEngine{
		pendingOrders: make([]Order, 0),
		pnlHistory:    make([]float64, 0),
		trades:        make([]Trade, 0),
		results:       make([]ResultRow, 0),
	}
}

func (pe *PaperEngine) Inventory() int {
	return pe.inventory
}

func (pe *PaperEngine) ApplyFills(c StrategyCandle) []Trade {
	if len(pe.pendingOrders) == 0 {
		return nil
	}

	fills := make([]Trade, 0, len(pe.pendingOrders))
	for _, order := range pe.pendingOrders {
		switch order.Side {
		case "buy":
			if c.Low <= order.Price {
				pe.inventory += order.Size
				pe.cash -= order.Price * float64(order.Size)
				trade := Trade{
					Side:  "buy",
					Time:  c.Time,
					Price: order.Price,
					Size:  order.Size,
				}
				pe.trades = append(pe.trades, trade)
				fills = append(fills, trade)
			}
		case "sell":
			if c.High >= order.Price {
				pe.inventory -= order.Size
				pe.cash += order.Price * float64(order.Size)
				trade := Trade{
					Side:  "sell",
					Time:  c.Time,
					Price: order.Price,
					Size:  order.Size,
				}
				pe.trades = append(pe.trades, trade)
				fills = append(fills, trade)
			}
		}
	}

	pe.pendingOrders = pe.pendingOrders[:0]
	return fills
}

func (pe *PaperEngine) FinalizeCandle(c StrategyCandle, quote Quote, fills []Trade) ResultRow {
	currentPnL := pe.cash + float64(pe.inventory)*c.Close
	pe.pnlHistory = append(pe.pnlHistory, currentPnL)

	signal := 0.0
	switch {
	case pe.inventory > 0:
		signal = 1.0
	case pe.inventory < 0:
		signal = -1.0
	}

	row := ResultRow{
		Time:          c.Time,
		Close:         c.Close,
		Bid:           math.NaN(),
		Ask:           math.NaN(),
		Efficiency:    c.NormalizedEfficiency,
		Inventory:     pe.inventory,
		Signal:        signal,
		Cash:          pe.cash,
		CumulativePnL: currentPnL,
	}

	for _, fill := range fills {
		switch fill.Side {
		case "buy":
			row.BuyFillPrice = fill.Price
			row.HasBuyFill = true
		case "sell":
			row.SellFillPrice = fill.Price
			row.HasSellFill = true
		}
	}

	if quote.Valid && quote.BidActive {
		row.Bid = quote.BidPrice
	}
	if quote.Valid && quote.AskActive {
		row.Ask = quote.AskPrice
	}

	if quote.Valid {
		newOrders := make([]Order, 0, 2)
		if quote.BidActive && quote.BidSize > 0 && !math.IsNaN(quote.BidPrice) {
			newOrders = append(newOrders, Order{
				Side:     "buy",
				Price:    quote.BidPrice,
				Size:     quote.BidSize,
				PlacedAt: quote.Time,
			})
		}
		if quote.AskActive && quote.AskSize > 0 && !math.IsNaN(quote.AskPrice) {
			newOrders = append(newOrders, Order{
				Side:     "sell",
				Price:    quote.AskPrice,
				Size:     quote.AskSize,
				PlacedAt: quote.Time,
			})
		}
		pe.pendingOrders = newOrders
	} else {
		pe.pendingOrders = pe.pendingOrders[:0]
	}

	pe.results = append(pe.results, row)
	pe.lastClose = c.Close

	return row
}

func (pe *PaperEngine) FinalPnL() float64 {
	return pe.cash + float64(pe.inventory)*pe.lastClose
}

func (pe *PaperEngine) PnLHistory() []float64 {
	return append([]float64(nil), pe.pnlHistory...)
}

func (pe *PaperEngine) Trades() []Trade {
	return append([]Trade(nil), pe.trades...)
}

func (pe *PaperEngine) Results() []ResultRow {
	return append([]ResultRow(nil), pe.results...)
}

type ResultRow struct {
	Time          int64
	Close         float64
	Bid           float64
	Ask           float64
	Efficiency    float64
	Inventory     int
	Signal        float64
	Cash          float64
	CumulativePnL float64
	BuyFillPrice  float64
	HasBuyFill    bool
	SellFillPrice float64
	HasSellFill   bool
}

type MeIndicator struct {
	period       int
	trWindow     []float64
	volWindow    []float64
	trSum        float64
	volSum       float64
	closeHistory []float64
}

func NewMeIndicator(period int) *MeIndicator {
	return &MeIndicator{
		period: period,
	}
}

func (ec *MeIndicator) Process(c Candle) (StrategyCandle, bool) {
	sc := StrategyCandle{
		Candle:               c,
		Efficiency:           math.NaN(),
		NormalizedEfficiency: math.NaN(),
		BandMid:              math.NaN(),
		BandUpper:            math.NaN(),
		BandLower:            math.NaN(),
		BandWidth:            math.NaN(),
		BandZ:                math.NaN(),
		BandMidSlope:         math.NaN(),
		BandMidSlopeNorm:     math.NaN(),
	}

	if ec.period <= 0 {
		return sc, false
	}

	tr := c.High - c.Low
	if len(ec.closeHistory) > 0 {
		prevClose := ec.closeHistory[len(ec.closeHistory)-1]
		tr = math.Max(tr, math.Abs(c.High-prevClose))
		tr = math.Max(tr, math.Abs(c.Low-prevClose))
	}

	ec.trWindow = append(ec.trWindow, tr)
	ec.trSum += tr
	if len(ec.trWindow) > ec.period {
		ec.trSum -= ec.trWindow[0]
		ec.trWindow = ec.trWindow[1:]
	}

	ec.volWindow = append(ec.volWindow, c.Volume)
	ec.volSum += c.Volume
	if len(ec.volWindow) > ec.period {
		ec.volSum -= ec.volWindow[0]
		ec.volWindow = ec.volWindow[1:]
	}

	ec.closeHistory = append(ec.closeHistory, c.Close)
	if len(ec.closeHistory) > ec.period+1 {
		ec.closeHistory = ec.closeHistory[len(ec.closeHistory)-(ec.period+1):]
	}

	if len(ec.closeHistory) <= ec.period {
		return sc, false
	}
	if len(ec.trWindow) < ec.period || len(ec.volWindow) < ec.period {
		return sc, false
	}

	priceChange := c.Close - ec.closeHistory[0]
	totalMovement := ec.trSum
	efficiency := 0.0
	if totalMovement > 0 {
		efficiency = math.Abs(priceChange) / totalMovement
	}

	volumeSMA := ec.volSum / float64(ec.period)
	volumeRatio := 0.0
	if volumeSMA > 0 {
		volumeRatio = c.Volume / volumeSMA
	}

	marketEfficiency := (efficiency * 0.7) + (math.Min(volumeRatio, 3)/3.0)*0.3
	normalized := math.Min(marketEfficiency, 1.0)

	sc.Efficiency = efficiency
	sc.NormalizedEfficiency = normalized
	sc.IsBullish = priceChange > 0
	sc.HasEfficiency = true

	return sc, true
}

type BollingerBands struct {
	Mid          float64
	Upper        float64
	Lower        float64
	Width        float64
	ZScore       float64
	MidSlope     float64
	MidSlopeNorm float64
}

type BollingerIndicator struct {
	period     int
	multiplier float64
	window     []float64
	sum        float64
	sumSquares float64
	lastMid    float64
	hasLastMid bool
}

func NewBollingerIndicator(period int, multiplier float64) *BollingerIndicator {
	return &BollingerIndicator{
		period:     period,
		multiplier: multiplier,
		window:     make([]float64, 0, period),
	}
}

func (bi *BollingerIndicator) Process(c Candle) (BollingerBands, bool) {
	bands := BollingerBands{
		Mid:          math.NaN(),
		Upper:        math.NaN(),
		Lower:        math.NaN(),
		Width:        math.NaN(),
		ZScore:       math.NaN(),
		MidSlope:     math.NaN(),
		MidSlopeNorm: math.NaN(),
	}

	if bi == nil || bi.period <= 1 {
		return bands, false
	}

	closePx := c.Close
	bi.window = append(bi.window, closePx)
	bi.sum += closePx
	bi.sumSquares += closePx * closePx

	if len(bi.window) > bi.period {
		removed := bi.window[0]
		bi.window = bi.window[1:]
		bi.sum -= removed
		bi.sumSquares -= removed * removed
	}

	if len(bi.window) < bi.period {
		return bands, false
	}

	n := float64(len(bi.window))
	mean := bi.sum / n
	variance := (bi.sumSquares / n) - (mean * mean)
	if variance < 0 {
		variance = 0
	}

	stdDev := math.Sqrt(variance)
	mult := bi.multiplier

	upper := mean
	lower := mean
	if mult > 0 && stdDev > 0 {
		offset := mult * stdDev
		upper = mean + offset
		lower = mean - offset
		bands.Width = 2 * offset
	} else {
		bands.Width = 0
	}

	z := 0.0
	if mult > 0 && stdDev > 0 {
		denom := mult * stdDev
		if denom > 0 {
			z = clampFloat((closePx-mean)/denom, -1, 1)
		}
	}

	slope := math.NaN()
	normSlope := math.NaN()
	if bi.hasLastMid {
		slope = mean - bi.lastMid
		denom := stdDev
		if denom == 0 {
			denom = math.Abs(bi.lastMid)
			if denom == 0 {
				denom = math.Abs(mean)
			}
		}
		if denom != 0 {
			normSlope = slope / denom
			if !math.IsNaN(normSlope) {
				normSlope = clampFloat(normSlope, -1, 1)
			}
		} else {
			normSlope = 0
		}
	}

	bands.Mid = mean
	bands.Upper = upper
	bands.Lower = lower
	bands.ZScore = z
	bands.MidSlope = slope
	bands.MidSlopeNorm = normSlope

	bi.lastMid = mean
	bi.hasLastMid = true

	return bands, true
}

type MmStrat struct {
	BaseSpread     float64
	InventoryLimit int
	LotSize        int
	InventorySkewK float64
	TrendSkewK     float64
	TrendBias      float64
}

func NewMmStrat(params *Params) *MmStrat {
	return &MmStrat{
		BaseSpread:     params.BaseSpread,
		InventoryLimit: params.InventoryLimit,
		LotSize:        params.LotSize,
		InventorySkewK: params.InventorySkewK,
		TrendSkewK:     params.TrendSkewK,
		TrendBias:      params.TrendBias,
	}
}

func (s *MmStrat) Process(c StrategyCandle, inventory int) Quote {
	quote := Quote{
		Time:      c.Time,
		BidPrice:  math.NaN(),
		AskPrice:  math.NaN(),
		BidSize:   s.LotSize,
		AskSize:   s.LotSize,
		BidActive: false,
		AskActive: false,
		Valid:     false,
	}

	if !c.HasEfficiency || math.IsNaN(c.NormalizedEfficiency) {
		return quote
	}

	closePrice := c.Close
	efficiency := c.NormalizedEfficiency
	isBullish := c.IsBullish

	spread := s.BaseSpread * closePrice * (1 + efficiency*2)
	halfSpread := spread / 2

	mid := closePrice
	bid := mid - halfSpread
	ask := mid + halfSpread

	if s.InventoryLimit > 0 && s.InventorySkewK != 0 {
		invDenominator := float64(s.InventoryLimit)
		if invDenominator != 0 {
			invFrac := float64(inventory) / invDenominator
			invFrac = clampFloat(invFrac, -1, 1)
			invShift := s.InventorySkewK * invFrac * halfSpread
			bid -= invShift
			ask -= invShift
		}
	}

	if s.TrendSkewK != 0 {
		trendSignal := 0.0
		slopeUsed := false
		if c.HasBollinger && !math.IsNaN(c.BandMidSlopeNorm) {
			trendSignal = c.BandMidSlopeNorm
			slopeUsed = true
		}
		if c.HasEfficiency && !math.IsNaN(efficiency) {
			baseTrend := efficiency
			if !isBullish {
				baseTrend = -baseTrend
			}
			if slopeUsed {
				trendSignal = clampFloat(trendSignal+0.5*baseTrend+s.TrendBias, -1, 1)
			} else {
				trendSignal = baseTrend
			}
		}
		trendShift := s.TrendSkewK * trendSignal * halfSpread
		bid -= trendShift
		ask -= trendShift
	}

	quote.BidPrice = bid
	quote.AskPrice = ask
	quote.Valid = true

	if s.InventoryLimit == 0 || absInt(inventory+s.LotSize) <= s.InventoryLimit {
		quote.BidActive = true
	} else {
		quote.BidPrice = math.NaN()
	}

	if s.InventoryLimit == 0 || absInt(inventory-s.LotSize) <= s.InventoryLimit {
		quote.AskActive = true
	} else {
		quote.AskPrice = math.NaN()
	}

	return quote
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func main() {
	paramsFile := flag.String("p", "params.json", "Strategy parameters")
	showTrades := flag.Bool("s", false, "Show trades")
	isTesting := flag.Bool("t", false, "Backtest mode")
	flag.Parse()

	params := LoadParams(*paramsFile)
	fmt.Printf("Params loaded: %+v\n", params)

	meIndicator := NewMeIndicator(params.Period)
	bandPeriod := params.BandPeriod
	if bandPeriod <= 0 {
		bandPeriod = params.Period
	}
	bandMultiplier := params.BandMultiplier
	if bandMultiplier <= 0 {
		bandMultiplier = 2.0
	}
	bollIndicator := NewBollingerIndicator(bandPeriod, bandMultiplier)
	strategy := NewMmStrat(params)
	paper := NewPaperEngine()

	fmt.Printf("Fetching data for %s (%s, limit=%d)...\n", params.Symbol, params.Interval, params.Limit)
	candles := FetchKlines(params.Symbol, params.Interval, params.Limit)

	for _, candle := range candles {
		bands, hasBands := bollIndicator.Process(candle)
		if sc, ok := meIndicator.Process(candle); ok {
			if hasBands {
				sc.BandMid = bands.Mid
				sc.BandUpper = bands.Upper
				sc.BandLower = bands.Lower
				sc.BandWidth = bands.Width
				sc.BandZ = bands.ZScore
				sc.BandMidSlope = bands.MidSlope
				sc.BandMidSlopeNorm = bands.MidSlopeNorm
				sc.HasBollinger = true
			}
			fills := paper.ApplyFills(sc)
			quote := strategy.Process(sc, paper.Inventory())
			row := paper.FinalizeCandle(sc, quote, fills)
			if len(fills) > 0 && *showTrades {
				fmt.Printf("\n%v\n%v\n---", row, fills)
			}
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
			bands, hasBands := bollIndicator.Process(c)
			if sc, ok := meIndicator.Process(c); ok {
				if hasBands {
					sc.BandMid = bands.Mid
					sc.BandUpper = bands.Upper
					sc.BandLower = bands.Lower
					sc.BandWidth = bands.Width
					sc.BandZ = bands.ZScore
					sc.BandMidSlope = bands.MidSlope
					sc.BandMidSlopeNorm = bands.MidSlopeNorm
					sc.HasBollinger = true
				}
				quote := strategy.Process(sc, trader.Inventory())
				trader.Apply(quote)
			}
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
