#!/usr/bin/env bash
# scripts/codegen.sh — Phase 2 placeholder
#
# Phase 2 起本脚本将：
#   1. 从 chainupcloud/pm-cup2026:pm-v2 拉取 OpenAPI yaml：
#        services/clob-service/docs/openapi.yaml  → pkg/clob/generated.go
#        services/gamma-service/api/openapi.yaml  → pkg/gamma/generated.go
#   2. 调用 oapi-codegen --config codegen-config.yaml
#   3. CI 通过 `git diff --exit-code` 校验 generated 不漂移
#
# Phase 1 仅占位，直接成功退出，便于 CI workflow 提前接入。

set -euo pipefail

echo "scripts/codegen.sh: Phase 1 stub — codegen will be implemented in Phase 2"
exit 0
