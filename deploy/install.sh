#!/bin/sh
# Build Boar BBS for Linux and install or update it on a server over SSH.
#
#   deploy/install.sh user@host
#
# The remote user needs sudo. Data in /var/lib/boar is never touched.
set -eu

target="${1:?usage: deploy/install.sh user@host}"
here=$(cd "$(dirname "$0")/.." && pwd)
build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT

echo "building..."
(cd "$here" &&
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$build/boar" ./cmd/boar &&
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$build/boar-door-example" ./cmd/boar-door-example)
cp "$here/deploy/boar.service" "$here/deploy/doors.json" "$build/"

echo "uploading to $target..."
remote_dir=$(ssh "$target" mktemp -d)
scp -q "$build"/* "$target:$remote_dir/"

echo "installing..."
ssh "$target" "sudo sh -eu -s '$remote_dir'" <<'REMOTE'
dir="$1"
id boar >/dev/null 2>&1 || useradd --system --home-dir /var/lib/boar --shell /usr/sbin/nologin boar
install -d -m 0755 /opt/boar/bin /etc/boar
install -d -m 0700 -o boar -g boar /var/lib/boar /var/lib/boar/art /var/lib/boar/doors
install -m 0755 "$dir/boar" "$dir/boar-door-example" /opt/boar/bin/
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
