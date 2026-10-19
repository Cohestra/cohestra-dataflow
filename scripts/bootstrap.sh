#!/usr/bin/env bash
# One-command bootstrap for self-hosting DataFlow.
#
#   ./scripts/bootstrap.sh            # generate config + secrets, then Helm deploy
#   NO_UP=1 ./scripts/bootstrap.sh    # generate only, don't start the stack
#
# Idempotent: existing .env values and the worker keypair are preserved.
set -euo pipefail
cd "$(dirname "$0")/.."

ENV_FILE=.env
gen_hex()    { node -e "console.log(require('crypto').randomBytes(32).toString('hex'))"; }
gen_base64() { node -e "console.log(require('crypto').randomBytes(32).toString('base64'))"; }

# set_env KEY VALUE — update KEY in .env if present, else append.
set_env() {
  local key="$1" val="$2"
  if grep -q "^${key}=" "$ENV_FILE"; then
    node -e '
      const fs=require("fs"); const [f,k,v]=process.argv.slice(1);
      const re=new RegExp("^"+k+"=.*$","m");
      fs.writeFileSync(f, fs.readFileSync(f,"utf8").replace(re, k+"="+v));
    ' "$ENV_FILE" "$key" "$val"
  else
    printf '%s=%s\n' "$key" "$val" >> "$ENV_FILE"
  fi
}

# Returns the current value of KEY in .env (empty if unset/placeholder).
get_env() { grep "^$1=" "$ENV_FILE" | head -1 | cut -d= -f2- || true; }

echo "▶ DataFlow bootstrap"

# 1. .env from the template.
if [ ! -f "$ENV_FILE" ]; then
  cp .env.example "$ENV_FILE"
  echo "  ✓ created .env from .env.example"
else
  echo "  • .env exists — keeping it, filling only empty secrets"
fi

# 2. Random secrets (only if empty or a placeholder).
for key in JWT_ACCESS_SECRET OAUTH_TOKEN_ENCRYPTION_KEY; do
  cur=$(get_env "$key")
  case "$cur" in ""|*change-me*) set_env "$key" "$(gen_hex)"; echo "  ✓ generated $key";; esac
done
cur=$(get_env TEMPORAL_PAYLOAD_ENCRYPTION_KEY)
[ -z "$cur" ] && { set_env TEMPORAL_PAYLOAD_ENCRYPTION_KEY "$(gen_base64)"; echo "  ✓ generated TEMPORAL_PAYLOAD_ENCRYPTION_KEY"; }

# 3. Worker RSA keypair + public key into .env.
if [ ! -f secrets/worker-keypair.pem ]; then
  node scripts/gen-worker-keypair.js >/dev/null
  echo "  ✓ generated secrets/worker-keypair.pem"
fi
PUB_ONELINE=$(node -e 'process.stdout.write(require("fs").readFileSync("secrets/worker-keypair.pub","utf8").replace(/\n/g,"\\n"))')
set_env WORKER_PUBLIC_KEY_PEM "\"$PUB_ONELINE\""
echo "  ✓ wired WORKER_PUBLIC_KEY_PEM"

# 4. Deploy the local Helm stack. Ollama is part of the release and becomes
# ready only after its configured model has been pulled.
if [ -n "${NO_UP:-}" ]; then
  echo "▶ NO_UP set — skipping startup. Start later with:"
  echo "    ./scripts/bootstrap.sh"
  exit 0
fi
for command in docker kind kubectl helm; do
  command -v "$command" >/dev/null 2>&1 || { echo "▶ $command is required"; exit 1; }
done

kind get clusters | grep -qx dataflow || kind create cluster --config <(sed "s|__REPO_ROOT__|$PWD|" deploy/kind/dataflow.yaml)
for node in $(kind get nodes --name dataflow); do
  docker update --restart unless-stopped "$node" >/dev/null
done
docker build -f apps/workflow-go/Dockerfile -t dataflow-app:local .
docker build -f apps/web/Dockerfile -t dataflow-web:local .
kind load docker-image --name dataflow dataflow-app:local dataflow-web:local

HELM_VALUES_FILE=$(mktemp "${TMPDIR:-/tmp}/dataflow-helm-secrets.XXXXXX.yaml")
node scripts/render-helm-secrets.mjs "$ENV_FILE" "$HELM_VALUES_FILE"
trap 'rm -f "$HELM_VALUES_FILE"' EXIT

PUBLIC_IP=$(curl --connect-timeout 1 --max-time 2 -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip || true)
if [ -z "${GCP_SECRET_MANAGER_NAME:-}" ] && [ -n "$PUBLIC_IP" ]; then
  GCP_SECRET_MANAGER_NAME=dataflow-secrets
fi
if [ -n "${GCP_SECRET_MANAGER_NAME:-}" ]; then
  echo "▶ Fetching secrets from GCP Secret Manager ($GCP_SECRET_MANAGER_NAME)"
  SECRET_JSON=""
  for attempt in $(seq 1 30); do
    if command -v gcloud >/dev/null 2>&1; then
      SECRET_JSON=$(gcloud secrets versions access latest --secret="$GCP_SECRET_MANAGER_NAME" 2>/dev/null || true)
    else
      SECRET_JSON=$(node scripts/fetch-gcp-secret.mjs "$GCP_SECRET_MANAGER_NAME" 2>/dev/null || true)
    fi
    [ -n "$SECRET_JSON" ] && break
    [ "$attempt" -lt 30 ] && sleep 10
  done
  if [ -n "$SECRET_JSON" ]; then
    echo "$SECRET_JSON" | node -e "
