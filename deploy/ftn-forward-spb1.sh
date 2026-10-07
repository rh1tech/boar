#!/bin/sh
# rbx1 is a relay since 2026-10-07: Boar and its tosser live on spb1
# (2:5030/1651). Hand every file received here to spb1's filebox and ask
# binkd to call it. Files that came *from* spb1 (its ibox) are left alone.
#
# Usage (from binkd exec): ftn-forward-spb1.sh /var/spool/ftn/in/FILE
set -eu

src=${1:?usage: ftn-forward-spb1.sh FILE}
case "$src" in /var/spool/ftn/in.spb1/*) exit 0 ;; esac

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
