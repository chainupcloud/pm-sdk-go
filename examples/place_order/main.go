// place_order 是 Phase 3 quickstart：演示如何用 *pmsdkgo.Client.Clob.PlaceOrder 下一笔限价单。
//
// 注意：本例仅演示 SDK 调用形态；真实下单需要 Phase 6 的 EIP-712 / pmcup26 5-field
// signer 才能通过 clob-service 鉴权，否则会返回 401（映射到 ErrSign）。
//
// 运行：
//
//	go run ./examples/place_order
//
// 不需要 endpoint 可达；本 example 只在编译期被 CI 校验通过。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	pmsdkgo "github.com/chainupcloud/pm-sdk-go"
	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
	"github.com/shopspring/decimal"
)

func main() {
	cli, err := pmsdkgo.New(
		pmsdkgo.WithEndpoints(
			"https://clob.example.com",
			"https://gamma.example.com",
			"wss://ws.example.com",
		),
		pmsdkgo.WithHTTPTimeout(10*time.Second),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init sdk:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, err := cli.Clob.PlaceOrder(ctx, clob.OrderReq{
		MarketID:    "0xmarketconditionid",
		TokenID:     "100200300",
		Side:        clob.SideBuy,
		OrderType:   clob.OrderTypeLimit,
		Price:       decimal.RequireFromString("0.55"),
		Size:        decimal.RequireFromString("10"),
		ClientOrder: "demo-001",
	})
	switch {
	case errors.Is(err, pmsdkgo.ErrSign):
		// 预期：本 example 未注入 signer，PlaceOrder 直接返回 ErrSign。
		fmt.Println("expected ErrSign (no signer wired in Phase 3 demo):", err)
		return
	case err != nil:
		fmt.Fprintln(os.Stderr, "place order:", err)
		os.Exit(1)
	}
	fmt.Printf("placed order: id=%s\n", id)
}
