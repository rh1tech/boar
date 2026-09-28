#!/bin/sh
# Publish site/ to boar.rh1.tech (nsk1). The page has no build step.
#
#   deploy/site.sh [ssh-host]      # default: nsk1 (see ~/.ssh/config)
#
# A tar stream rather than rsync: macOS's rsync lacks the options this needs.
# The new copy is unpacked beside the live one and swapped in, so a reader
# never sees a half-written site.
set -eu
host="${1:-nsk1}"
cd "$(dirname "$0")/.."
COPYFILE_DISABLE=1 tar --no-xattrs -C site -cf - . | ssh "$host" 'set -e
new=/var/www/boar-site.new
rm -rf "$new" && mkdir -p "$new" && tar -xf - -C "$new"
chmod -R u=rwX,go=rX "$new"
rm -rf /var/www/boar-site.old
[ -d /var/www/boar-site ] && mv /var/www/boar-site /var/www/boar-site.old
mv "$new" /var/www/boar-site && rm -rf /var/www/boar-site.old'
echo "published to $host:/var/www/boar-site"
