#!/usr/bin/env bash
# Local-only acceptance environment. Never points at an existing deployment.
set -euo pipefail
cd "$(dirname "$0")/.."
# Override to run a second isolated stack from another checkout.
project="${COHESTRA_ACCEPTANCE_PROJECT:-cohestra-acceptance-20260922}"
compose=(docker compose -p "$project" -f docker-compose.yml -f docker-compose.acceptance.yml)
case "${1:-status}" in
  up|start)
    if [[ "${1}" == up ]]; then
      NO_UP=1 ./scripts/bootstrap.sh
      chmod 600 .env secrets/worker-keypair.pem
      npm ci
      VITE_PASSWORD_LOGIN_ENABLED=true VITE_GOOGLE_LOGIN_ENABLED=false npm run build
      mkdir -p .artifacts/local-acceptance/bin
      (cd apps/workflow-go && go build -tags ee -p 2 -o ../../.artifacts/local-acceptance/bin/ ./cmd/api ./cmd/worker ./cmd/activity-worker)
    fi
    "${compose[@]}" up -d --no-build --wait postgres redis clickhouse temporal temporal-namespaces temporal-ui
    exec python3 scripts/local-acceptance-native.py serve
    ;;
  stop|down)
    if [[ -f .artifacts/local-acceptance/native/supervisor.pid ]]; then
      python3 scripts/local-acceptance-native.py stop
    fi
    "${compose[@]}" "${1}" # down retains all acceptance volumes and records.
    ;;
  status) "${compose[@]}" ps ;;
  logs) "${compose[@]}" logs --tail 100 "${@:2}" ;;
  smoke) python3 scripts/local-acceptance-smoke.py ;;
  browser) npx playwright test --config tests/local-acceptance/playwright.config.ts ;;
  controls)
    python3 scripts/local-acceptance-controls.py
    npx playwright test --config tests/local-acceptance/controls.config.ts
    ;;
  review) npx playwright test --config tests/local-acceptance/review.config.ts ;;
  issues) npx playwright test --config tests/local-acceptance/issues.config.ts ;;
  responsive) npx playwright test --config tests/local-acceptance/responsive.config.ts ;;
  runtime) npx playwright test --config tests/local-acceptance/runtime.config.ts ;;
  *) echo "Usage: $0 {up|start|stop|down|status|logs [service]|smoke|browser|controls|review|issues|runtime|responsive}" >&2; exit 2 ;;
esac
