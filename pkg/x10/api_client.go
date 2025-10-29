package x10

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

const ApiEndpoint string = "https://api.starknet.extended.exchange/api/v1"
const StreamEndpoint string = "ws://api.starknet.extended.exchange/stream.extended.exchange/v1"

type ApiClient struct {
	client       *fasthttp.Client
	starkAccount *StarkPerpetualAccount
}

func NewApiClient(starkAccount *StarkPerpetualAccount) *ApiClient {
	return &ApiClient{
		client:       &fasthttp.Client{},
		starkAccount: starkAccount,
	}
}

// ===== Order Operations =====

// OrderResponse represents the API response after order submission
type OrderResponse struct {
	Status string `json:"status"`
	Data   struct {
		OrderID    uint   `json:"id"`
		ExternalID string `json:"externalId"`
	}
}

func (c *ApiClient) SubmitOrder(order *PerpetualOrderModel) (*OrderResponse, error) {
	if order == nil {
		return nil, fmt.Errorf("order is nil")
	}

	orderReq, err := json.Marshal(order)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal order to JSON: %w", err)
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(ApiEndpoint + "/user/order")
	req.Header.SetMethod(fasthttp.MethodPost)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.starkAccount.apiKey)
	req.SetBody(orderReq)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	err = c.client.Do(req, resp)
	if err != nil {
		panic(err)
	}

	// Use the new DoRequest method to handle the HTTP request and JSON parsing
	var orderResponse OrderResponse
	err = json.Unmarshal(resp.Body(), &orderResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if orderResponse.Status != "OK" {
		return nil, fmt.Errorf("API returned error status: %v", orderResponse.Status)
	}

	if orderResponse.Data.ExternalID != order.ID {
		return nil, fmt.Errorf("mismatched order ID in response: got %s, expected %s", orderResponse.Data.ExternalID, order.ID)
	}

	return &orderResponse, nil
}

// MassCancel enables the cancellation of multiple orders by ID, by specific market, or for all orders within an account.
func (c *ApiClient) MassCancel(ctx context.Context, market string) bool {
	mcReq, err := json.Marshal(map[string]any{
		"markets":   []string{market},
		"cancelAll": true,
	})
	if err != nil {
		return false
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(ApiEndpoint + "/user/order/massCancel")
	req.Header.SetMethod(fasthttp.MethodPost)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.starkAccount.apiKey)
	req.SetBody(mcReq)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	err = c.client.Do(req, resp)
	if err != nil {
		panic(err)
	}

	status := gjson.GetBytes(resp.Body(), "status")
	return status.Str == "OK"
}

type L2ConfigModel struct {
	Type                 string `json:"type"`
	CollateralID         string `json:"collateralId"`
	CollateralResolution int64  `json:"collateralResolution"`
	SyntheticID          string `json:"syntheticId"`
	SyntheticResolution  int64  `json:"syntheticResolution"`
}

type MarketModel struct {
	Name                     string        `json:"name"`
	AssetName                string        `json:"assetName"`
	AssetPrecision           int           `json:"assetPrecision"`
	CollateralAssetName      string        `json:"collateralAssetName"`
	CollateralAssetPrecision int           `json:"collateralAssetPrecision"`
	Active                   bool          `json:"active"`
	L2Config                 L2ConfigModel `json:"l2Config"`
}

type StarknetDomain struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	ChainID  string `json:"chainId"`
	Revision string `json:"revision"`
}

// TradingFeeModel represents trading fees for a market
type TradingFeeModel struct {
	Market         string          `json:"market"`
	MakerFeeRate   decimal.Decimal `json:"makerFeeRate"`
	TakerFeeRate   decimal.Decimal `json:"takerFeeRate"`
	BuilderFeeRate decimal.Decimal `json:"builderFeeRate"`
}

var DefaultFees = TradingFeeModel{
	Market:         "BTC-USD",
	MakerFeeRate:   decimal.NewFromFloat(0.0002), // 2/10000 = 0.0002
	TakerFeeRate:   decimal.NewFromFloat(0.0005), // 5/10000 = 0.0005
	BuilderFeeRate: decimal.NewFromFloat(0),      // 0
}
