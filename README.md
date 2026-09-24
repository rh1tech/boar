# Boar BBS

A bulletin board in the old style, reachable over **telnet and SSH**: ANSI
color, CP437 block art, hotkey menus, private **mail**, public **message
boards**, live **chat**, **door games**, a oneliner wall and a set of sysop
tools.

It's written in Go and builds to one binary. All data lives in one SQLite
file, via a pure-Go driver, so there's no C compiler step.

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

Type `NEW` at the handle prompt to register. **The first account becomes the
sysop.** Everyone after that can read but not write until a sysop approves
them (see [New callers](#new-callers)).

The module targets Go 1.27. With the default `GOTOOLCHAIN=auto`, an older
`go` command downloads the right toolchain for this project by itself.

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

**Messages**
- **Mailbox**:
  - Inbox with unread markers, and a mode that reads new mail in order.
  - Reply with quoting, forward, and conversation **threads**.
  - **Search** by words, subject or handle.
  - Send to several people at once (`kasia, bartek`). Sysops can mail `ALL`.
  - Outbox with **read receipts** (`√`).
  - Each side deletes its own copy; a message is purged once both have.
- **Message boards**: public forums with per-caller unread counts, a
  "read all new" scan, threaded replies, and mailing a post's author. Sysops
  can make a board read-only for everyone else, like the seeded
  *Announcements*.
- **Chat**: a live teleconference with rooms (`/join`, `/who`, `/rooms`,
  `/me`, `/q`). Lines from other callers appear while you type without
  garbling your half-typed line.
- **Pages**: a one-line message to someone online now. It shows at their next
  prompt, or right away if they're in chat.
- **News**: bulletins from the sysop. New ones are offered at login.
- **Door games**: external programs such as Legend of the Red Dragon; see
  [Doors](#doors).

**People**
- Who's online (node, activity, and whether they connected over SSH), last
  callers, and the user list.
- **Oneliner wall**, shown at login.
- **Settings**: password, location, terminal type, **blocked callers** and
  **email**. A blocked caller can't mail or page you, and you don't see
  their chat lines.

**Terminals**: UTF-8 with color, CP437 with color (classic BBS clients), or
plain ASCII. Over SSH the terminal type is detected automatically. Window
size comes from Telnet NAWS or the SSH pty, and long output pauses at
`-- more --`.

## New callers

With `-approve-new-users` (the default), a new account can only read until a
sysop approves it. It can read the boards and news, and mail the sysop, but
it can't mail other callers, post, chat, page, open doors, write on the
oneliner wall or set up email. Online sysops get a notice when someone signs
up, and the main menu shows how many are waiting. Under **Sysop menu → New
callers**, sysops approve, reject (delete) or mail each one. Promoting
someone to sysop approves them too. Start with `-approve-new-users=false` to
let everyone in straight away.

## SSH keys

Callers can add up to 5 public keys under **Settings → SSH keys**, by pasting
the contents of `~/.ssh/id_ed25519.pub`. After that, `ssh -p 2222
bbs@host` signs them in with no password, and the key also gets them past
any per-handle login backoff. DSA keys and RSA keys under 2048 bits are
refused. SSH clients without a registered key get the normal BBS login;
whatever password they send to SSH itself isn't checked.

## Email

Start with `-smtp-host` and `-smtp-from`, and put the SMTP password in
`BOAR_SMTP_PASSWORD`. Gmail and Fastmail both work with an app password.
Without these flags, email features stay off.

- Callers add an address under **Settings → Email**. It only counts once
  they type the 6-digit code emailed to it. Codes last 15 minutes, only a
  hash is stored, and 5 wrong guesses cancel the code.
- For new BBS mail, each caller chooses **off**, a **notice** (sender and
  subject, sent only when they're offline, at most one every 10 minutes), or
  a **full copy**.
- **Email me** on any message sends a copy to your own verified address.
- The BBS never sends to an address its owner hasn't verified, so it can't be
  used as a spam relay. Replies to these emails aren't delivered anywhere.
- Mail to a remote relay only goes out over TLS with a valid certificate
  (STARTTLS, or implicit TLS on port 465). A relay on the same machine, such
  as a local Postfix on `127.0.0.1:25`, is used without TLS, since the mail
  never leaves the machine on its way there.
- An address can belong to one account only, and it gets at most 3 code
  emails a day however many accounts ask, so the code emails can't be used
  to flood someone's inbox. Header values are stripped of line breaks, so a subject can't
  add headers of its own. Logs record only the recipient's domain.

## Custom art

Drop screens into the art folder (`-art`, default `data/art`) to replace the
built-in ones. The BBS picks a file up the next time it shows that screen,
so there's no need to restart.

| Screen    | When it's shown         |
|-----------|-------------------------|
| `welcome` | before login            |
| `newuser` | when someone registers  |
| `logon`   | right after login       |
| `goodbye` | when logging off        |

- **`NAME.ans`** is ANSI art in CP437, as saved by PabloDraw or Moebius. The
  SAUCE metadata record is removed.
  - CP437 terminals get the original bytes.
  - UTF-8 terminals get the glyphs converted to Unicode.
  - Plain-ASCII callers get approximations without color.
- **`NAME.txt`** is UTF-8 text with the pipe codes described
  [below](#screens-and-color-codes).
- **Variants:** `welcome.2.ans`, `welcome.3.txt` and so on. One is picked at
  random for each call.
- **Tokens:** `@BBS@`, `@NODE@`, `@NODES@`, `@ONLINE@`, `@MEMBERS@`,
  `@HANDLE@`, `@LOCATION@`, `@CALLS@`, `@TIME@` and `@DURATION@` are filled
  in on every screen.
- Sysops can preview every screen under **Sysop menu → Custom art**.

## Doors

Doors are external programs the BBS hands a caller to. List them in
`data/doors.json`; `doors.example.json` shows the format.

| Field         | Meaning                                                    |
|---------------|------------------------------------------------------------|
| `key`, `name` | short id and menu name (`description` is optional)         |
| `command`     | program and arguments; `{placeholders}` are filled in       |
| `dir`         | working directory                                          |
| `io`          | `stdio` (native programs) or `tcp` (DOSBox serial over TCP) |
| `max_minutes` | time limit per visit (default 60)                          |
| `single_node` | only one caller at a time                                  |
| `sysop_only`  | hidden from other callers                                  |

Placeholders: `{node}`, `{dropdir}`, `{doorsys}`, `{dorinfo}`, `{handle}`,
`{userid}`, `{minutes}`, and `{port}` for tcp doors. They're also passed as
`BOAR_*` environment variables.

Before starting a door, the BBS writes `DOOR.SYS` (the 52-line GAP format)
and `DORINFO1.DEF` into a private folder for each node. Your password is
never written into them. While the door runs:

- Output is translated from CP437 for UTF-8 callers, and typed characters
  are translated to CP437.
- At the time limit, or if the caller hangs up, the door and everything it
  started are killed.
- Doors get a minimal environment, so no secrets from the BBS process leak
  into them.

**Try the example door**, Boar Hunt:

```sh
go build -o bin/boar-door-example ./cmd/boar-door-example
cp doors.example.json data/doors.json    # then remove the LORD entry
go run ./cmd/boar                        # press D at the main menu
```

**Classic DOS doors** (LORD, TradeWars 2002, Usurper) run under
[DOSBox-X](https://dosbox-x.com) using `"io": "tcp"`. The BBS listens on
`127.0.0.1:{port}` and DOSBox-X connects its emulated serial port there
(`serial1=nullmodem server:127.0.0.1 port:{port}`). The door itself is set
up for COM1 with a FOSSIL driver such as BNU or X00, with the drop folder
mounted as a DOS drive. The LORD entry in `doors.example.json` shows the
shape of it, but the exact DOSBox-X and door settings depend on the game
and haven't been tested with a real DOS door yet. **You supply the game files;
classic doors are copyrighted.**

## Sysop tools

Press `!` at the main menu:

- **Manage a user**: see their details, mail them, reset their password,
  lock or unlock the account (a lock hangs them up at once), promote or
  demote, and delete (you retype the handle to confirm).
- **Kick a node**, **broadcast** a line to everyone online, and read the
  **event log** (logins, failed logins with IPs, signups, and every sysop
  action).
- Create and delete **boards**, post and delete **news** bulletins, clean
  up the **oneliner wall**, and preview **custom art**.

Sysop rights are re-checked on every action, so a demotion takes effect
immediately.

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
                   boards, chat, sysop tools
internal/bbs/art/  screens (*.ans), embedded into the binary
```

The schema is versioned with `PRAGMA user_version`. Migrations live in
`internal/store/schema.go` and are append-only.

### Screens and color codes

Screens in `internal/bbs/art/` are UTF-8 text with Renegade/Mystic-style
pipe codes:

| Code          | Effect                                      |
|---------------|---------------------------------------------|
| `\|00`–`\|15` | foreground (DOS palette, `\|08`+ is bright) |
| `\|16`–`\|23` | background                                  |
| `\|CL`        | clear screen                                |
| `\|RE`        | reset colors                                |
| `\|\|`        | a literal `\|`                              |

Tokens like `@HANDLE@` are replaced at display time, and their values are
escaped.

## Security notes

- **Prefer SSH.** Telnet is plaintext, and telnet callers are warned at
  signup. SSH authentication is left open on purpose: callers log in to the
  BBS inside the encrypted channel, so any SSH user name works. The host key
  is Ed25519, generated on first run with mode `0600`. Back it up, because
  callers pin it.
- Passwords are PBKDF2-HMAC-SHA256 (600k iterations, random salt). An unknown
  handle takes as long to reject as a wrong password.
- User text has control characters stripped when stored and again when
  displayed, and pipe codes are escaped, so nobody can send escape sequences
  to someone else's terminal. All SQL is parameterised.
- Passwords must be at least 8 characters.
- **Rate limits:**
  - Per IP: failed logins (5 per 15 minutes, which also blocks new
    connections) and signups (3 per hour). IPv6 addresses count per /64, so
    rotating through a subnet doesn't help.
  - Per handle: after 3 failed logins, from anywhere, each further try waits
    longer: 2 s, 4 s, 8 s and so on, up to one minute. Parallel guesses
    against one handle are refused while a check is running. A successful
    login clears it. Unknown handles are treated the same way, so this
    reveals nothing, and the most a guesser can do to the real owner is make
    them wait a minute.
  - Whole BBS: at most `-max-signups` new accounts a day.
  - Per user: mail (30 recipients per hour), posts, pages, chat lines and
    oneliners. Sysops are exempt.
  - The login, signup and code-email limits are saved in the database, so
    restarting the BBS doesn't reset them.
- A wrong password and a locked account get the same message, so guessing
  a locked account's password right reveals nothing. Locked callers who sign
  in with an SSH key are told directly, since the key proves who they are.
- **Timeouts:** 5 minutes to log in, a hang-up when idle, 30 seconds for the
  SSH handshake, and a write watchdog for clients that stop reading.
- **Connection caps:** open connections are capped in total and per IP
  (8), before login, so floods of half-open connections are dropped at once.
  Nodes are capped separately.
- **Sysop checks in the store too:** privileged database operations check
  that the acting user is a sysop themselves, and refuse to lock, demote or
  delete yourself, as a backstop behind the menus.
- User and board listings are capped (1000 users, the newest 500 posts per
  board), so a huge table can't make every visit expensive.
- The database, its WAL files and the host key are all created with mode
  `0600`.

## Deployment

`deploy/install.sh user@host` builds Linux binaries and installs or updates
Boar BBS on a Debian-style server over SSH (the remote user needs sudo):

- binaries in `/opt/boar/bin`, a `boar` system user, and a hardened systemd
  unit (`deploy/boar.service`) with telnet on port 23 and SSH on 2222;
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
them the way callers would: two users chatting, a sysop locking someone who
is online, and so on.
