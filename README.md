# pm-sdk-go

`github.com/chainupcloud/pm-sdk-go` 是 [pm-cup2026](https://github.com/chainupcloud/pm-cup2026) 预测市场的 Go 客户端 SDK。

风格对标 [`github.com/GoPolymarket/polymarket-go-sdk`](https://github.com/GoPolymarket/polymarket-go-sdk)（顶层 `Client` 门面 + `With*` Options + `pkg/<module>` 子包 + `examples/`）。

## 状态

> v0.1.0 开发中 — 当前仓内为 **Phase 1 骨架**：仅暴露顶层 Client / Options / 子包占位，业务方法尚未实现。

接口契约见 [`docs/contract.md`](docs/contract.md)（权威来源：`chainupcloud/pm-cup2026-liquidity` 仓 `docs/design-docs/pm-sdk-go-contract.md`）。

## 快速开始（Phase 1 占位）

```go
package main

import (
    "log"
    "time"

    pmsdk "github.com/chainupcloud/pm-sdk-go"
)

func main() {
    c, err := pmsdk.New(
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
    _ = c.Clob // Phase 3 起可用
    _ = c.Gamma
    _ = c.WS
}
```

实际业务调用（`PlaceOrder` / `SubscribeBook` 等）将随 Phase 3+ 落地。

## OpenAPI 来源

`scripts/codegen.sh`（Phase 2 实施）从 `chainupcloud/pm-cup2026:pm-v2` 拉取以下 spec 生成 `pkg/clob/generated.go` / `pkg/gamma/generated.go`：

| 子包 | OpenAPI 路径 |
|------|--------------|
| `pkg/clob` | `services/clob-service/docs/openapi.yaml` |
| `pkg/gamma` | `services/gamma-service/api/openapi.yaml` |

## 路线图

完整 8 phase 计划见 [`pm-cup2026-liquidity` 仓 sdk-agent-brief](https://github.com/chainupcloud/pm-cup2026-liquidity/blob/main/docs/sdk-agent-brief.md)。

| Phase | 目标 |
|-------|------|
| 1 | 仓初始化 + 顶层 `Client` / `Option` / 子包占位 + CI（**当前**） |
| 2 | `scripts/codegen.sh` 实拉 OpenAPI + `oapi-codegen` 输出 generated.go |
| 3 | `pkg/clob` 业务方法 + types + 错误模型 + examples |
| 4 | `pkg/gamma` 业务方法 + types + examples |
| 5 | `pkg/ws` 自动重连 / Sequence guard / `SubscribeBook` |
| 6 | `pkg/signer` EIP-712 + pmcup26 5-field auth |
| 7 | observability（zap / prometheus）+ contract test |
| 8 | `v0.1.0` tag + GitHub release |

## 开发

```bash
go vet ./...
go test ./...
golangci-lint run
```

要求 Go 1.26 及以上（与 `chainupcloud/pm-cup2026` 主仓对齐）。

## License

[Apache License 2.0](LICENSE).
