#!/usr/bin/env bash
# install.sh
# Full automated setup for Frappe Lightning.
# Runs all setup steps in sequence: MariaDB → config → Meilisearch → build → backfill.
#
# Usage (from the f_lightning app root):
#   ./scripts/install.sh --bench /home/frappe/frappe-bench --site erp.local
#
# Run with --help to see all options.

set -euo pipefail

SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_ROOT="$(dirname "$SCRIPTS_DIR")"

# ── Defaults ──────────────────────────────────────────────────────────────────
BENCH_PATH=""
SITE_NAME=""
LIGHTNING_PASS=""
MEILI_KEY=""
API_TOKEN=""
MEILI_HOST="http://localhost:7700"
SKIP_MARIADB=false
SKIP_MEILI=false
SKIP_BUILD=false
SKIP_BACKFILL=false
SERVER_ID=100
AI_MODE="off"

# ── Help ──────────────────────────────────────────────────────────────────────
usage() {
  cat <<USAGE
Usage: $0 --bench <path> --site <name> [options]

Required:
  --bench PATH        Path to your frappe-bench directory
  --site  NAME        Frappe site name (e.g. erp.local)

Optional:
  --lightning-pass PASS   MariaDB Lightning user password (auto-generated if omitted)
  --meili-key KEY         Meilisearch master key (auto-generated if omitted)
  --api-token TOKEN       Lightning API token (auto-generated if omitted)
  --meili-host URL        Meilisearch URL (default: http://localhost:7700)
  --server-id ID          MariaDB replication server ID (default: 100)
  --ai-mode MODE          off | local (default: off)
  --skip-mariadb          Skip MariaDB binlog setup (already configured)
  --skip-meili            Skip starting Meilisearch via Docker
  --skip-build            Skip building the Go binary
  --skip-backfill         Skip initial index backfill

USAGE
  exit 0
}

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --bench)           BENCH_PATH="$2";    shift 2 ;;
    --site)            SITE_NAME="$2";     shift 2 ;;
    --lightning-pass)  LIGHTNING_PASS="$2"; shift 2 ;;
    --meili-key)       MEILI_KEY="$2";     shift 2 ;;
    --api-token)       API_TOKEN="$2";     shift 2 ;;
    --meili-host)      MEILI_HOST="$2";    shift 2 ;;
    --server-id)       SERVER_ID="$2";     shift 2 ;;
    --ai-mode)         AI_MODE="$2";       shift 2 ;;
    --skip-mariadb)    SKIP_MARIADB=true;  shift ;;
    --skip-meili)      SKIP_MEILI=true;    shift ;;
    --skip-build)      SKIP_BUILD=true;    shift ;;
    --skip-backfill)   SKIP_BACKFILL=true; shift ;;
    --help|-h)         usage ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

if [[ -z "$BENCH_PATH" || -z "$SITE_NAME" ]]; then
  echo "ERROR: --bench and --site are required."
  echo "Run with --help for usage."
  exit 1
fi

cd "$APP_ROOT"

echo ""
echo "╔══════════════════════════════════════╗"
echo "║  ⚡ Frappe Lightning Setup            ║"
echo "║  Bench: ${BENCH_PATH}                 "
echo "║  Site:  ${SITE_NAME}                  "
echo "╚══════════════════════════════════════╝"
echo ""

# ── Step 1: MariaDB Setup ─────────────────────────────────────────────────────
if [[ "$SKIP_MARIADB" == "false" ]]; then
  echo "━━━ Step 1/5: Configure MariaDB ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  MARIADB_ARGS="--server-id ${SERVER_ID}"
  if [[ -n "$LIGHTNING_PASS" ]]; then
    MARIADB_ARGS="$MARIADB_ARGS --lightning-pass ${LIGHTNING_PASS}"
  fi
  # Capture generated password from setup-mariadb.sh output
  MARIADB_OUTPUT=$("$SCRIPTS_DIR/setup-mariadb.sh" $MARIADB_ARGS 2>&1 | tee /dev/tty)
  if [[ -z "$LIGHTNING_PASS" ]]; then
    LIGHTNING_PASS=$(echo "$MARIADB_OUTPUT" | grep "Lightning DB password:" | awk '{print $NF}')
  fi
else
  echo "━━━ Step 1/5: MariaDB (skipped) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  if [[ -z "$LIGHTNING_PASS" ]]; then
    read -rsp "Lightning DB user password: " LIGHTNING_PASS
    echo
  fi
fi

