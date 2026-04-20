// Command simple_chart resolves a symbol and prints the last candle on
// every update.
//
// Usage:
//
//	go run ./examples/simple_chart BINANCE:BTCEUR D
//
// Exits on SIGINT.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/vmorsell/tradingview-sdk-go"
	"github.com/vmorsell/tradingview-sdk-go/chart"
)

func main() {
	if len(os.Args) < 3 {
		log.Fatal("usage: simple_chart SYMBOL TIMEFRAME")
	}
	symbol, timeframe := os.Args[1], os.Args[2]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := tradingview.Connect(ctx)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	cs, err := client.NewChartSession()
	if err != nil {
		log.Fatalf("chart session: %v", err)
	}
	if err := cs.SetMarket(symbol, chart.WithTimeframe(timeframe), chart.WithRange(200)); err != nil {
		log.Fatalf("set market: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-cs.Updates():
			if !ok {
				return
			}
			switch v := u.(type) {
			case chart.SymbolResolved:
				fmt.Printf("resolved: %s (%s)\n", v.Info.FullName, v.Info.Description)
			case chart.Candles:
				if len(v.Changed) == 0 {
					continue
				}
				c := v.Changed[len(v.Changed)-1]
				fmt.Printf("%s  O=%.4f  H=%.4f  L=%.4f  C=%.4f  V=%.2f\n",
					c.Time.Format("2006-01-02 15:04"),
					c.Open, c.High, c.Low, c.Close, c.Volume)
			case chart.ChartError:
				log.Printf("chart error (%s): %v", v.Kind, v.Err)
			}
		}
	}
}