const fs = require('fs');
const output = process.argv[1]; let d='';
process.stdin.on('data', c => d+=c);
process.stdin.on('end', () => {
  const secrets = JSON.parse(d);
  let yaml = 'secrets:\n';
  for (const [k, v] of Object.entries(secrets)) {
    yaml += '  ' + k + ': ' + JSON.stringify(v) + '\n';
  }
  fs.writeFileSync(output, yaml, { mode: 0o600 });
  // Production mode hard-requires this set (see 'required' calls in the
  // chart); missing any key would fail helm upgrade before install.
  const needed = ['appDatabaseUrl','databaseUrl','googleClientId','googleClientSecret','jwt','oauthKey','postgresPassword','redisPassword','redisUrl','smtpFrom','smtpPass','smtpUser','temporalPayloadKey'];
  const missing = needed.filter(k => !(k in secrets));
  if (missing.length) { console.error('missing secret keys: ' + missing.join(', ')); process.exit(3); }
});" "$HELM_VALUES_FILE" && SECRETS_READY=true || SECRETS_READY=false
  else
    echo "⚠️ Could not fetch secret $GCP_SECRET_MANAGER_NAME"
    SECRETS_READY=false
  fi
fi

HELM_RUNTIME_ARGS=()
if [ -n "$PUBLIC_IP" ]; then
  # Production mode only when the full secret set actually loaded — otherwise
  # helm's required() checks abort the install and the stack never comes up.
  if [ "${SECRETS_READY:-false}" = "true" ]; then
    HELM_RUNTIME_ARGS+=(--set runtime.production=true)
  elif [ "${ALLOW_INSECURE_PUBLIC_INSTALL:-false}" = "true" ]; then
    echo "⚠️ Secrets incomplete or unavailable — ALLOW_INSECURE_PUBLIC_INSTALL=true, installing WITHOUT runtime.production on public IP $PUBLIC_IP"
  else
    # A public IP without production mode means development secrets, non-Secure
    # cookies and default database passwords on the internet. Stop instead.
    echo "✖ Secrets incomplete or unavailable for $GCP_SECRET_MANAGER_NAME; refusing to install on public IP $PUBLIC_IP without runtime.production." >&2
    echo "  Populate the secret and rerun bootstrap, or set ALLOW_INSECURE_PUBLIC_INSTALL=true for a throwaway demo." >&2
    exit 1
  fi
  HELM_RUNTIME_ARGS+=(--set-string "secrets.appUrl=https://${PUBLIC_IP}.nip.io")
  if [ -n "${BACKUP_GCS_BUCKET:-}" ]; then
    HELM_RUNTIME_ARGS+=(--set-string "backup.gcsBucket=${BACKUP_GCS_BUCKET}")
  fi
fi

helm upgrade --install dataflow deploy/helm/dataflow --namespace dataflow --create-namespace \
  -f "$HELM_VALUES_FILE" "${HELM_RUNTIME_ARGS[@]}"

if [ -f cohestra/deploy/helm/fcp/Chart.yaml ]; then
  kind load docker-image --name dataflow cohestra-control-api:latest cohestra-worker:latest
  helm upgrade --install cohestra cohestra/deploy/helm/fcp --namespace cohestra-system --create-namespace \
    --set image.pullPolicy=Never \
    --set image.controlApi.repository=cohestra-control-api --set image.controlApi.tag=latest \
    --set image.worker.repository=cohestra-worker --set image.worker.tag=latest \
    --set controlApi.replicaCount=1 --set controlApi.service.type=NodePort --set controlApi.service.nodePort=30080 \
    --set worker.replicaCount=1 --set temporal.mode=bundled --set temporal.bundled.uiEnabled=true \
    --set 'flink.watchNamespaces[0]=dataflow'
fi

if [ -n "$PUBLIC_IP" ]; then
  docker rm -f caddy >/dev/null 2>&1 || true
  docker run -d --restart unless-stopped --name caddy --network host \
    -v caddy_data:/data -v caddy_config:/config \
    caddy:alpine caddy reverse-proxy --from "${PUBLIC_IP}.nip.io" --to localhost:3002 \
    --header-down "Strict-Transport-Security: max-age=31536000; includeSubDomains" >/dev/null
fi

kubectl -n dataflow rollout status deployment/api --timeout=5m
kubectl -n dataflow rollout status deployment/web --timeout=5m
if [ -n "$PUBLIC_IP" ]; then
  echo "✓ DataFlow ready. Web: https://${PUBLIC_IP}.nip.io · Cohestra: http://localhost:8080 · Temporal: http://localhost:8082"
else
  echo "✓ DataFlow ready. Web: http://localhost:3002 · Cohestra: http://localhost:8080 · Temporal: http://localhost:8082"
fi
