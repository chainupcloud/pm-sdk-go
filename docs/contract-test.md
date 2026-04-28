# Contract Test

> 录回放（record-and-replay）方式，把 pm-cup2026 staging 真实请求/响应作为 fixture 跑回归。

## 设计

- Build tag `contract`：默认 `go test ./...` **不跑**，只在 `go test -tags contract ./...` 时编译执行
- Fixture 存 `testdata/contract-fixtures/*.json`，每个文件一个 case
- 框架：`internal/contracttest` 提供 `Load(t, path)` 与 `NewMockServer(t, fx)`
- 回放：起 `httptest.Server` 按 fixture 校验 method/path 并写出 fixture 里的 response body
- 上层：`pkg/clob/contract_test.go` / `pkg/gamma/contract_test.go` 用 mock server 构造 SDK Facade，跑业务方法，断言返回值字段非空 + 形态正确

## 运行

```bash
# 默认（跳过 contract test）
go test ./...

# 显式跑 contract test
go test -tags contract ./...

# 单包
go test -tags contract ./pkg/clob/...
```

CI 在 `contract-tests` job 里跑，结果可见但不阻塞主 checks。

## Fixture 格式

每个 fixture 是单 case JSON：

```json
{
  "name": "human-readable case name",
  "comment": "optional notes",
  "request": {
    "method": "POST",
    "path": "/order",
    "query": {"key": "value"},
    "body": {}
  },
  "response": {
    "status": 200,
    "headers": {"Content-Type": "application/json"},
    "body": { ... }
  }
}
```

校验严格度：

| 字段 | 校验 |
|------|------|
| `request.method` | strict（不匹配返回 502） |
| `request.path` | strict（不匹配返回 502） |
| `request.query` | soft（key 存在即可，值不强校验；签名等动态字段难复现） |
| `request.body` | soft（仅 debug 日志，因为 SDK 会算 signature 等动态字段） |
| `response.*` | 直接回放 |

## 当前状态

**Placeholder fixtures**（M10 staging 联调前）：

| 文件 | 状态 | 备注 |
|------|------|------|
| `clob_place_order.json` | placeholder | mock orderID |
| `clob_get_order.json` | placeholder | mock OpenOrder shape |
| `clob_get_book.json` | placeholder | mock OrderBookSummary |
| `gamma_list_events.json` | placeholder | mock Event 数组 |
| `gamma_get_market.json` | placeholder | mock Market |

测试的核心目的：保证框架可跑通 + 类型映射不退化（解析失败会 fail）。**不**保证字段值与 prod 一致。

## 录制工作流（staging 联调时）

1. 跑 staging endpoint 的 e2e 脚本（`pm-cup2026-liquidity` 仓 ops/m10/`）打开 `mitmproxy` 或 `httputil.DumpRequestOut/DumpResponse` 抓真实 HTTP
2. 把每个核心 case 的请求/响应整理成 fixture JSON 替换 `testdata/contract-fixtures/*.json`
3. `comment` 字段写明录制时间 + endpoint + git sha
4. 重跑 `go test -tags contract ./...` 确保框架仍通过
5. PR 合到 main 后 contract job 周期性 sanity check

## 添加新 case

1. 录一个新 fixture JSON 到 `testdata/contract-fixtures/<pkg>_<method>.json`
2. 在 `pkg/<pkg>/contract_test.go` 加一个 `TestContract_XXX` 函数，调 `contracttest.Load + NewMockServer`，跑 facade 方法并断言
3. 跑 `go test -tags contract ./pkg/<pkg>/...` 验证
