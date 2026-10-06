#!/usr/bin/env bash
# Install (or refresh) Boar's FidoNet edge node on spb1: binkd as 2:5030/1651
# and point 2:410/51.1, relaying everything it receives to the boss 2:410/51
# on rbx1. Run as root from a copy of this directory. Idempotent.
#
# The passwords are not in git. /etc/binkd/nodes.inc must exist already
# (template: nodes.spb1.inc.example); this script refuses to start without it.
#
# Until 2026-10-07 the node ran on nsk1, set up by hand from these files.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
export DEBIAN_FRONTEND=noninteractive

command -v binkd >/dev/null || apt-get install -y -q binkd >/dev/null
getent passwd ftn >/dev/null || { echo "[ftn] the binkd package did not create the ftn user" >&2; exit 1; }

[ -r /etc/binkd/nodes.inc ] || {
  echo "[ftn] /etc/binkd/nodes.inc is missing — create it from nodes.spb1.inc.example with the real passwords" >&2
  exit 1
}

install -d -m 755 -o ftn -g ftn /var/spool/ftn /var/spool/ftn/inb /var/spool/ftn/outb
for d in in in.insecure out tmp obox.boss forwarded; do
  install -d -m 2770 -o ftn -g ftn "/var/spool/ftn/$d"
done
install -d -m 750 -o ftn -g ftn /var/log/binkd

install -m 640 -o root -g ftn "$HERE/binkd-spb1.cfg" /etc/binkd/binkd.cfg
chown root:ftn /etc/binkd/nodes.inc
chmod 640 /etc/binkd/nodes.inc
install -d -m 755 /usr/local/lib/boar
install -m 755 "$HERE/ftn-forward-boss.sh" /usr/local/lib/boar/ftn-forward-boss.sh

# The package's unit runs `binkd /etc/binkd/binkd.cfg` as ftn, sandboxed. It
# needs a runtime directory for the pid file, and group-writable files so the
# relay script and binkd agree on the spool.
install -d -m 755 /etc/systemd/system/binkd.service.d
cat >/etc/systemd/system/binkd.service.d/override.conf <<'CONF'
[Service]
RuntimeDirectory=binkd
UMask=0007
CONF
install -m 644 "$HERE/binkd-poll-boss.service" "$HERE/binkd-poll-boss.timer" /etc/systemd/system/

systemctl daemon-reload
systemctl enable binkd.service binkd-poll-boss.timer >/dev/null 2>&1
systemctl restart binkd.service
systemctl start binkd-poll-boss.timer
sleep 2
systemctl is-active binkd.service binkd-poll-boss.timer
