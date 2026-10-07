#!/usr/bin/env bash
# Install (or refresh) Boar's FidoNet node on spb1: binkd as 2:5030/1651, the
# main node since 2026-10-07, with Boar tossing its inbound. rbx1 is the relay
# (point 2:5030/1651.1, deploy/install-rbx1-relay.sh). Run as root from a copy
# of this directory. Idempotent.
#
# The passwords are not in git. /etc/binkd/nodes.inc must exist already
# (template: nodes.spb1.inc.example); this script refuses to start without it.
#
# Also installs the mail relay the node and the BBS send through: postfix on
# loopback, relaying to rbx1:2525 over TLS pinned to rbx1's certificate (a home
# address cannot deliver mail itself). rbx1 must trust spb1's address in
# mynetworks and ufw for that port.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
export DEBIAN_FRONTEND=noninteractive
# SHA-256 of rbx1's SMTP certificate (/etc/ssl/certs/ssl-cert-snakeoil.pem).
RBX1_SMTP_FP=7F:65:30:A0:35:AD:35:80:B9:45:4F:8C:0B:6E:1A:46:EF:D3:A8:A7:63:14:8D:09:21:30:A9:9B:3D:79:3D:EA

command -v binkd >/dev/null || apt-get install -y -q binkd >/dev/null
getent passwd ftn >/dev/null || { echo "[ftn] the binkd package did not create the ftn user" >&2; exit 1; }

[ -r /etc/binkd/nodes.inc ] || {
  echo "[ftn] /etc/binkd/nodes.inc is missing — create it from nodes.spb1.inc.example with the real passwords" >&2
  exit 1
}

install -d -m 755 -o ftn -g ftn /var/spool/ftn /var/spool/ftn/inb /var/spool/ftn/outb
for d in in in.insecure out tmp obox.rbx1 forwarded; do
  install -d -m 2770 -o ftn -g ftn "/var/spool/ftn/$d"
done
# Boar's tosser files what it read here.
if getent passwd boar >/dev/null; then
  usermod -aG ftn boar
  install -d -m 2770 -o boar -g ftn /var/spool/ftn/tossed /var/spool/ftn/bad
fi
install -d -m 750 -o ftn -g ftn /var/log/binkd

install -m 640 -o root -g ftn "$HERE/binkd-spb1.cfg" /etc/binkd/binkd.cfg
chown root:ftn /etc/binkd/nodes.inc
chmod 640 /etc/binkd/nodes.inc

# The package's unit runs `binkd /etc/binkd/binkd.cfg` as ftn, sandboxed. It
# needs a runtime directory for the pid file, and group-writable files so the
# BBS and binkd agree on the spool.
install -d -m 755 /etc/systemd/system/binkd.service.d
cat >/etc/systemd/system/binkd.service.d/override.conf <<'CONF'
[Service]
RuntimeDirectory=binkd
UMask=0007
CONF

# Until 2026-10-07 spb1 relayed to a boss on rbx1; that timer and script go.
systemctl disable --now binkd-poll-boss.timer >/dev/null 2>&1 || true
rm -f /etc/systemd/system/binkd-poll-boss.service /etc/systemd/system/binkd-poll-boss.timer \
  /usr/local/lib/boar/ftn-forward-boss.sh
install -m 644 "$HERE/binkd-poll-rbx1.service" "$HERE/binkd-poll-rbx1.timer" \
  "$HERE/binkd-poll-hub.service" "$HERE/binkd-poll-hub.timer" /etc/systemd/system/

echo "[ftn] mail relay..."
echo "postfix postfix/main_mailer_type select Satellite system" | debconf-set-selections
echo "postfix postfix/mailname string spb1.re-hash.org" | debconf-set-selections
command -v postfix >/dev/null || apt-get install -y -q postfix >/dev/null
postconf -e "myhostname = spb1.re-hash.org" \
  "inet_interfaces = loopback-only" \
  "mydestination = localhost" \
  "mynetworks = 127.0.0.0/8 [::1]/128" \
  "relayhost = [rbx1.re-hash.org]:2525" \
  "smtp_tls_security_level = fingerprint" \
  "smtp_tls_fingerprint_digest = sha256" \
  "smtp_tls_fingerprint_cert_match = $RBX1_SMTP_FP"
systemctl enable postfix >/dev/null 2>&1
systemctl restart postfix

echo "[ftn] file echoes and their download links..."
# Where Boar keeps file-echo files (-files), and the vhost for download links
# (-files-web on 127.0.0.1:8024) at boar-bbs.rh1.tech, the callers' name.
if getent passwd boar >/dev/null; then
  install -d -m 750 -o boar -g boar /var/lib/boar/files
fi
if [ ! -e /etc/letsencrypt/live/boar-bbs.rh1.tech/fullchain.pem ]; then
  certbot certonly --nginx --non-interactive --agree-tos --keep-until-expiring \
    -m xtreme@outlook.com -d boar-bbs.rh1.tech >/dev/null ||
    echo "[ftn] no certificate for boar-bbs.rh1.tech yet; download links will not work" >&2
fi
if [ -e /etc/letsencrypt/live/boar-bbs.rh1.tech/fullchain.pem ]; then
  install -m 644 "$HERE/nginx-boar-bbs.conf" /etc/nginx/vhosts/boar-bbs.rh1.tech.conf
  if nginx -t 2>/dev/null; then
    systemctl reload nginx
  else
    rm -f /etc/nginx/vhosts/boar-bbs.rh1.tech.conf
    echo "[ftn] nginx rejected the boar-bbs.rh1.tech vhost; removed it" >&2
  fi
fi

systemctl daemon-reload
# The hub never calls in: what it holds for us (netmail, robot answers,
# echomail and files once linked) is collected by this poll.
systemctl enable binkd.service binkd-poll-rbx1.timer binkd-poll-hub.timer >/dev/null 2>&1
systemctl restart binkd.service
systemctl start binkd-poll-rbx1.timer binkd-poll-hub.timer
sleep 2
systemctl is-active binkd.service binkd-poll-rbx1.timer binkd-poll-hub.timer postfix.service
