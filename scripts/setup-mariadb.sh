#!/usr/bin/env bash
# setup-mariadb.sh
# Enables ROW-format binary logging and creates the dedicated Lightning MariaDB user.
# Must be run as a user with sudo access and MySQL root/admin privileges.
#
# Usage:
#   ./scripts/setup-mariadb.sh
#   ./scripts/setup-mariadb.sh --mysql-root-user admin --lightning-password mysecret

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
MYSQL_ROOT_USER="root"
MYSQL_ROOT_PASS=""
LIGHTNING_USER="lightning"
LIGHTNING_PASS=""
MARIADB_CONF_DIR="/etc/mysql/mariadb.conf.d"
SERVER_ID=100

# ── Argument parsing ───────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --mysql-root-user)  MYSQL_ROOT_USER="$2";  shift 2 ;;
    --mysql-root-pass)  MYSQL_ROOT_PASS="$2";  shift 2 ;;
    --lightning-user)   LIGHTNING_USER="$2";   shift 2 ;;
    --lightning-pass)   LIGHTNING_PASS="$2";   shift 2 ;;
    --server-id)        SERVER_ID="$2";        shift 2 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# ── Interactive prompts if not provided ────────────────────────────────────────
if [[ -z "$MYSQL_ROOT_PASS" ]]; then
  read -rsp "MariaDB root password (leave blank if no password): " MYSQL_ROOT_PASS
  echo
fi

if [[ -z "$LIGHTNING_PASS" ]]; then
  LIGHTNING_PASS=$(openssl rand -hex 16)
  echo "Generated Lightning DB password: $LIGHTNING_PASS"
  echo "  (this will also be written to config.yaml by generate-config.sh)"
fi

MYSQL_CMD="mysql -u${MYSQL_ROOT_USER}"
if [[ -n "$MYSQL_ROOT_PASS" ]]; then
  MYSQL_CMD="${MYSQL_CMD} -p${MYSQL_ROOT_PASS}"
fi

# ── Step 1: Enable binary log in MariaDB config ───────────────────────────────
echo ""
echo "==> Configuring MariaDB binary log..."

CONF_FILE=""
for dir in "/etc/mysql/mariadb.conf.d" "/etc/mysql/conf.d" "/etc/my.cnf.d"; do
  if [[ -d "$dir" ]]; then
    CONF_FILE="${dir}/90-lightning.cnf"
    break
  fi
done

if [[ -z "$CONF_FILE" ]]; then
  CONF_FILE="/etc/mysql/conf.d/90-lightning.cnf"
fi

sudo tee "$CONF_FILE" > /dev/null <<EOF
# Frappe Lightning — MariaDB binary log settings
[mysqld]
binlog_format    = ROW
binlog_row_image = FULL
server_id        = ${SERVER_ID}
log-bin          = mysql-bin
expire_logs_days = 7
EOF

echo "    Written: $CONF_FILE"

# ── Step 2: Restart MariaDB ───────────────────────────────────────────────────
echo "==> Restarting MariaDB..."
if command -v systemctl &>/dev/null; then
  sudo systemctl restart mariadb || sudo systemctl restart mysql
else
  sudo service mysql restart
fi

# Brief wait for MariaDB to finish starting
sleep 2

# ── Step 3: Verify binlog format ─────────────────────────────────────────────
echo "==> Verifying binlog_format..."
BINLOG_FORMAT=$(${MYSQL_CMD} -se "SHOW VARIABLES LIKE 'binlog_format';" | awk '{print $2}')
if [[ "$BINLOG_FORMAT" != "ROW" ]]; then
  echo "ERROR: binlog_format is '${BINLOG_FORMAT}', expected ROW."
  echo "       Check $CONF_FILE and try restarting MariaDB manually."
  exit 1
fi
echo "    binlog_format = ROW ✓"

# ── Step 4: Create the Lightning database user ────────────────────────────────
echo "==> Creating Lightning database user '${LIGHTNING_USER}'..."
${MYSQL_CMD} <<EOF
CREATE USER IF NOT EXISTS '${LIGHTNING_USER}'@'%' IDENTIFIED BY '${LIGHTNING_PASS}';
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO '${LIGHTNING_USER}'@'%';
FLUSH PRIVILEGES;
EOF
echo "    User '${LIGHTNING_USER}'@'%' created with replication grants ✓"

# ── Step 5: Grant SELECT on each Frappe site DB ────────────────────────────────
echo ""
echo "==> Discovering Frappe site databases..."
SITE_DBS=$(${MYSQL_CMD} -se "SHOW DATABASES;" | grep -v -E '^(information_schema|performance_schema|mysql|sys)$' || true)

if [[ -z "$SITE_DBS" ]]; then
  echo "    No site databases found. Grant SELECT manually:"
  echo "    GRANT SELECT ON \`your_site_db\`.* TO '${LIGHTNING_USER}'@'%';"
else
  for db in $SITE_DBS; do
    ${MYSQL_CMD} -e "GRANT SELECT ON \`${db}\`.* TO '${LIGHTNING_USER}'@'%';"
    echo "    GRANT SELECT on \`${db}\` ✓"
  done
  ${MYSQL_CMD} -e "FLUSH PRIVILEGES;"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo "MariaDB setup complete."
echo ""
echo "  Lightning DB user:     ${LIGHTNING_USER}"
echo "  Lightning DB password: ${LIGHTNING_PASS}"
echo ""
echo "Next step: run ./scripts/generate-config.sh to create config.yaml"
echo "  Pass --lightning-pass '${LIGHTNING_PASS}' to pre-fill the password."
