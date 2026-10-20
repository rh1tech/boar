#!/bin/sh
# Run by binkd on spb1 for every received mail file. Boar tosses binkd's
# inbound into the BBS and moves each file to tossed/, so hpt (the hub tosser
# that feeds point 2:5030/1651.1 and exports its mail) gets its own hard link
# to the same file in in.hpt/ (or in.hpt.insecure/). hpt-toss.timer tosses it.
#
# Usage (from binkd exec): ftn-copy-hpt.sh /var/spool/ftn/in/FILE
set -eu

src=${1:?usage: ftn-copy-hpt.sh FILE}
case "$src" in
/var/spool/ftn/in.insecure/*) dest=/var/spool/ftn/in.hpt.insecure ;;
*) dest=/var/spool/ftn/in.hpt ;;
esac
base=$(basename "$src")
# Boar tosses every 30 seconds and may have moved it already.
if [ ! -e "$src" ]; then
	src=/var/spool/ftn/tossed/$base
fi
if [ ! -e "$src" ]; then
	logger -t ftn-copy-hpt "$base: gone before hpt got a copy"
	exit 0
fi
target=$dest/$base
if [ -e "$target" ]; then
	# Mailers reuse names; keep the extension hpt recognises.
	target=$dest/$(od -An -N4 -tx4 /dev/urandom | tr -d ' ').${base##*.}
fi
ln "$src" "$target" 2>/dev/null || cp -p "$src" "$target"
