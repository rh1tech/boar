#!/bin/sh
# Publish site/ to boar.rh1.tech (spb1, since 2026-10-07). No build step.
#
#   deploy/site.sh [ssh-target]    # default: xtreme@spb1.re-hash.org
#   BOAR_SSH_PORT                  # default 51622 (the router's SSH forward)
#   BOAR_SSH_IDENTITY              # optional path to ssh key
#
# A tar stream rather than rsync: macOS's rsync lacks the options this needs.
# The new copy is unpacked beside the live one and swapped in, so a reader
# never sees a half-written site. Remote steps use sudo (xtreme on spb1).
set -eu
host="${1:-xtreme@spb1.re-hash.org}"
port="${BOAR_SSH_PORT:-51622}"
ident="${BOAR_SSH_IDENTITY:-$HOME/.ssh/spb1_ed25519}"
ssh_cmd="ssh -p $port -i $ident"
cd "$(dirname "$0")/.."

$ssh_cmd "$host" 'sudo sh -eu -c "
rm -rf /var/www/boar-site.new && mkdir -p /var/www/boar-site.new
"'
COPYFILE_DISABLE=1 tar --no-xattrs -C site -cf - . \
  | $ssh_cmd "$host" 'sudo tar -C /var/www/boar-site.new -xf -'
$ssh_cmd "$host" 'sudo sh -eu -c "
chmod -R u=rwX,go=rX /var/www/boar-site.new
rm -rf /var/www/boar-site.old
[ -d /var/www/boar-site ] && mv /var/www/boar-site /var/www/boar-site.old
mv /var/www/boar-site.new /var/www/boar-site
rm -rf /var/www/boar-site.old
"'
echo "published to $host:/var/www/boar-site"
