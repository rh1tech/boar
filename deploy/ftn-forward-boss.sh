#!/bin/sh
# Copy a just-received FTN file into the boss filebox on spb1 and ask binkd
# to poll 2:410/51 (rbx1). The BBS tosses on the boss; this host only relays.
#
# Usage (from binkd exec): ftn-forward-boss.sh /var/spool/ftn/in/FILE
set -eu

src=${1:?usage: ftn-forward-boss.sh FILE}
obox=/var/spool/ftn/obox.boss
boss_poll=/var/spool/ftn/out/019a0033.poll
base=$(basename "$src")
dest=$obox/$base

install -d -m 2770 -o ftn -g ftn "$obox"
if [ -e "$dest" ]; then
	dest=$obox/$base.$(date -u +%Y%m%d%H%M%S)
fi
cp -p "$src" "$dest"
chown ftn:ftn "$dest"
chmod 664 "$dest"
# Keep a copy under forwarded/ for forensics; remove from inbound so we
# do not re-notify forever.
install -d -m 2770 -o ftn -g ftn /var/spool/ftn/forwarded
mv -f "$src" /var/spool/ftn/forwarded/"$base"
touch "$boss_poll"
chown ftn:ftn "$boss_poll" 2>/dev/null || true
