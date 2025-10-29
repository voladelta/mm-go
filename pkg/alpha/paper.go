package alpha

import "math"

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
	return pe.pnlHistory
}

func (pe *PaperEngine) Trades() []Trade {
	return pe.trades
}

func (pe *PaperEngine) Results() []ResultRow {
	return pe.results
}
