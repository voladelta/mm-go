package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

const SZ_EPSILON = 0.05 // 5%

// Pool for strings.Builder instances to reduce allocations
var builderPool = sync.Pool{
	New: func() any {
		return &strings.Builder{}
	},
}

type Binance struct {
	client    *fasthttp.Client
	apiKey    string
	secretKey string

	symbol      string
	szPrecision int
	pxPrecision int

	pxFactor  float64
	szFactor  float64
	szEpsilon float64
	tradeSz   float64
	pz        float64
}

func NewBinance(params *Params) *Binance {
	return &Binance{
		client:      &fasthttp.Client{},
		pxPrecision: params.PxPrecision,
		szPrecision: params.SzPrecision,
		pxFactor:    math.Pow10(params.PxPrecision),
		szFactor:    math.Pow10(params.SzPrecision),
		tradeSz:     params.TradeSz,
	}
}

func (b *Binance) Sync(symbol string) {
	b.symbol = symbol

	b.apiKey = strings.TrimSpace(os.Getenv("BINANCE_API_KEY"))
	if b.apiKey == "" {
		panic("BINANCE_API_KEY not set")
	}

	b.secretKey = strings.TrimSpace(os.Getenv("BINANCE_SECRET_KEY"))
	if b.secretKey == "" {
		panic("BINANCE_SECRET_KEY not set")
	}

	b.pz = b.getPz()

	go b.wsUser()
}

func (b *Binance) signHmac(data string) string {
	mac := hmac.New(sha256.New, []byte(b.secretKey))
	_, err := mac.Write([]byte(data))
	if err != nil {
		panic(err)
	}

	return fmt.Sprintf("%x", (mac.Sum(nil)))
}

func (b *Binance) Inventory() int {
	return int(math.Floor(b.pz / b.tradeSz))
}

func (b *Binance) Apply(quote Quote) {
	b.cancelOrders()
	fmt.Printf("%v\n", quote)
	if quote.BidActive && quote.BidSize > 0 && !math.IsNaN(quote.BidPrice) {
		go b.placeOrder(float64(quote.BidSize)*b.tradeSz, math.Floor(quote.BidPrice*b.pxFactor)/b.pxFactor)
	}
	if quote.AskActive && quote.AskSize > 0 && !math.IsNaN(quote.AskPrice) {
		go b.placeOrder(-float64(quote.AskSize)*b.tradeSz, math.Ceil(quote.AskPrice*b.pxFactor)/b.pxFactor)
	}
}

