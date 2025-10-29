package alpha

type Candle struct {
	Time   int64
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
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

type Trade struct {
	Side  string
	Time  int64
	Price float64
	Size  int
}
