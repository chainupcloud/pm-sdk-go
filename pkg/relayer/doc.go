// Package relayer 提供 pm-cup2026 relayer-service 的 Go 客户端 + EIP-712 帮助类，
// 帮做市/上链 admin 工具走 Polymarket 风格的「无 gas」Safe 提交路径。
//
// 三块组成：
//
//  1. Client — relayer-service HTTP 端点（POST /submit、GET /nonce、/deployed、
//     /transaction、/transactions、/relay-payload）的 thin wrapper。鉴权支持
//     RELAYER_API_KEY 头与 Authorization: Bearer <jwt> 两路（契约见
//     pm-cup2026/services/relayer-service/internal/auth/middleware.go）。
//
//  2. EIP-712 — SafeCreate 与 SafeTx 两套域 + structHash + digest 一站式构造，
//     与 pm-cup2026/services/clob-service/cmd/market-maker/relayer.go 中已经在
//     prod 上跑过的 typeHash 字符串、字段顺序、padding 完全一致。
//
//  3. Service — 把 Client + DigestSigner 组合成两个高级方法 DeploySafe /
//     ExecuteSafeTx，调用方一次调用就完成 nonce 拉取 → digest 计算 → 签名 →
//     /submit → /transaction 轮询。
//
// 注：Service 用本地 DigestSigner 接口（仅一个 SignDigest 方法）而不是
// pkg/signer.Signer——因为 Safe 的两套域分隔符在每次调用时由 chainID/
// factoryAddr/safeAddr 动态推导，pkg/signer 的 Signer 接口在构造时即固化
// domainSeparator，不适配本场景。NewPrivateKeySigner 提供了开箱即用实现，
// 与 ethcrypto.Sign 行为一致（v ∈ {0,1}，本包内部按 Safe / SafeProxyFactory
// 期望统一加 27）。
package relayer
