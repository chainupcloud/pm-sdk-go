---
milestone: M23
issue: 38
phase: implementation-and-review
status: active
---

# 活单分页响应必须完整

关联 chainupcloud/pm-cup2026-liquidity#2003 与 PR #2013。独立审查发现 ListOrders 将 HTTP 200 的
空对象、null、缺 data 或缺 next_cursor 当成完整空集，不能作为唯一代及 HOLD 恢复的安全依据。

- [x] 真实 HTTP 红测复现五种畸形成功响应错误返回 nil error。
- [x] 校验 data 与 next_cursor 字段存在且非 null；合法空数组与显式空/终止游标保持兼容。
- [ ] 全量 SDK 回归、vet、独立双轴审查、PR main 后由 liquidity dev pin。

不改签名、下单、重试或交易终态；只是拒绝不符合分页合同的服务端响应。
