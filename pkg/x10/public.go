package x10

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"mm/pkg/alpha"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

func GetMarketInfo(market string) *MarketModel {
	client := &fasthttp.Client{}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	baseURL := ApiEndpoint + "/info/markets?market=" + market
	req.SetRequestURI(baseURL)
	req.Header.SetMethod(fasthttp.MethodGet)

	err := client.Do(req, resp)
	if err != nil {
		panic(err)
	}

	var marketResponse struct {
		Data   []MarketModel `json:"data"`
		Status string        `json:"status"`
	}
	err = json.Unmarshal(resp.Body(), &marketResponse)

	if err != nil || marketResponse.Status != "OK" {
		panic(fmt.Errorf("API returned error status: %s", marketResponse.Status))
	}

	return &marketResponse.Data[0]
}

func FetchKlines(symbol, interval string, limit int, endTime string) []alpha.Candle {
	client := &fasthttp.Client{}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(fmt.Sprintf("%s/info/candles/%s/%s", ApiEndpoint, symbol, "trades"))
	req.Header.SetMethod(fasthttp.MethodGet)
	queryArgs := req.URI().QueryArgs()
	queryArgs.Set("symbol", symbol)
	queryArgs.Set("interval", interval)
	queryArgs.Set("limit", strconv.Itoa(min(limit, 1500)))
	if endTime != "" {
		t, err := time.Parse(time.RFC3339, endTime)
		if err != nil {
			panic(err)
		}
		queryArgs.Set("endTime", strconv.FormatInt(t.UnixMilli(), 10))
	}
	if err := client.Do(req, resp); err != nil {
		panic(err)
	}

	jsonResult := gjson.GetBytes(resp.Body(), "data")
	if !jsonResult.IsArray() {
		panic("unexpected kline response format")
	}

	candles := make([]alpha.Candle, limit)
	n := limit - 1
	for i, v := range jsonResult.Array() {
		candles[n-i] = alpha.Candle{
			Time:   v.Get("T").Int(),
			Open:   v.Get("o").Float(),
			High:   v.Get("h").Float(),
			Low:    v.Get("l").Float(),
			Close:  v.Get("c").Float(),
			Volume: v.Get("v").Float(),
		}
	}

	return candles
}

func WsKline(symbol, interval string, onTick func(alpha.Candle)) {
	wsURL := fmt.Sprintf("%s/candles/%s/%s?interval=PT%s", StreamEndpoint, symbol, "trades", strings.ToUpper(interval))

	for {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			panic(err)
		}

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				slog.Error("WsKline", "WebSocket read error", err)
				conn.Close()
				break
			}

			data := gjson.GetBytes(message, "data")
			if !data.IsArray() {
				continue
			}

			arr := data.Array()
			k := arr[len(arr)-1]
			onTick(alpha.Candle{
				Time:   k.Get("T").Int(),
				Open:   k.Get("o").Float(),
				High:   k.Get("h").Float(),
				Low:    k.Get("l").Float(),
				Close:  k.Get("c").Float(),
				Volume: k.Get("v").Float(),
			})
		}

		slog.Info("WsKline", "disconnected", "reconnect in a sec")
		time.Sleep(time.Second)
	}
}
