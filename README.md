# pm-sdk-go

`github.com/chainupcloud/pm-sdk-go` 是 [pm-cup2026](https://github.com/chainupcloud/pm-cup2026) 预测市场的 Go 客户端 SDK。

风格对标 [`github.com/GoPolymarket/polymarket-go-sdk`](https://github.com/GoPolymarket/polymarket-go-sdk)（顶层 `Client` 门面 + `With*` Options + `pkg/<module>` 子包 + `examples/`）。

## 状态

当前版本 **v0.1.0**（首个公开版本）。完整变更见 [`CHANGELOG.md`](CHANGELOG.md)。

权威接口契约源：<https://github.com/chainupcloud/pm-cup2026-liquidity/blob/main/docs/design-docs/pm-sdk-go-contract.md>。

完整 API 参考：[`docs/api.md`](docs/api.md)。

## 安装

```bash
go get github.com/chainupcloud/pm-sdk-go@v0.1.0
```

要求 Go 1.26 及以上（与 `chainupcloud/pm-cup2026` 主仓对齐）。

## 快速开始

```go
package main

import (
    "context"
    "log"
    "time"

    pmsdk "github.com/chainupcloud/pm-sdk-go"
)

func main() {
    cli, err := pmsdk.New(
        pmsdk.WithEndpoints(
            "https://clob.example.com",
            "https://gamma.example.com",
            "wss://ws.example.com",
        ),
        pmsdk.WithHTTPTimeout(10*time.Second),
        pmsdk.WithChainID(137),
        pmsdk.WithUserAgent("my-app/1.0"),
    )
    if err != nil {
        log.Fatal(err)
    }

    book, err := cli.Clob.GetBook(context.Background(), "0xtoken...")
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("bids=%d asks=%d", len(book.Bids), len(book.Asks))
}
```

下单等需要签名的调用请用 `pkg/signer.NewPMCup26Signer(privKey, scopeID, chainID, opts...)` 构造 signer，再通过 `pmsdk.WithSigner(sg)` 注入。完整端到端流程见 [`examples/full_flow`](examples/full_flow)。

更多示例：

| 示例 | 用途 |
|------|------|
| `examples/place_order` | clob `PlaceOrder` |
| `examples/cancel_order` | clob `CancelOrder` |
| `examples/list_events` | gamma `ListEvents` |
| `examples/subscribe_book` | ws `SubscribeBook` |
| `examples/full_flow` | signer + obs + 5 个 SDK 调用端到端 |

## OpenAPI 来源

`scripts/codegen.sh` 从 `chainupcloud/pm-cup2026:pm-v2` 拉以下 spec 生成 `pkg/clob/generated.go` / `pkg/gamma/generated.go`：

| 子包 | OpenAPI 路径 |
|------|--------------|
| `pkg/clob` | `services/clob-service/docs/openapi.yaml` |
| `pkg/gamma` | `services/gamma-service/api/openapi.yaml` |

## Codegen

```bash
# 本地（需 gh CLI 已登录可访问 chainupcloud/pm-cup2026 的账号）
./scripts/codegen.sh

# 也可用环境变量 token，无需 gh CLI
GH_TOKEN=ghp_xxx ./scripts/codegen.sh
```

不要手改 `pkg/{clob,gamma}/generated.go`；要改：(1) 改上游 spec；或 (2) 改 `codegen-config-{clob,gamma}.yaml`，再重跑脚本。CI 里 `codegen-drift` job 会拉同一份 spec 重跑，与仓内 generated 文件做 `git diff --exit-code` 校验漂移。

## 开发

```bash
go vet ./...
go test ./...
golangci-lint run
```

## License

[Apache License 2.0](LICENSE).
