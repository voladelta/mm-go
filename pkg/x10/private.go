package x10

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math"
	"mm/pkg/alpha"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

type X10Trader struct {
	client *ApiClient
	market *MarketModel

	symbol      string
	szPrecision int
	pxPrecision int

	pxFactor float64
	szFactor float64
	tradeSz  float64
	pz       float64
}

func NewX10Trader(params *alpha.Params) *X10Trader {
	return &X10Trader{
		pxPrecision: params.PxPrecision,
		szPrecision: params.SzPrecision,
		pxFactor:    math.Pow10(params.PxPrecision),
		szFactor:    math.Pow10(params.SzPrecision),
		tradeSz:     params.TradeSz,
	}
}

func (t *X10Trader) Sync(symbol string) {
	apiKey := strings.TrimSpace(os.Getenv("X10_API_KEY"))
	if apiKey == "" {
		panic("X10_API_KEY not set")
	}

	publicKey := strings.TrimSpace(os.Getenv("X10_PUBLIC_KEY"))
	if apiKey == "" {
		panic("X10_PUBLIC_KEY not set")
	}

	privateKey := strings.TrimSpace(os.Getenv("X10_PRIVATE_KEY"))
	if apiKey == "" {
		panic("X10_PRIVATE_KEY not set")
	}

	vaultStr := strings.TrimSpace(os.Getenv("X10_VAULT"))
	if vaultStr == "" {
		panic("X10_VAULT not set")
	}

	vault, err := strconv.ParseUint(vaultStr, 10, 64)
	if err != nil {
		panic("X10_VAULT invalid")
	}

	account, err := NewStarkPerpetualAccount(vault, privateKey, publicKey, apiKey)
	if err != nil {
		log.Fatal("Failed to create account:", err)
	}

	t.client = NewApiClient(account)

	t.market = GetMarketInfo(symbol)

	go WsUser(apiKey, symbol, func(pz float64) {
		t.pz = pz
	})
}

func (b *X10Trader) Inventory() int {
	return int(math.Floor(b.pz / b.tradeSz))
}

func (b *X10Trader) Apply(quote alpha.Quote) {
	b.cancelOrders()

	if quote.BidActive && quote.BidSize > 0 && !math.IsNaN(quote.BidPrice) {
		go b.placeOrder(float64(quote.BidSize)*b.tradeSz, math.Floor(quote.BidPrice*b.pxFactor)/b.pxFactor)
	}
	if quote.AskActive && quote.AskSize > 0 && !math.IsNaN(quote.AskPrice) {
		go b.placeOrder(-float64(quote.AskSize)*b.tradeSz, math.Ceil(quote.AskPrice*b.pxFactor)/b.pxFactor)
	}
}

func (b *X10Trader) placeOrder(sz, px float64) {
	nonce := int(time.Now().Unix()) // Use timestamp as nonce for uniqueness
	expireTime := time.Now().Add(5 * time.Minute)

	side := OrderSideBuy
	if sz < 0 {
		sz = -sz
		side = OrderSideSell
	}
	params := CreateOrderObjectParams{
		Market:          *b.market,
		Account:         *b.client.starkAccount,
		SyntheticAmount: decimal.NewFromFloat(sz),
		Price:           decimal.NewFromFloat(px),
		Side:            side,
		Signer:          b.client.starkAccount.Sign,
		StarknetDomain: StarknetDomain{
			Name:     "Perpetuals",
			Version:  "v0",
			ChainID:  "SN_MAIN",
			Revision: "1",
		},
		ExpireTime:               &expireTime,
		PostOnly:                 true,
		TimeInForce:              TimeInForceGTT,
		SelfTradeProtectionLevel: SelfTradeProtectionDisabled,
		Nonce:                    &nonce,
	}

	// Create the order object
	order, err := CreateOrderObject(params)
	if err != nil {
		panic(fmt.Errorf("failed to create order: %w", err))
	}
	b.client.SubmitOrder(order)
}

func (t *X10Trader) cancelOrders() {
	ctx := context.Background()
	t.client.MassCancel(ctx, t.market.Name)
}

func WsUser(apiKey, market string, onPz func(pz float64)) {
	urlStr := StreamEndpoint + "/account"

	requestHeader := http.Header{}
	requestHeader.Add("X-Api-Key", apiKey)

	for {
		conn, _, err := websocket.DefaultDialer.Dial(urlStr, requestHeader)
		if err != nil {
			panic(err)
		}

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				slog.Error("WsUser", "WebSocket read error", err)
				conn.Close()
				break
			}

			t := gjson.GetBytes(message, "type")
			if !t.Exists() || t.Str != "POSITION" {
				continue
			}

			positions := gjson.GetBytes(message, "data.positions")
			for _, p := range positions.Array() {
				if p.Get("market").Str == market {
					var pz float64
					if p.Get("status").Str == "OPENED" {
						pz = p.Get("size").Float()
						if p.Get("side").Str == "SHORT" {
							pz = -pz
						}
					}

					onPz(pz)
					break
				}
			}
		}

		slog.Info("WsUser", "disconnected", "reconnect in a sec")
		time.Sleep(time.Second)
	}
}
