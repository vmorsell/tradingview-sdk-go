// Command quote_stream subscribes to one or more symbols and prints their
// last price as TradingView pushes updates.
//
// Usage:
//
//	go run ./examples/quote_stream BINANCE:BTCUSDT COINBASE:ETHEUR
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
	"github.com/vmorsell/tradingview-sdk-go/quote"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: quote_stream SYMBOL [SYMBOL...]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := tradingview.Connect(ctx)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	qs, err := client.NewQuoteSession(quote.WithFields(quote.FieldPresetPrice))
	if err != nil {
		log.Fatalf("quote session: %v", err)
	}
	for _, s := range os.Args[1:] {
		if err := qs.AddSymbol(s, quote.SessionRegular); err != nil {
			log.Fatalf("add %s: %v", s, err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-qs.Updates():
			if !ok {
				return
			}
			switch v := u.(type) {
			case quote.QuoteData:
				if lp, ok := v.Fields[quote.FieldLastPrice]; ok {
					fmt.Printf("%s %v\n", v.Symbol, lp)
				}
			case quote.QuoteError:
				log.Printf("quote error %s: %v", v.Symbol, v.Err)
			case quote.QuoteCompleted:
				log.Printf("snapshot complete: %s", v.Symbol)
			}
		}
	}
}
