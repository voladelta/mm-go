package bn

import (
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

func FetchKlines(symbol, interval string, limit int, endTime string) []alpha.Candle {
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

	jsonResult := gjson.ParseBytes(resp.Body())
	if !jsonResult.IsArray() {
		panic("unexpected kline response format")
	}

	candles := make([]alpha.Candle, limit)

	for i, v := range jsonResult.Array() {
		row := v.Array()

		candles[i] = alpha.Candle{
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

func WsKline(symbol string, onTick func(alpha.Candle)) {
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
			onTick(alpha.Candle{
				Time:   k.Get("t").Int(),
				Open:   k.Get("o").Float(),
				High:   k.Get("h").Float(),
				Low:    k.Get("l").Float(),
				Close:  k.Get("c").Float(),
				Volume: k.Get("v").Float(),
			})
		}

		slog.Info("WsBbo", "disconnected", "reconnect in a sec")
		time.Sleep(time.Second)
	}
}
