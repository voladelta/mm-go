package alpha

import "math"

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

		trendSignal := clampFloat(s.emaIndi.SlopeNorm+0.5*baseTrend-s.TrendBias, -1, 1)
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
