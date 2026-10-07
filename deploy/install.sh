#!/bin/sh
# Build Boar BBS for Linux and install or update it on a server over SSH.
#
#   deploy/install.sh [user@host [port]]   # default: spb1, xtreme@spb1.re-hash.org port 51622
#
# Boar runs on spb1 since 2026-10-07 (rbx1 is only the FidoNet relay). The
# remote user needs sudo. Data in /var/lib/boar is never touched. Without a
# working local Go, the binaries are built in a golang container instead.
set -eu

target="${1:-xtreme@spb1.re-hash.org}"
port="${2:-51622}"
here=$(cd "$(dirname "$0")/.." && pwd)
build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT

echo "building..."
cmds="boar boar-door-example boar-ftn"
if go version >/dev/null 2>&1; then
	for c in $cmds; do
		(cd "$here" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$build/$c" "./cmd/$c")
	done
else
	# A tar stream in and out rather than a bind mount, which not every
	# Docker setup can make of an external volume.
	(cd "$here" && COPYFILE_DISABLE=1 tar --no-xattrs --exclude=.git -cf - .) |
		docker run --rm -i -e GOTOOLCHAIN=auto golang:1.27 sh -eu -c "
			mkdir /src /out && tar -x -C /src 2>/dev/null && cd /src
			for c in $cmds; do
				GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /out/\$c ./cmd/\$c >&2
			done
			tar -C /out -cf - ." | tar -x -C "$build"
fi
cp "$here/deploy/boar.service" "$here/deploy/doors.json" "$build/"

echo "uploading to $target..."
remote_dir=$(ssh -p "$port" "$target" mktemp -d)
scp -q -P "$port" "$build"/* "$target:$remote_dir/"

echo "installing..."
ssh -p "$port" "$target" "sudo sh -eu -s '$remote_dir'" <<'REMOTE'
dir="$1"
id boar >/dev/null 2>&1 || useradd --system --home-dir /var/lib/boar --shell /usr/sbin/nologin boar
# The tosser reads binkd's spool (deploy/install-spb1-ftn.sh sets it up).
getent group ftn >/dev/null && usermod -aG ftn boar
install -d -m 0755 /opt/boar/bin /etc/boar
install -d -m 0700 -o boar -g boar /var/lib/boar /var/lib/boar/art /var/lib/boar/doors
install -m 0755 "$dir/boar" "$dir/boar-door-example" "$dir/boar-ftn" /opt/boar/bin/
[ -e /etc/boar/doors.json ] || install -m 0644 "$dir/doors.json" /etc/boar/doors.json
[ -e /etc/boar/boar.env ] || install -m 0600 /dev/null /etc/boar/boar.env
install -m 0644 "$dir/boar.service" /etc/systemd/system/boar.service
rm -rf "$dir"
systemctl daemon-reload
systemctl enable boar >/dev/null 2>&1
systemctl restart boar
sleep 1
systemctl --no-pager --lines=5 status boar
REMOTE