func (b *Binance) placeOrder(qty float64, px float64) {
	fmt.Printf("%f %f\n", qty, px)
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	builder.WriteString("type=LIMIT")
	builder.WriteString("&symbol=")
	builder.WriteString(b.symbol)
	builder.WriteString("&quantity=")
	if qty > 0 {
		builder.WriteString(strconv.FormatFloat(qty, 'f', b.szPrecision, 64))
		builder.WriteString("&side=BUY")
	} else {
		builder.WriteString(strconv.FormatFloat(-qty, 'f', b.szPrecision, 64))
		builder.WriteString("&side=SELL")
	}
	if px == 0 {
		builder.WriteString("&priceMatch=QUEUE")
		builder.WriteString("&timeInForce=GTC")
	} else {
		builder.WriteString("&price=")
		builder.WriteString(strconv.FormatFloat(px, 'f', b.pxPrecision, 64))
		builder.WriteString("&timeInForce=GTX")
	}
	builder.WriteString("&recvWindow=250")
	builder.WriteString("&timestamp=")
	builder.WriteString(strconv.FormatInt(time.Now().UnixMilli(), 10))

	totalParams := builder.String()
	signature := b.signHmac(totalParams)

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.SetRequestURI("https://fapi.binance.com/fapi/v1/order")
	req.Header.Set("X-MBX-APIKEY", b.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.SetMethod("POST")

	req.AppendBodyString(totalParams)
	req.AppendBodyString("&signature=")
	req.AppendBodyString(signature)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	err := b.client.Do(req, resp)
	if err != nil {
		panic(err)
	}

	body := resp.Body()
	msg := gjson.GetBytes(body, "msg")

	if msg.Exists() {
		code := gjson.GetBytes(body, "code").Int()
		slog.Error("PlaceOrder", "code", code, "msg", msg.Str, "params", totalParams)
		if code == -5022 || code == -5028 || code == -1008 {
			time.Sleep(500 * time.Millisecond)
			b.placeOrder(qty, 0)
		}

		// [TODO] might be mayday here
		return
	}
}

func (b *Binance) cancelOrders() {
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	builder.WriteString("symbol=")
	builder.WriteString(b.symbol)
	builder.WriteString("&recvWindow=500")
	builder.WriteString("&timestamp=")
	builder.WriteString(strconv.FormatInt(time.Now().UnixMilli(), 10))
	totalParams := builder.String()
	signature := b.signHmac(totalParams)

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.SetRequestURI("https://fapi.binance.com/fapi/v1/allOpenOrders")
	req.Header.Set("X-MBX-APIKEY", b.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.SetMethod("DELETE")

	req.AppendBodyString(totalParams)
	req.AppendBodyString("&signature=")
	req.AppendBodyString(signature)

	err := b.client.Do(req, nil)
	if err != nil {
		panic(err)
	}
}

func (b *Binance) getPz() float64 {
	builder := builderPool.Get().(*strings.Builder)
	builder.Reset()
	defer builderPool.Put(builder)

	builder.WriteString("symbol=")
	builder.WriteString(b.symbol)
	builder.WriteString("&recvWindow=500")
	builder.WriteString("&timestamp=")
	builder.WriteString(strconv.FormatInt(time.Now().UnixMilli(), 10))
	totalParams := builder.String()
	signature := b.signHmac(totalParams)

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.SetRequestURI("https://fapi.binance.com/fapi/v3/positionRisk?" + totalParams + "&signature=" + signature)
	req.Header.Set("X-MBX-APIKEY", b.apiKey)
	req.Header.SetMethod("GET")

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	err := b.client.Do(req, resp)
	if err != nil {
		panic(err)
	}

	body := resp.Body()
	msg := gjson.GetBytes(body, "msg")
	if msg.Exists() {
		panic(msg.Str)
	}

	return gjson.GetBytes(body, "0.positionAmt").Float()
}

func (b *Binance) getListenKey() string {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.SetRequestURI("https://fapi.binance.com/fapi/v1/listenKey")
	req.Header.Set("X-MBX-APIKEY", b.apiKey)
	req.Header.SetMethod("POST")

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	err := b.client.Do(req, resp)
	if err != nil {
		panic(err)
	}

	body := resp.Body()
	msg := gjson.GetBytes(body, "msg")
	if msg.Exists() {
		panic(msg.Str)
	}

	return gjson.GetBytes(body, "listenKey").Str
}

func (b *Binance) extendListenKey() {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.SetRequestURI("https://fapi.binance.com/fapi/v1/listenKey")
	req.Header.Set("X-MBX-APIKEY", b.apiKey)
	req.Header.SetMethod("PUT")

	err := b.client.Do(req, nil)
	if err != nil {
		panic(err)
	}
}

func (b *Binance) wsUser() {
	for {
		listenKey := b.getListenKey()

		// Extend listen key every 55 minutes
		ticker := time.NewTicker(55 * time.Minute)
		go func() {
			for range ticker.C {
				b.extendListenKey()
			}
		}()

		urlStr := "wss://fstream.binance.com/ws/" + listenKey
		c, _, err := websocket.DefaultDialer.Dial(urlStr, nil)
		if err != nil {
			panic(err)
		}

		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				slog.Error("wsUser", "ReadMessage", err)
				break
			}

			eventResult := gjson.GetBytes(message, "e")
			if !eventResult.Exists() {
				continue
			}

			if eventResult.Str == "ACCOUNT_UPDATE" {
				positions := gjson.GetBytes(message, "a.P")
				if !positions.Exists() {
					continue
				}

				for _, position := range positions.Array() {
					if position.Get("s").Str == b.symbol {
						b.pz = position.Get("pa").Float()
						break
					}
				}
			}
		}

		ticker.Stop()

		slog.Info("wsUser", "disconnected", "reconnect in a sec")
		time.Sleep(time.Second)
	}
}

func FetchKlines(symbol, interval string, limit int) []Candle {
	client := &fasthttp.Client{}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI("https://fapi.binance.com/fapi/v1/klines")
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
