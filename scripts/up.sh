#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose up --build -d
echo
echo "Surge 已启动：http://127.0.0.1:8080"
echo "健康检查：   curl -s http://127.0.0.1:8080/healthz"
echo "停栈：       make down  或  ./scripts/down.sh"
