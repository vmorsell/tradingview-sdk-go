// Command symbol_search looks up symbols by keyword (optionally filtered by
// exchange) and prints the top matches plus TradingView's TA snapshot for
// the first hit.
//
// Usage:
//
//	go run ./examples/symbol_search btc
//	go run ./examples/symbol_search binance:eth
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/vmorsell/tradingview-sdk-go"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: symbol_search QUERY")
	}
	query := os.Args[1]

	ctx := context.Background()
	results, err := tradingview.SearchSymbol(ctx, query)
	if err != nil {
		log.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		fmt.Println("no matches")
		return
	}
	limit := min(len(results), 10)
	fmt.Printf("%d matches (showing %d):\n", len(results), limit)
	for _, r := range results[:limit] {
		fmt.Printf("  %-28s %-10s %s\n", r.ID, r.Type, r.Description)
	}

	fmt.Printf("\nTA for %s:\n", results[0].ID)
	ta, err := tradingview.GetTA(ctx, results[0].ID)
	if err != nil {
		log.Printf("ta: %v", err)
		return
	}
	for _, tf := range []tradingview.Timeframe{
		tradingview.TF1m, tradingview.TF5m, tradingview.TF15m, tradingview.TF1h,
		tradingview.TF4h, tradingview.TF1D, tradingview.TF1W, tradingview.TF1M,
	} {
		p := ta[tf]
		fmt.Printf("  %-4s  Other=%+.2f  All=%+.2f  MA=%+.2f\n", tf, p.Other, p.All, p.MA)
	}
}
