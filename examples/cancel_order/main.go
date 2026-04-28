// cancel_order 是 Phase 3 quickstart：演示如何撤一笔已存在的订单。
//
// 运行：
//
//	go run ./examples/cancel_order <order-id>
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	pmsdkgo "github.com/chainupcloud/pm-sdk-go"
	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cancel_order <order-id>")
		os.Exit(2)
	}
	id := clob.OrderID(os.Args[1])

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

	err = cli.Clob.CancelOrder(ctx, id)
	switch {
	case errors.Is(err, pmsdkgo.ErrNotFound):
		fmt.Println("order not found:", id)
		return
	case err != nil:
		fmt.Fprintln(os.Stderr, "cancel order:", err)
		os.Exit(1)
	}
	fmt.Printf("cancelled order: id=%s\n", id)
}
