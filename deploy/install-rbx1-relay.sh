#!/usr/bin/env bash
# Install (or refresh) rbx1's half of Boar's FidoNet setup: binkd as 2:410/51
# (Petros' link) and 2:5030/1651.1 (spb1's point), relaying everything it
# receives to spb1, where Boar tosses it. Run as root from a copy of this
# directory. Idempotent.
#
# The passwords are not in git: /etc/binkd/nodes.inc must exist, with spb1 as
#   node 2:5030/1651@fidonet spb1.re-hash.org,194.146.240.248 BACKBONE_PASSWORD - /var/spool/ftn/obox.spb1 /var/spool/ftn/in.spb1
#
# Boar itself no longer runs here (since 2026-10-07); its old data in
# /var/lib/boar is left alone.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"

[ -r /etc/binkd/nodes.inc ] || { echo "[relay] /etc/binkd/nodes.inc is missing" >&2; exit 1; }
grep -q '^node 2:5030/1651@fidonet' /etc/binkd/nodes.inc ||
  { echo "[relay] nodes.inc has no line for spb1 (2:5030/1651)" >&2; exit 1; }

for d in in in.insecure in.spb1 out tmp obox.spb1 forwarded; do
  install -d -m 2770 -o ftn -g ftn "/var/spool/ftn/$d"
done
install -m 640 -o root -g ftn "$HERE/binkd.cfg" /etc/binkd/binkd.cfg
install -d -m 755 /usr/local/lib/boar
install -m 755 "$HERE/ftn-forward-spb1.sh" /usr/local/lib/boar/ftn-forward-spb1.sh

systemctl disable --now boar.service >/dev/null 2>&1 || true
systemctl restart binkd.service
sleep 2
systemctl is-active binkd.service
