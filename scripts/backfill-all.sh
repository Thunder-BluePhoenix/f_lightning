#!/usr/bin/env bash
# backfill-all.sh
# Reads the DocType list for a site directly from config.yaml and runs backfill
# for every one. Run this after the initial setup or after adding new DocTypes
# and running bench migrate.
#
# Usage:
#   ./scripts/backfill-all.sh
#   ./scripts/backfill-all.sh --config /etc/frappe-lightning/config.yaml --site erp.local
#   ./scripts/backfill-all.sh --config config.yaml --site erp.local --lightning-bin ./lightning

set -euo pipefail

CONFIG="frappe_lightning/config.yaml"
SITE=""
LIGHTNING_BIN="./lightning"

while [[ $# -gt 0 ]]; do
  case $1 in
    --config)        CONFIG="$2";        shift 2 ;;
    --site)          SITE="$2";          shift 2 ;;
    --lightning-bin) LIGHTNING_BIN="$2"; shift 2 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# ── Validate config file exists ───────────────────────────────────────────────
if [[ ! -f "$CONFIG" ]]; then
  echo "ERROR: config file not found: ${CONFIG}"
  exit 1
fi

# ── Resolve the lightning binary ───────────────────────────────────────────────
if [[ ! -x "$LIGHTNING_BIN" ]]; then
  if command -v lightning &>/dev/null; then
    LIGHTNING_BIN="lightning"
  else
    echo "ERROR: lightning CLI binary not found at '${LIGHTNING_BIN}'"
    echo "       Build it first:"
    echo "         cd frappe_lightning && go build -o ../lightning ./cmd/lightning/main.go"
    exit 1
  fi
fi

# ── Read site name and DocType list from config.yaml ─────────────────────────
# Uses python3 + PyYAML (available in every Frappe bench environment).
# Falls back to a plain grep if PyYAML is somehow missing.

read_config() {
  python3 - "$CONFIG" "$SITE" <<'PYEOF'
import sys, yaml

config_path = sys.argv[1]
requested_site = sys.argv[2]  # may be empty string

with open(config_path) as f:
    cfg = yaml.safe_load(f)

sites = cfg.get("sites", [])
if not sites:
    print("ERROR: no sites defined in config", file=sys.stderr)
    sys.exit(1)

# Pick the requested site, or default to the first one
site_cfg = None
if requested_site:
    for s in sites:
        if s["name"] == requested_site:
            site_cfg = s
            break
    if site_cfg is None:
        print(f"ERROR: site '{requested_site}' not found in config", file=sys.stderr)
        sys.exit(1)
else:
    site_cfg = sites[0]

doctypes = [dt["name"] for dt in site_cfg.get("doctypes", [])]
if not doctypes:
    print("ERROR: no doctypes listed for site", file=sys.stderr)
    sys.exit(1)

# Print: first line = site name, remaining lines = doctype names
print(site_cfg["name"])
for dt in doctypes:
    print(dt)
PYEOF
}

# Run the parser
if ! CONFIG_LINES=$(read_config 2>/tmp/lightning_backfill_err); then
  cat /tmp/lightning_backfill_err >&2

  # Grep fallback — coarser but avoids a hard failure if PyYAML is missing
  echo "Falling back to grep-based config parsing..." >&2
  if [[ -z "$SITE" ]]; then
    SITE=$(grep -A2 '^sites:' "$CONFIG" | grep 'name:' | head -1 | awk '{print $2}')
  fi
  DOCTYPES_RAW=$(grep -E '^\s+-\s+name:' "$CONFIG" \
    | grep -v 'sites:' \
    | awk '{$1=$2=""; print $0}' \
    | sed 's/^ *//')
  mapfile -t DOCTYPES <<< "$DOCTYPES_RAW"
else
  # First line is the resolved site name; the rest are DocType names
  SITE=$(echo "$CONFIG_LINES" | head -1)
  mapfile -t DOCTYPES <<< "$(echo "$CONFIG_LINES" | tail -n +2)"
fi

if [[ -z "$SITE" ]]; then
  read -rp "Site name (from config.yaml): " SITE
fi

if [[ ${#DOCTYPES[@]} -eq 0 ]]; then
  echo "ERROR: could not read any DocTypes from ${CONFIG}"
  exit 1
fi

# ── Run backfill ──────────────────────────────────────────────────────────────
echo "⚡ Lightning Backfill"
echo "   Config:   ${CONFIG}"
echo "   Site:     ${SITE}"
echo "   DocTypes: ${#DOCTYPES[@]} (read from config.yaml)"
echo ""

FAILED=()
for dt in "${DOCTYPES[@]}"; do
  [[ -z "$dt" ]] && continue
  echo "==> Backfilling: ${dt}"
  if "$LIGHTNING_BIN" backfill \
      --config "$CONFIG" \
      --site   "$SITE" \
      --doctype "$dt" 2>&1; then
    echo "    Done ✓"
  else
    echo "    FAILED ✗ (continuing with remaining DocTypes)"
    FAILED+=("$dt")
  fi
  echo ""
done

# ── Summary ───────────────────────────────────────────────────────────────────
if [[ ${#FAILED[@]} -gt 0 ]]; then
  echo "Backfill finished with errors. Re-run these individually:"
  for dt in "${FAILED[@]}"; do
    echo "  $LIGHTNING_BIN backfill --config ${CONFIG} --site ${SITE} --doctype \"${dt}\""
  done
  exit 1
fi

echo "All ${#DOCTYPES[@]} DocTypes indexed successfully."
echo ""
echo "Verify sync:"
for dt in "${DOCTYPES[@]}"; do
  [[ -z "$dt" ]] && continue
  echo "  $LIGHTNING_BIN diff ${SITE} \"${dt}\" --config ${CONFIG}"
done
