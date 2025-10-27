package main

import "math"

type Candle struct {
	Time   int64
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
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
	closeHistory []float64
	trWindow     []float64
	volWindow    []float64
	trSum        float64
	volSum       float64

	Efficiency float64
	IsBearish  bool
}

func NewMeIndicator(period int) *MeIndicator {
	return &MeIndicator{
		period: period,
	}
}

func (indi *MeIndicator) Process(c Candle) bool {
	if indi.period <= 0 {
		return false
	}

	tr := c.High - c.Low
	if len(indi.closeHistory) > 0 {
		prevClose := indi.closeHistory[len(indi.closeHistory)-1]
		tr = math.Max(tr, math.Abs(c.High-prevClose))
		tr = math.Max(tr, math.Abs(c.Low-prevClose))
	}

	indi.trWindow = append(indi.trWindow, tr)
	indi.trSum += tr
	if len(indi.trWindow) > indi.period {
		indi.trSum -= indi.trWindow[0]
		indi.trWindow = indi.trWindow[1:]
	}

	indi.volWindow = append(indi.volWindow, c.Volume)
	indi.volSum += c.Volume
	if len(indi.volWindow) > indi.period {
		indi.volSum -= indi.volWindow[0]
		indi.volWindow = indi.volWindow[1:]
	}

	indi.closeHistory = append(indi.closeHistory, c.Close)
	if len(indi.closeHistory) > indi.period+1 {
		indi.closeHistory = indi.closeHistory[len(indi.closeHistory)-(indi.period+1):]
	}

	if len(indi.closeHistory) <= indi.period {
		return false
	}
	if len(indi.trWindow) < indi.period || len(indi.volWindow) < indi.period {
		return false
	}

	priceChange := c.Close - indi.closeHistory[0]
	totalMovement := indi.trSum
	efficiency := 0.0
	if totalMovement > 0 {
		efficiency = math.Abs(priceChange) / totalMovement
	}

	volumeSMA := indi.volSum / float64(indi.period)
	volumeRatio := 0.0
	if volumeSMA > 0 {
		volumeRatio = c.Volume / volumeSMA
	}

	marketEfficiency := (efficiency * 0.7) + (min(volumeRatio, 3)/3.0)*0.3
	indi.Efficiency = min(marketEfficiency, 1.0)
	indi.IsBearish = priceChange < 0

	return true
}

type EmaIndicator struct {
	span       int
	window     []float64
	alpha      float64
	decay      float64
	ema        float64
	SlopeNorm  float64
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
		span:   span,
		window: make([]float64, 0, span),
	}
}

func (indi *EmaIndicator) Process(c Candle) bool {
	ema := c.Close*indi.alpha + indi.ema*indi.decay
	indi.ema = ema
	indi.window = append(indi.window, ema)
	indi.sum += ema
	indi.sumSquares += ema * ema

	if len(indi.window) < indi.span {
		return false
	}

	if len(indi.window) > indi.span {
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

	return true
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

func (s *MmStrat) Process(c Candle, inventory int) (bool, Quote) {
	emaOk := s.emaIndi.Process(c)
	meOk := s.meIndi.Process(c)

	if !emaOk || math.IsNaN(s.emaIndi.SlopeNorm) ||
		!meOk || math.IsNaN(s.meIndi.Efficiency) {
		return false, Quote{}
	}

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
	efficiency := s.meIndi.Efficiency

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
		if s.meIndi.IsBearish {
			baseTrend = -baseTrend
		}

		trendSignal := clampFloat(s.emaIndi.SlopeNorm+0.5*baseTrend+s.TrendBias, -1, 1)
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
