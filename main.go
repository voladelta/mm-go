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
	MeSpan         int     `json:"mePeriod"`
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

	EmaSlopeNorm         float64
	Efficiency           float64
	NormalizedEfficiency float64
	IsBullish            bool
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

func (pe *PaperEngine) ApplyFills(c Candle) []Trade {
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

func (pe *PaperEngine) FinalizeCandle(c Candle, quote Quote, fills []Trade) ResultRow {
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
		EmaSlopeNorm:         math.NaN(),
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

	return sc, true
}

type EmaIndicator struct {
	Span       int
	window     []float64
	alpha      float64
	decay      float64
	ema        float64
	sum        float64
	sumSquares float64
	lastMid    float64
	hasLastMid bool
}

func NewEmaIndicator(span int) *EmaIndicator {
	alpha := 2 / float64(span+1)
	return &EmaIndicator{
		alpha:  alpha,
		decay:  1 - alpha,
		Span:   span,
		window: make([]float64, 0, span),
	}
}

func (indi *EmaIndicator) Process(c Candle) (float64, bool) {
	ema := c.Close*indi.alpha + indi.ema*indi.decay
	indi.ema = ema
	indi.window = append(indi.window, ema)
	indi.sum += ema
	indi.sumSquares += ema * ema

	if len(indi.window) < indi.Span {
		return math.NaN(), false
	}

	if len(indi.window) > indi.Span {
		removed := indi.window[0]
		indi.window = indi.window[1:]
		indi.sum -= removed
		indi.sumSquares -= removed * removed
	}

	n := float64(len(indi.window))
	mean := indi.sum / n
	variance := (indi.sumSquares / n) - (mean * mean)
	if variance < 0 {
		variance = 0
	}

	stdDev := math.Sqrt(variance)

	slope := math.NaN()
	normSlope := math.NaN()
	if indi.hasLastMid {
		slope = mean - indi.lastMid
		denom := stdDev
		if denom == 0 {
			denom = math.Abs(indi.lastMid)
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

	indi.lastMid = mean
	indi.hasLastMid = true

	return normSlope, true
}

type MmStrat struct {
	meIndi         *MeIndicator
	emaIndi        *EmaIndicator
	BaseSpread     float64
	InventoryLimit int
	LotSize        int
	InventorySkewK float64
	TrendSkewK     float64
	TrendBias      float64
}

func NewMmStrat(params *Params) *MmStrat {
	return &MmStrat{
		meIndi:         NewMeIndicator(params.MeSpan),
		emaIndi:        NewEmaIndicator(params.EmaSpan),
		BaseSpread:     params.BaseSpread,
		InventoryLimit: params.InventoryLimit,
		LotSize:        params.LotSize,
		InventorySkewK: params.InventorySkewK,
		TrendSkewK:     params.TrendSkewK,
		TrendBias:      params.TrendBias,
	}
}

func (s *MmStrat) Process(candle Candle, inventory int) (bool, Quote) {
	emaSlopeNorm, emaOk := s.emaIndi.Process(candle)
	c, meOk := s.meIndi.Process(candle)

	if !emaOk || math.IsNaN(emaSlopeNorm) ||
		!meOk || math.IsNaN(c.NormalizedEfficiency) {
		return false, Quote{}
	}

	c.EmaSlopeNorm = emaSlopeNorm

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
		baseTrend := efficiency
		if !isBullish {
			baseTrend = -baseTrend
		}

		trendSignal := clampFloat(emaSlopeNorm+0.5*baseTrend+s.TrendBias, -1, 1)
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

	return true, quote
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
