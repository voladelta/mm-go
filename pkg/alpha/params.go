package alpha

import (
	"encoding/json"
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
