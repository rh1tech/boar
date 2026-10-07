#!/bin/sh
# Run by binkd on rbx1 for every received file. rbx1 is a node of its own for
# netmail: it emails the sysop about netmail addressed to it (2:410/51, and
# its point address), from anyone including spb1. For everything else it is a
# relay since 2026-10-07: Boar and its tosser live on spb1 (2:5030/1651), so
# every file is handed to spb1's filebox and binkd asked to call it. Files
# that came *from* spb1 (its ibox) are not sent back.
#
# Usage (from binkd exec): ftn-forward-spb1.sh /var/spool/ftn/in/FILE
set -eu

src=${1:?usage: ftn-forward-spb1.sh FILE}
# Best effort: a failed notice must never stop the mail being relayed.
/opt/boar/bin/boar-ftn notify -to xtreme@outlook.com -for 2:410/51,2:5030/1651.1 "$src" || true

# What spb1 sends goes out into FidoNet from here: its netmail for anyone but
# this system is queued for the next hop (Petros for everything he is not
# linked to directly, which is all of it). Nothing is sent back to spb1.
case "$src" in
/var/spool/ftn/in.spb1/*)
	/opt/boar/bin/boar-ftn route -own 2:410/51,2:5030/1651.1 \
		-direct 2:410/9,2:410/0,2:41/0,2:5030/731,2:5030/0,2:5030/1651 -via 2:410/9 \
		"$src" >>/var/log/binkd/route.log 2>&1 ||
		echo "$(date -u +%FT%TZ) route failed for $src; left in place" >>/var/log/binkd/route.log
	exit 0
	;;
esac

obox=/var/spool/ftn/obox.spb1
# An empty direct-flavour flow file is a poll: binkd calls 2:5030/1651 on its
# next rescan. (binkd ignores *.poll; those never triggered a call.)
spb1_poll=/var/spool/ftn/out/13a60673.dlo
base=$(basename "$src")
dest=$obox/$base

install -d -m 2770 -o ftn -g ftn "$obox"
if [ -e "$dest" ]; then
	# Same name still waiting: rename, keeping the extension the tosser
	# recognises (a bundle's base name carries no meaning).
	dest=$obox/$(od -An -N4 -tx4 /dev/urandom | tr -d ' ').${base##*.}
fi
cp -p "$src" "$dest"
chown ftn:ftn "$dest"
chmod 664 "$dest"
# Keep a copy under forwarded/ for forensics, and empty the inbound.
install -d -m 2770 -o ftn -g ftn /var/spool/ftn/forwarded
mv -f "$src" /var/spool/ftn/forwarded/"$base"
touch "$spb1_poll"
chown ftn:ftn "$spb1_poll" 2>/dev/null || true