# ── Step 2: Start Meilisearch ─────────────────────────────────────────────────
if [[ "$SKIP_MEILI" == "false" ]]; then
  echo ""
  echo "━━━ Step 2/5: Start Meilisearch ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

  if [[ -z "$MEILI_KEY" ]]; then
    MEILI_KEY=$(openssl rand -hex 24)
    echo "Generated Meilisearch master key: ${MEILI_KEY}"
  fi

  if command -v docker &>/dev/null; then
    # Stop existing container if any
    docker rm -f meilisearch 2>/dev/null || true

    docker run -d \
      --name meilisearch \
      --restart unless-stopped \
      -p 7700:7700 \
      -v "${HOME}/meili_data:/meili_data" \
      getmeili/meilisearch:latest \
      meilisearch --master-key="${MEILI_KEY}" --env="production"

    echo "Meilisearch container started ✓"
    echo "Waiting for Meilisearch to be ready..."
    for i in $(seq 1 15); do
      if curl -sf "${MEILI_HOST}/health" > /dev/null 2>&1; then
        echo "Meilisearch is ready ✓"
        break
      fi
      sleep 1
      if [[ $i -eq 15 ]]; then
        echo "WARNING: Meilisearch health check timed out. Continuing anyway."
      fi
    done
  else
    echo "Docker not found. Start Meilisearch manually:"
    echo "  meilisearch --master-key='${MEILI_KEY}' --env=production"
    echo ""
    read -rp "Press Enter once Meilisearch is running..."
  fi
else
  echo ""
  echo "━━━ Step 2/5: Meilisearch (skipped) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  if [[ -z "$MEILI_KEY" ]]; then
    read -rsp "Meilisearch master key: " MEILI_KEY
    echo
  fi
fi

# ── Step 3: Generate config.yaml ─────────────────────────────────────────────
echo ""
echo "━━━ Step 3/5: Generate config.yaml ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

CONFIG_ARGS="--bench ${BENCH_PATH} --site ${SITE_NAME}"
CONFIG_ARGS="$CONFIG_ARGS --lightning-pass ${LIGHTNING_PASS}"
CONFIG_ARGS="$CONFIG_ARGS --meili-key ${MEILI_KEY}"
CONFIG_ARGS="$CONFIG_ARGS --meili-host ${MEILI_HOST}"
CONFIG_ARGS="$CONFIG_ARGS --server-id ${SERVER_ID}"
CONFIG_ARGS="$CONFIG_ARGS --ai-mode ${AI_MODE}"
if [[ -n "$API_TOKEN" ]]; then
  CONFIG_ARGS="$CONFIG_ARGS --api-token ${API_TOKEN}"
fi
CONFIG_OUTPUT=$("$SCRIPTS_DIR/generate-config.sh" $CONFIG_ARGS 2>&1 | tee /dev/tty)
if [[ -z "$API_TOKEN" ]]; then
  API_TOKEN=$(echo "$CONFIG_OUTPUT" | grep "Lightning API token:" | awk '{print $NF}')
fi

CONFIG_FILE="frappe_lightning/config.yaml"

# ── Step 4: Build Go binaries ─────────────────────────────────────────────────
if [[ "$SKIP_BUILD" == "false" ]]; then
  echo ""
  echo "━━━ Step 4/5: Build Lightning binaries ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

  if ! command -v go &>/dev/null; then
    echo "ERROR: Go is not installed or not on PATH."
    echo "Install Go 1.21+ from https://go.dev/dl/ and try again."
    exit 1
  fi

  echo "Building lightning-server..."
  (cd frappe_lightning && go build -o ../lightning-server ./main.go)
  echo "  lightning-server ✓"

  echo "Building lightning CLI..."
  (cd frappe_lightning && go build -o ../lightning ./cmd/lightning/main.go)
  echo "  lightning ✓"
else
  echo ""
  echo "━━━ Step 4/5: Build (skipped) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
fi

# ── Step 5: Initial Backfill ──────────────────────────────────────────────────
if [[ "$SKIP_BACKFILL" == "false" ]]; then
  echo ""
  echo "━━━ Step 5/5: Initial Backfill ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "Starting Lightning server in the background for backfill..."

  ./lightning-server --config "$CONFIG_FILE" &
  LIGHTNING_PID=$!
  echo "Server PID: $LIGHTNING_PID"

  # Wait for the API to be up
  for i in $(seq 1 20); do
    if curl -sf "http://localhost:8765/api/v1/health" > /dev/null 2>&1; then
      echo "Lightning server ready ✓"
      break
    fi
    sleep 1
    if [[ $i -eq 20 ]]; then
      echo "WARNING: Server health check timed out. Attempting backfill anyway."
    fi
  done

  "$SCRIPTS_DIR/backfill-all.sh" \
    --config "$CONFIG_FILE" \
    --site "$SITE_NAME" \
    --lightning-bin "./lightning"

  echo ""
  echo "Stopping background server (PID $LIGHTNING_PID)..."
  kill "$LIGHTNING_PID" 2>/dev/null || true
  wait "$LIGHTNING_PID" 2>/dev/null || true
else
  echo ""
  echo "━━━ Step 5/5: Backfill (skipped) ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  ⚡ Frappe Lightning setup complete!                          ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "  Config:    ${APP_ROOT}/${CONFIG_FILE}"
echo "  API token: ${API_TOKEN}"
echo ""
echo "Start the server:"
echo "  ${APP_ROOT}/lightning-server --config ${CONFIG_FILE}"
echo ""
echo "Or install as a systemd service (see docs/deployment.md)."
echo ""
echo "Verify sync health:"
echo "  ./lightning status --config ${CONFIG_FILE}"
echo "  ./lightning diff ${SITE_NAME} \"Sales Invoice\" --config ${CONFIG_FILE}"
