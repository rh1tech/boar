# Boar BBS

An old-style bulletin board you call over telnet or SSH. It has ANSI color,
CP437 block art and hotkey menus, plus private mail, public message boards,
live chat, door games, a oneliner wall and tools for the sysop.

It's written in Go and builds to a single binary. Everything is stored in
one SQLite file through a pure-Go driver, so you don't need a C compiler.

```
               ██████╗  ██████╗  █████╗ ██████╗
               ██╔══██╗██╔═══██╗██╔══██╗██╔══██╗
               ██████╔╝██║   ██║███████║██████╔╝
               ██╔══██╗██║   ██║██╔══██║██╔══██╗
               ██████╔╝╚██████╔╝██║  ██║██║  ██║
               ╚═════╝  ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝
```

## Quick start

```sh
go run ./cmd/boar                         # telnet :2323, SSH :2222
ssh -p 2222 bbs@localhost                 # any user name; you log in to the BBS itself
telnet localhost 2323                     # or SyncTERM, NetRunner, PuTTY
```

Type `NEW` at the handle prompt to register. The first account becomes the
sysop, so register before you give anyone the address. Everyone after that
can read but not write until a sysop approves them (see
[New callers](#new-callers)).

The module targets Go 1.27. With the default `GOTOOLCHAIN=auto`, an older
`go` command downloads the right toolchain by itself.

### Flags

| Flag        | Default                     | Meaning                                    |
|-------------|-----------------------------|--------------------------------------------|
| `-telnet`   | `:2323`                     | Telnet address (`""` disables telnet)      |
| `-ssh`      | `:2222`                     | SSH address (`""` disables SSH)            |
| `-host-key` | `data/ssh_host_ed25519_key` | SSH host key, generated on first run       |
| `-data`     | `data/boar.db`              | SQLite database, created if missing        |
| `-name`     | `Boar BBS`                  | BBS name shown to callers                  |
| `-sysop`    |                             | Promote an existing user to sysop at start |
| `-art`      | `data/art`                  | Folder of custom screens                   |
| `-doors`    | `data/doors.json`           | Door games config                          |
| `-doors-dir`| `data/doors`                | Where per-node drop files are written      |
| `-smtp-host`|                             | SMTP relay; empty turns email off          |
| `-smtp-port`| `587`                       | 465 = implicit TLS, otherwise STARTTLS     |
| `-smtp-user`|                             | SMTP login; password from `BOAR_SMTP_PASSWORD` |
| `-smtp-from`|                             | Address emails come from                   |
| `-public-address` |                       | How emails tell people to call, e.g. `bbs.example.com:2222` |
| `-nodes`    | `32`                        | Maximum simultaneous callers               |
| `-approve-new-users` | `true`             | New callers wait for a sysop's approval    |
| `-max-signups` | `20`                     | New accounts per day, across the whole BBS |
| `-idle`     | `15m`                       | Hang up on callers idle this long          |
| `-v`        | off                         | Debug logging                              |

## What callers get

### Mail

The mailbox has an inbox with unread markers and a mode that walks through
new mail in order. You can reply with the original quoted, forward, follow a
conversation as a thread, and search by words, subject or handle. One message
can go to several people (`kasia, bartek`), and sysops can mail `ALL`. The
outbox marks messages the recipient has read with `√`. Each side deletes its
own copy, and a message is gone for good once both have.

### Everything else

- Message boards: public forums with unread counts per caller, a "read all
  new" scan, threaded replies, and a key to mail a post's author privately.
  Sysops can make a board read-only for everyone else, like the
  Announcements board that comes with a new install.
- Chat, with rooms (`/join`, `/who`, `/rooms`, `/me`, `/q`). Other callers'
  lines appear while you type, without mangling what you've typed so far.
- Pages: a one-line message to someone online. They see it at their next
  prompt, or right away if they're in chat.
- News bulletins from the sysop, offered at login when there's something new.
- Door games, external programs such as Legend of the Red Dragon. See
  [Doors](#doors).
- Who's online (with each caller's node, what they're doing and whether they
  came in over SSH), last callers, the user list and the oneliner wall,
  which also shows at login.
- Settings for password, location, terminal type, email and blocked callers.
  Someone you block can't mail or page you, and you won't see their chat
  lines.

The BBS works with UTF-8 terminals, classic CP437 clients and plain ASCII.
Over SSH it picks the terminal type up by itself. It reads the window size
from Telnet NAWS or the SSH pty, and long output pauses at `-- more --`.

## New callers

With `-approve-new-users` (the default), a new account can only read until a
sysop approves it. It can read the boards and news and mail the sysop, but
it can't mail other callers, post, chat, page, open doors, write on the
oneliner wall or set up email.

Sysops who are online get a notice when someone signs up, and the main menu
shows how many are waiting. Sysop menu → New callers lets you approve,
reject (which deletes the account) or mail each one. Promoting someone to
sysop approves them too. Start with `-approve-new-users=false` to let
everyone in straight away.

## SSH keys

Under Settings → SSH keys, callers can paste up to 5 public keys (the
contents of `~/.ssh/id_ed25519.pub`). After that, `ssh -p 2222 bbs@host`
signs them in without a password, and skips any per-handle login backoff.
DSA keys and RSA keys under 2048 bits are refused.

SSH clients without a registered key get the normal BBS login. Whatever
password they send to SSH itself isn't checked.

## Email

Start with `-smtp-host` and `-smtp-from`, and put the SMTP password in
`BOAR_SMTP_PASSWORD`. Gmail and Fastmail both work with an app password.
Without these flags, email stays off.

Callers add an address under Settings → Email. It only counts once they
type the 6-digit code that gets emailed to it. Codes last 15 minutes, only a
hash is stored, and 5 wrong guesses cancel the code. For new BBS mail, each
caller picks one of three settings: off, a short notice (who wrote and the
subject, only while they're offline, at most one every 10 minutes), or a full
copy. "Email me" on any message sends a copy to your own verified address.

Some limits keep it from being abused:

- The BBS only emails addresses their owner has verified, so it can't be
  used as a spam relay. Replies to its emails go nowhere.
- An address can belong to one account, and gets at most 3 code emails a
  day no matter how many accounts ask, so the codes can't be used to flood
  someone's inbox.
- Header values have line breaks stripped out, so a subject can't sneak in
  extra headers. The logs record only the recipient's domain.
- Mail to a remote relay needs TLS with a valid certificate (STARTTLS, or
  implicit TLS on port 465). A relay on the same machine, such as Postfix on
  `127.0.0.1:25`, is used without TLS, because the mail never leaves the
  machine on the way there.

## Custom art

Drop screens into the art folder (`-art`, default `data/art`) to replace the
built-in ones. The BBS reads the folder each time it shows a screen, so you
don't need to restart.

| Screen    | When it's shown         |
|-----------|-------------------------|
| `welcome` | before login            |
| `newuser` | when someone registers  |
| `logon`   | right after login       |
| `goodbye` | when logging off        |

A screen can be `NAME.ans`, ANSI art in CP437 as saved by PabloDraw or
Moebius, or `NAME.txt`, UTF-8 text with the pipe codes described
[below](#screens-and-color-codes). The SAUCE record at the end of `.ans`
files is stripped. CP437 terminals get the original bytes, UTF-8 terminals
get the glyphs converted to Unicode, and plain ASCII callers get
approximations without color.

Add variants such as `welcome.2.ans` or `welcome.3.txt` and one is picked at
random on each call. These tokens are filled in on every screen: `@BBS@`,
`@NODE@`, `@NODES@`, `@ONLINE@`, `@MEMBERS@`, `@HANDLE@`, `@LOCATION@`,
`@CALLS@`, `@TIME@` and `@DURATION@`. Sysop menu → Custom art previews each
file.

## Doors

A door is an external program the BBS hands the caller over to. List doors
in `data/doors.json`; `doors.example.json` shows the format.

| Field         | Meaning                                                    |
|---------------|------------------------------------------------------------|
| `key`, `name` | short id and menu name (`description` is optional)         |
| `command`     | program and arguments; `{placeholders}` are filled in       |
| `dir`         | working directory                                          |
| `io`          | `stdio` (native programs) or `tcp` (DOSBox serial over TCP) |
| `max_minutes` | time limit per visit (default 60)                          |
| `single_node` | only one caller at a time                                  |
| `sysop_only`  | hidden from other callers                                  |

The placeholders are `{node}`, `{dropdir}`, `{doorsys}`, `{dorinfo}`,
`{handle}`, `{userid}` and `{minutes}`, plus `{port}` for tcp doors. Doors
also get them as `BOAR_*` environment variables.

Before a door starts, the BBS writes `DOOR.SYS` (the 52-line GAP format) and
`DORINFO1.DEF` into a private folder for that node. The caller's password
never goes into them. While the door runs, its output is converted from
CP437 for UTF-8 callers and the caller's keys are converted to CP437. When
the time limit passes or the caller hangs up, the door is killed along with
anything it started. Doors get a minimal environment, so nothing secret from
the BBS process leaks into them.

To try the example door, Boar Hunt:

```sh
go build -o bin/boar-door-example ./cmd/boar-door-example
cp doors.example.json data/doors.json    # then remove the LORD entry
go run ./cmd/boar                        # press D at the main menu
```

Classic DOS doors like LORD, TradeWars 2002 and Usurper run under
[DOSBox-X](https://dosbox-x.com) with `"io": "tcp"`. The BBS listens on
`127.0.0.1:{port}`, and DOSBox-X connects its emulated serial port there
(`serial1=nullmodem server:127.0.0.1 port:{port}`). Inside DOSBox the door
is set up for COM1 with a FOSSIL driver such as BNU or X00, and the drop
folder is mounted as a DOS drive. The LORD entry in `doors.example.json`
shows roughly what that looks like. The exact DOSBox-X and door settings
depend on the game, and nobody has tried a real DOS door with it yet. You
supply the game files; classic doors are still copyrighted.

## Sysop tools

Press `!` at the main menu. From there a sysop can:

- look up a user, mail them, reset their password, lock or unlock the
  account (locking hangs them up at once), promote or demote them, or
  delete the account after retyping the handle to confirm;
- kick a node, broadcast a line to everyone online, and read the event log
  of logins, failed logins with their IPs, signups and every sysop action;
- create and delete boards, post and delete news, clean up the oneliner
  wall and preview custom art.

Sysop rights are checked again on every action, so a demotion takes effect
straight away.

## Layout

```
cmd/boar/          entrypoint: flags, signals, telnet + SSH listeners
cmd/boar-door-example/  Boar Hunt, a small example door
internal/telnet/   Telnet protocol: IAC, option negotiation, NAWS, CR/LF
internal/term/     pipe color codes → ANSI, UTF-8/CP437/ASCII, sanitising
internal/store/    SQLite: users, blocks, mail, boards, news, oneliners, events,
                   email verification
internal/mailer/   SMTP sending and the background mail queue
internal/doors/    door config, drop files, running door programs
internal/bbs/      server, SSH transport, nodes, sessions, menus, mail,
                   boards, chat, sysop tools, screen layout
internal/bbs/art/  built-in screens (*.ans), embedded into the binary
```

The schema is versioned with `PRAGMA user_version`. Migrations live in
`internal/store/schema.go` and are only ever appended to.

### Screens and color codes

The built-in screens in `internal/bbs/art/` and custom `.txt` screens are
UTF-8 text with Renegade/Mystic-style pipe codes:

| Code          | Effect                                      |
|---------------|---------------------------------------------|
| `\|00`–`\|15` | foreground (DOS palette, `\|08`+ is bright) |
| `\|16`–`\|23` | background                                  |
| `\|CL`        | clear screen                                |
| `\|RE`        | reset colors                                |
| `\|\|`        | a literal `\|`                              |

Tokens like `@HANDLE@` are replaced when the screen is shown, and their
values are escaped.

Modern terminals get these colors as 256-color codes that match the VGA
palette. The classic codes draw bold black as black and brown as olive on
most of them. CP437 clients keep the classic codes they expect.

## Security notes

Prefer SSH. Telnet is plaintext, and telnet callers are warned about it at
signup. SSH authentication is left open on purpose: callers log in to the
BBS inside the encrypted channel, so any SSH user name works. The host key
is Ed25519, created on first run with mode `0600`. Back it up, because
callers' SSH clients remember it and will complain if it changes.

Passwords are hashed with PBKDF2-HMAC-SHA256 (600,000 iterations, random
salt) and must be at least 8 characters. An unknown handle takes as long to
reject as a wrong password. User text has control characters stripped when
it's stored and again when it's shown, and pipe codes are escaped, so nobody
can send escape sequences to someone else's terminal. All SQL is
parameterised.

Rate limits:

- Per IP: 5 failed logins per 15 minutes (after which new connections from
  that address are refused too) and 3 signups per hour. IPv6 addresses count
  per /64, so rotating through a subnet doesn't help.
- Per handle: after 3 failed logins from anywhere, each further try waits
  longer (2 s, 4 s, 8 s and so on, up to a minute). Parallel guesses against
  one handle are refused while a check is running, and a successful login
  clears the count. Unknown handles are treated the same way, so this
  reveals nothing, and the most a guesser can do to the real owner is make
  them wait a minute.
- Across the BBS: at most `-max-signups` new accounts a day.
- Per user: mail (30 recipients an hour), posts, pages, chat lines and
  oneliners. Sysops are exempt.

The login, signup and code-email limits are kept in the database, so a
restart doesn't reset them.

A wrong password and a locked account get the same message, so guessing a
locked account's password correctly tells an attacker nothing. Locked
callers who sign in with an SSH key are told directly, since the key already
proves who they are.

Callers get 5 minutes to log in and are hung up when idle. The SSH handshake
has 30 seconds, and a write watchdog drops clients that stop reading. Open
connections are capped in total and at 8 per IP before login, so a flood of
half-open connections is dropped immediately. Nodes have their own cap.

Privileged database operations check that the acting user is a sysop, and
refuse to lock, demote or delete the sysop's own account. The menus check
too; this is a second line of defence. The user list stops at 1,000 users
and each board at its newest 500 posts, so a huge table can't make every
visit slow. The database, its WAL files and the host key are all created
with mode `0600`.

## Deployment

`deploy/install.sh user@host` builds Linux binaries and installs or updates
Boar BBS on a Debian-style server over SSH. The remote user needs sudo. The
script sets up:

- the binaries in `/opt/boar/bin`, a `boar` system user, and a locked-down
  systemd unit (`deploy/boar.service`) with telnet on port 23 and SSH on
  2222;
- config in `/etc/boar`: `doors.json` (installed once, with Boar Hunt) and
  `boar.env`, where `BOAR_ARGS` adds flags and `BOAR_SMTP_PASSWORD` goes;
- data in `/var/lib/boar`: the database, the SSH host key, custom art and
  drop files. Updates never touch it.

Open ports 23 and 2222 in the firewall. Logs: `journalctl -u boar -f`.

## Development

```sh
git config core.hooksPath .githooks   # once per clone
go test -race -cover ./...
go vet ./...
```

The `commit-msg` hook in `.githooks/` rejects commit messages with
co-author trailers or other traces of AI tools.

The `bbs` tests start real telnet and SSH servers on random ports and drive
them the way callers would, for example two users chatting or a sysop
locking someone out while they're online.

## License

Copyright (C) 2026 Mikhail Matveev

Boar BBS is free software: you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free
Software Foundation, either version 3 of the License, or (at your option)
any later version. It is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See [LICENSE](LICENSE)
for the full text.
