# Running Vibrance

This guide is for whoever installs and runs Vibrance. Vibrance runs next to [Vibrance MusicLib](https://github.com/tommasonovelli/vibrance-musiclib), in one Docker Compose stack: MusicLib imports your music and writes its library, and Vibrance serves that library to listen to, with accounts, favorites, playlists and search. Read the guide from top to bottom the first time: each section builds on the one before. Every command is explained, with what a good result looks like.

The examples use the same values everywhere, so you can copy them and change only what differs on your machine:

| Example | What it stands for |
|---|---|
| `~/musiclib` | the folder that holds `compose.yaml` and `.env`. Run every command of this guide from there (`cd ~/musiclib`). |
| `192.168.1.20` | the machine's address on your home network |
| `example.com` | your domain; `vibrance.example.com` and `musiclib.example.com` are the two names under it |
| `2026-10-05-2130` | the name of a backup |
| `anna` | an account of Vibrance |

MusicLib has its own guide, [operations.md of MusicLib](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md): this one says what is different when Vibrance is in the stack, and refers to it for the rest. For building and testing Vibrance itself, and for how the server works inside, see the [developer guide](development.md).

## Contents

1. [What you need](#what-you-need)
2. [Install](#install)
3. [Configuration](#configuration)
4. [Adopting an existing MusicLib installation](#adopting-an-existing-musiclib-installation)
5. [The passwords and the accounts](#the-passwords-and-the-accounts)
6. [Access from other devices](#access-from-other-devices)
7. [Backups, restore and moving](#backups-restore-and-moving)
8. [Upgrading](#upgrading)
9. [Troubleshooting](#troubleshooting)

## What you need

The stack needs what MusicLib needs ([MusicLib's guide, "What you need"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#what-you-need)):

- **Ubuntu 24.04 or later** on an **amd64** (x86-64) machine.
- **Docker Engine** with the **Compose v2** plugin (`docker compose`), Compose 2.24 or later for [Caddy](#https-and-two-names). Your user must be able to run `docker` without `sudo`.
- **Local ext4 storage** for Docker's own storage (`/var/lib/docker`, where the named volumes live). Vibrance keeps its database in a named volume, and SQLite does not work on network filesystems (NFS, SMB).
- `curl` and `openssl`: `sudo apt install curl openssl` if they are missing.
- Preferably a **second disk** for backups.

You do not install Go, FFmpeg or any other tool on the machine: everything runs in Docker. Docker Desktop, NAS and network filesystems, and ARM machines are not supported, as for MusicLib.

Check the machine before you install:

```sh
uname -m                                   # must print x86_64
docker compose version                     # prints "Docker Compose version v2...." (2.24 or later)
findmnt -no FSTYPE -T /var/lib/docker      # must print ext4
```

## Install

### First start

An installation needs two files of a Vibrance release: `compose.yaml` and `env.example`. `compose.yaml` is MusicLib's own `compose.yaml`, unchanged, plus the service `vibrance` and its two volumes; `env.example` is MusicLib's `.env.example` plus Vibrance's settings. Nothing is built.

Already running MusicLib? Read [Adopting an existing MusicLib installation](#adopting-an-existing-musiclib-installation) instead.

Paste this block into a terminal:

```sh
mkdir -p ~/musiclib/import && cd ~/musiclib
[ -e compose.yaml ] || curl -fsSLO https://github.com/tommasonovelli/vibrance/releases/latest/download/compose.yaml
[ -e .env ] || { curl -fsSL -o .env https://github.com/tommasonovelli/vibrance/releases/latest/download/env.example && chmod 600 .env && sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 32)/" .env && sed -i "s|^MUSICLIB_PASSWORD=.*|MUSICLIB_PASSWORD=$(openssl rand -base64 24)|" .env && sed -i "s|^VIBRANCE_ADMIN_PASSWORD=.*|VIBRANCE_ADMIN_PASSWORD=$(openssl rand -base64 24)|" .env; }
docker compose up -d --wait
```

What each line does:

1. Creates `~/musiclib` and MusicLib's import folder `~/musiclib/import` in it, then enters `~/musiclib`.
2. Downloads `compose.yaml`, the description of the three containers, unless the folder already has one.
3. Downloads `env.example` as `.env`, your settings file, unless the folder already has one. It makes `.env` readable by you only (`chmod 600`), then writes three random passwords into it, without printing them: the database password of MusicLib (`POSTGRES_PASSWORD`), the sign-in password of MusicLib (`MUSICLIB_PASSWORD`) and the password of Vibrance's first account, the admin (`VIBRANCE_ADMIN_PASSWORD`).
4. Starts the stack. The first time, Docker downloads the images. The command returns when the three containers report that they are ready.

The block is safe to paste twice: it never replaces an existing `compose.yaml` or `.env`.

What the block creates:

- in `~/musiclib`: `compose.yaml`, `.env` and the empty folder `import`;
- in Docker: the containers `musiclib` (MusicLib), `musiclib-db` (MusicLib's PostgreSQL) and `musiclib-vibrance` (Vibrance), and the volumes `musiclib_db`, `musiclib_data` (MusicLib's originals and library), `musiclib_backup` (MusicLib's backups), `musiclib_vibrance_state` (Vibrance's database) and `musiclib_vibrance_backup` (Vibrance's backups).

### Check that it works

```sh
docker compose ps                                    # the three containers show "healthy"
curl -f http://127.0.0.1:8090/health/ready           # Vibrance: prints {"status":"ready"}
curl -f http://127.0.0.1:8080/health/ready           # MusicLib: prints {"status":"ready"}
grep VIBRANCE_ADMIN_PASSWORD .env                    # prints VIBRANCE_ADMIN_PASSWORD=...
```

The admin's name is `admin`, and the password is everything after `=` on the last line.

- **MusicLib** is at **<http://127.0.0.1:8080>**: import your music there ([MusicLib's guide](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#importing-and-the-activity-page)). Its password is in `.env` too: `grep MUSICLIB_PASSWORD .env`.
- **Vibrance** is at **<http://127.0.0.1:8090>**. Version 0.1 has no web player of its own: the address opens the documentation of its API, where you can sign in and try every request, and [api.md](api.md) shows the main ones with `curl`. Apps talk to the same API.

Vibrance reads the library MusicLib writes, read-only, and looks at it again every 5 minutes (`VIBRANCE_SCAN_INTERVAL`): an album imported in MusicLib appears in Vibrance at the latest a few minutes after MusicLib has written it. An admin can ask for a scan at once with `POST /api/v1/admin/library/scan`, and read the state of the library with `GET /api/v1/admin/library`.

The stack starts again by itself after a reboot, unless you stopped it. If `docker compose up -d --wait` reports a container as unhealthy, read its log (`docker compose logs --tail=20 vibrance`, or `app` for MusicLib) and look up the `code` in [Troubleshooting](#troubleshooting).

### Everyday commands

Run them from `~/musiclib`:

| Task | Command | Good result |
|---|---|---|
| Stop everything | `docker compose stop` | the three containers stopped; nothing is deleted |
| Start it again | `docker compose up -d --wait` | returns when the containers are healthy |
| Stop or start Vibrance only | `docker compose stop vibrance`, `docker compose start vibrance` | MusicLib is not touched |
| Show the status | `docker compose ps` | `musiclib`, `musiclib-db` and `musiclib-vibrance` show `healthy` |
| Read Vibrance's log | `docker compose logs --tail=100 vibrance` | JSON lines; `"level":"ERROR"` lines carry a `code` |
| Follow Vibrance's log | `docker compose logs -f vibrance` | new lines appear as they come; Ctrl-C stops following |
| Check that Vibrance is ready | `curl -f http://127.0.0.1:8090/health/ready` | `{"status":"ready"}` |
| Show Vibrance's version | `docker compose run --rm --no-deps vibrance version` | `version: …` |
| Show the admin's first password | `grep VIBRANCE_ADMIN_PASSWORD .env` | `VIBRANCE_ADMIN_PASSWORD=…` |
| List Vibrance's accounts | `docker compose exec vibrance vibrance user list` | one line per account |

Vibrance works with MusicLib stopped: `docker compose stop app`, which MusicLib's backups use, does not touch it. It goes on serving what it has indexed; only the files MusicLib is rewriting at that moment may answer `503 library_changing` for a few seconds.

Stopping Vibrance is safe at any moment. It stops at once when nothing is being served. A request that is still open, such as a track that is playing, gets 10 seconds to end; then Vibrance closes it, closes its database and exits. `compose.yaml` gives the container 15 seconds for all of this (`stop_grace_period`) before Docker kills it, and a Vibrance that is killed loses nothing: the next start recovers.

> **Never run `docker compose down -v`: it deletes your library, both databases and the backup volumes.** To stop the stack, use `docker compose stop`. Plain `docker compose down` removes the containers (not the volumes); `docker compose up -d --wait` creates them again.

## Configuration

All settings are in `.env`, next to `compose.yaml`. `env.example` lists them, with comments: MusicLib's first, then Vibrance's. After changing `.env`, apply it:

```sh
docker compose up -d --wait
```

Compose recreates the containers whose settings changed and keeps the volumes. MusicLib's settings are explained in [MusicLib's guide, "Configuration"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#configuration). Vibrance's:

| Variable | Default | Example | What it does |
|---|---|---|---|
| `VIBRANCE_ADMIN_PASSWORD` | none: required at the first start | the output of `openssl rand -base64 24` | The password of the first admin, 12 to 1024 bytes. Read only while Vibrance has no account: see [The passwords and the accounts](#the-passwords-and-the-accounts). |
| `VIBRANCE_PUBLIC_ORIGIN` | `http://127.0.0.1:8090` (the port is `VIBRANCE_PORT`) | `http://192.168.1.20:8090`, `https://vibrance.example.com` | The exact address clients open: scheme, host and port, lowercase, nothing else. Vibrance answers only requests for this address. |
| `VIBRANCE_ADMIN_USERNAME` | `admin` | `anna` | The name of the first admin: 3 to 32 of `a-z`, `0-9`, `.`, `_`, `-`, beginning with a letter or a digit. Read only while Vibrance has no account. |
| `VIBRANCE_BIND` | `127.0.0.1` | `192.168.1.20` | The host address Vibrance listens on. `127.0.0.1`: this machine only. |
| `VIBRANCE_PORT` | `8090` | `8091` | The host port Vibrance listens on. Change the port in `VIBRANCE_PUBLIC_ORIGIN` with it. |
| `VIBRANCE_SCAN_INTERVAL` | `5m` | `30m` | The time between two scans of MusicLib's library: a duration such as `90s`, `10m` or `1h`, at least `30s`. |
| `VIBRANCE_WORKERS` | empty: the number of CPUs, at least 1 and at most 4 | `2` | How many `ffmpeg` and `ffprobe` processes, and albums being indexed, run at once, 1 to 16. |
| `VIBRANCE_BACKUP` | `vibrance_backup` (the named volume `musiclib_vibrance_backup`) | `/mnt/backup/vibrance` | Where Vibrance's backups go (`/backup` in its container): a named volume, or the absolute path of a folder owned by uid 1000, preferably on another disk. |
| `VIBRANCE_UID`, `VIBRANCE_GID` | `1000`, `1000` | `1001`, `1001` | The user and group Vibrance runs as. Never `0`: Vibrance refuses to run as root (`run_as_root`). See below before changing them. |
| `MUSICLIB_DATA` | `data` (the named volume `musiclib_data`) | `/srv/musiclib/data` | MusicLib's setting: Vibrance mounts the same volume or folder, read-only, at `/musiclib`. |
| `COMPOSE_FILE` | `compose.yaml` | `compose.yaml:compose.caddy.yaml` | Compose's own setting: which Compose files plain `docker compose` commands use. Only for [Caddy](#https-and-two-names). |
| `CF_API_TOKEN` | empty | a Cloudflare API token | Only for [Caddy](#https-and-two-names): the token Caddy uses to obtain the certificate. |

Example: to use port 8091 instead of 8090, change two lines of `.env` together, then apply:

```sh
sed -i 's|^#\?VIBRANCE_PORT=.*|VIBRANCE_PORT=8091|' .env
sed -i 's|^VIBRANCE_PUBLIC_ORIGIN=.*|VIBRANCE_PUBLIC_ORIGIN=http://127.0.0.1:8091|' .env
docker compose up -d --wait
```

The first `sed` sets `VIBRANCE_PORT` (the line starts with `#` in `env.example`, which the `\?` also matches); the second sets `VIBRANCE_PUBLIC_ORIGIN`.

**Another uid.** A new named volume is created owned by uid 1000, whatever `VIBRANCE_UID` says, so Vibrance with another uid cannot write its state (`state_unwritable`). After setting `VIBRANCE_UID` and `VIBRANCE_GID`, give the state volume to them once, then start Vibrance:

```sh
docker compose run --rm --no-deps --user 0:0 --cap-add CHOWN --entrypoint chown vibrance -R 1001:1001 /var/lib/vibrance
docker compose up -d --wait
```

`VIBRANCE_BACKUP` must then be a host folder owned by that uid, and MusicLib's library must be readable by it.

Rules for `.env`, as for MusicLib:

- **Unix (LF) line endings only.** A `.env` saved with Windows (CRLF) line endings makes Vibrance refuse its password (`admin_password_invalid`) or its address (`config_invalid`). `grep -c $'\r' .env` prints `0` for a good file; to fix one, run `sed -i 's/\r$//' .env`.
- One `NAME=value` per line, without spaces around `=`. A line that starts with `#` is a comment: the setting keeps its default.
- It holds three passwords: keep it readable by you only (`chmod 600 .env`).

### What Compose passes to Vibrance

You do not set these: `compose.yaml` does.

- `VIBRANCE_HTTP_ADDR` is `:8080` inside the container; `VIBRANCE_BIND` and `VIBRANCE_PORT` choose where it is published on the host.
- `/musiclib` is MusicLib's data (`MUSICLIB_DATA`), read-only: Vibrance reads `library/` and the two markers `.maintenance` and `.musiclib-store`, and never writes there.
- `/var/lib/vibrance` is the volume `musiclib_vibrance_state`: the database `vibrance.db` and `thumbs/`, a cache of cover thumbnails that can be deleted at any time.
- `/backup` is `VIBRANCE_BACKUP`.
- The container runs with a read-only root filesystem, no capabilities, and `init` to collect the `ffmpeg` and `ffprobe` processes. Its health check is `vibrance healthcheck`, which asks `/health/ready` from inside.
- `depends_on` makes Compose create MusicLib's container first, only so that a new data volume belongs to MusicLib. Vibrance does not wait for MusicLib and does not need it to run.

## Adopting an existing MusicLib installation

An installation of MusicLib made with its own `compose.yaml` adopts Vibrance without moving anything: the stack is the same Compose project, `musiclib`, with the same containers and volumes, plus Vibrance.

1. **Check MusicLib's version.** The stack runs the MusicLib version its release names (MusicLib 1.2.0 for this release of Vibrance; [compat.md](compat.md) lists them):

   ```sh
   cd ~/musiclib
   docker compose run --rm --no-deps app version     # version: 1.2.0
   ```

   - The same version: go on.
   - An older one: adopting is also an upgrade of MusicLib. Read MusicLib's own upgrade notes first ([MusicLib's guide, "Upgrading"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#upgrading)), and make the backup of step 2.
   - A newer one: wait for a release of Vibrance that names it. MusicLib cannot go back to an older version.
   - A source build (`COMPOSE_FILE=compose.dev.yaml` in `.env`) cannot be adopted: this stack runs MusicLib's published image.

2. **Back up MusicLib** ([MusicLib's guide, "Making a backup"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#making-a-backup)):

   ```sh
   docker compose stop app && docker compose run --rm --no-deps app backup --to "/backup/before-vibrance-$(date +%F-%H%M)"
   ```

3. **Replace `compose.yaml`, keep `.env`, and add Vibrance's settings to `.env`:**

   ```sh
   cp compose.yaml compose.yaml.before-vibrance
   curl -fsSLO https://github.com/tommasonovelli/vibrance/releases/latest/download/compose.yaml
   curl -fsSLO https://github.com/tommasonovelli/vibrance/releases/latest/download/env.example
   grep -q '^VIBRANCE_ADMIN_PASSWORD=.' .env || { sed -i '/^VIBRANCE_ADMIN_PASSWORD=/d' .env && printf '\nVIBRANCE_ADMIN_PASSWORD=%s\n' "$(openssl rand -base64 24)" >> .env; }
   grep -q '^VIBRANCE_PUBLIC_ORIGIN=' .env || printf 'VIBRANCE_PUBLIC_ORIGIN=http://127.0.0.1:8090\n' >> .env
   ```

   - The first line keeps your old `compose.yaml`. If you changed it by hand (the database password, for example), move those changes to `.env` now: the new `compose.yaml` is MusicLib's file as released.
   - `env.example` is for reference: its last block lists Vibrance's settings, which you can copy to `.env`.
   - The fourth line adds a random admin password, unless `.env` already sets one; the fifth adds Vibrance's address.

4. **Start the stack:** `docker compose up -d --wait`. MusicLib's containers keep their volumes; Vibrance starts, indexes the whole library in the background, and is ready at once. The first indexing reads every track with `ffprobe` and `ffmpeg`: on a large library it takes a while, and `GET /api/v1/admin/library` shows how far it has come.

If you put Caddy in front of MusicLib with a `compose.override.yaml` ([MusicLib's guide, "Caddy in Docker"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#caddy-in-docker)), Compose still reads it, and MusicLib keeps working through it. To give Vibrance a name too, either add `vibrance: { ports: !reset [] }` to that override and a second name to your `Caddyfile`, as in [HTTPS and two names](#https-and-two-names), or move to `compose.caddy.yaml` and delete the override (keep Caddy's volume, `musiclib_caddy_data`, which both files use).

MusicLib's own guide stays valid for MusicLib: its commands (`docker compose stop app`, `docker compose run --rm --no-deps app …`) act on MusicLib only. The one exception is upgrading: see [Upgrading](#upgrading).

## The passwords and the accounts

Vibrance has its own accounts, made by an admin; there is no self sign-up. The first admin is created at the first start, on an empty database, from `VIBRANCE_ADMIN_USERNAME` and `VIBRANCE_ADMIN_PASSWORD`. **Once an account exists, the two settings are ignored**: changing them in `.env` changes nothing. Over plain HTTP the passwords cross the network in clear: see [Access from other devices](#access-from-other-devices).

An admin manages the accounts with the API (`/api/v1/admin/users`, in the documentation at <http://127.0.0.1:8090>). The same can be done from the machine, while Vibrance runs; the password is read from the terminal, never from the command line:

```sh
read -rs -p 'Password: ' P && echo
printf '%s' "$P" | docker compose exec -T vibrance vibrance user create --username anna --role user --password-stdin
unset P
```

- `--role admin` makes an admin. A name is 3 to 32 of `a-z`, `0-9`, `.`, `_`, `-`; a password 12 to 1024 bytes.
- `user reset-password --username anna --password-stdin`, used the same way, sets a new password and signs the account out everywhere. It is how to recover the admin's password: `--username admin`.
- `docker compose exec vibrance vibrance user list` lists the accounts.
- Exit code 0 when done, 2 for an invalid name, role or password, 1 otherwise (`username_taken`, `user_not_found`).

Each user changes their own password with `PUT /api/v1/me/password`. MusicLib's two passwords are explained in [MusicLib's guide, "The passwords"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#the-passwords).

## Access from other devices

By default Vibrance listens only on `127.0.0.1:8090`, and MusicLib on `127.0.0.1:8080`: only the machine they run on can open them. `VIBRANCE_PUBLIC_ORIGIN` must match the address clients use exactly: `http://localhost:8090` and `http://127.0.0.1:8090` are different addresses, and with the default setting only the second one works. Any other address answers `421 host_not_allowed`.

| Way | Encryption | Settings |
|---|---|---|
| [SSH tunnel](#through-an-ssh-tunnel) | yes (SSH) | none |
| [Home network (LAN)](#on-your-home-network-lan) | **no**: passwords and session cookies cross the network in clear | `VIBRANCE_BIND`, `VIBRANCE_PUBLIC_ORIGIN` |
| [HTTPS and two names](#https-and-two-names) | yes (HTTPS) | `VIBRANCE_PUBLIC_ORIGIN`, `PUBLIC_ORIGIN`, `COMPOSE_FILE`, `CF_API_TOKEN` |

Never expose the ports of Vibrance or MusicLib directly to the Internet.

### Through an SSH tunnel

```sh
ssh -L 8090:127.0.0.1:8090 -L 8080:127.0.0.1:8080 you@server
```

While the connection stays open, `http://127.0.0.1:8090` (Vibrance) and `http://127.0.0.1:8080` (MusicLib) on your computer reach the server.

### On your home network (LAN)

Give the machine a fixed address in your router first. Then set that address in `.env` (the `VIBRANCE_BIND` line starts with `#`: remove it), and run `docker compose up -d --wait`:

```text
VIBRANCE_BIND=192.168.1.20
VIBRANCE_PUBLIC_ORIGIN=http://192.168.1.20:8090
```

Open `http://192.168.1.20:8090` on any device of your network. Vibrance then no longer answers at `http://127.0.0.1:8090`, not even on the machine itself: use the LAN address there too. MusicLib has the same two settings, `MUSICLIB_BIND` and `PUBLIC_ORIGIN` ([MusicLib's guide](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#on-your-home-network-lan)).

**Use this only on a network you trust.** Over plain HTTP anyone who can watch the network can read the passwords and the session cookies.

### HTTPS and two names

Vibrance and MusicLib each get their own name under your domain, such as `https://vibrance.example.com` and `https://musiclib.example.com`, and [Caddy](https://caddyserver.com) puts HTTPS in front of both. The stack has the files for it:

- `compose.caddy.yaml` adds Caddy, removes the host ports of Vibrance and MusicLib (only Caddy reaches them, over the Compose network), and builds Caddy with the DNS plugin of your provider (Cloudflare in the file) at the first start;
- `Caddyfile.example` routes each name to its product, with one wildcard certificate for `*.example.com` obtained with the DNS-01 challenge: Caddy proves it owns the domain through the provider's API, so **no port has to be open to the Internet**, and the names work at home and through a VPN alike.

Vibrance serves its whole API, and the documentation page, at the root of its own name: Caddy sends it everything, with the `Host` header unchanged.

1. **The names.** Both names must resolve to the server's **local** address, `192.168.1.20`, on every device: at home and through your VPN. Either add them to your router's or home DNS server's records, or add public DNS records (`A`) that point them to that private address. Some routers refuse public names that point to private addresses, as a protection against *DNS rebinding*: then allow the domain in the router, or use its local DNS.
2. **The token.** Create an API token at your DNS provider, allowed to edit the DNS records of the domain (for Cloudflare: *Zone*, *DNS*, *Edit*, for that zone only).
3. **The files.** Download the two files of the release, and make your `Caddyfile` from the example, with your domain in place of `example.com`:

   ```sh
   cd ~/musiclib
   curl -fsSLO https://github.com/tommasonovelli/vibrance/releases/latest/download/compose.caddy.yaml
   curl -fsSLO https://github.com/tommasonovelli/vibrance/releases/latest/download/Caddyfile.example
   sed 's/example\.com/your-domain.net/g' Caddyfile.example > Caddyfile
   ```

4. **The settings.** In `.env`, set the Compose files, the token and the two addresses (`https://`, the name, no port, no trailing slash), then start:

   ```sh
   printf 'COMPOSE_FILE=compose.yaml:compose.caddy.yaml\nCF_API_TOKEN=%s\n' 'the token' >> .env
   sed -i 's|^VIBRANCE_PUBLIC_ORIGIN=.*|VIBRANCE_PUBLIC_ORIGIN=https://vibrance.your-domain.net|' .env
   sed -i 's|^PUBLIC_ORIGIN=.*|PUBLIC_ORIGIN=https://musiclib.your-domain.net|' .env
   docker compose up -d --wait
   ```

   The first start builds Caddy's image (a minute or two) and obtains the certificate.

5. **Check:** `docker compose ps` shows `musiclib-caddy` running, and `curl -f https://vibrance.your-domain.net/health/ready` prints `{"status":"ready"}`.

What to know:

- **Each product answers only at its address.** From then on `http://127.0.0.1:8090` answers nothing (the port is gone) and the old addresses answer `421 host_not_allowed`; only the health checks answer at any address. With an `https://` address the session cookie is marked `Secure`.
- `!reset` in `compose.caddy.yaml` needs Docker Compose 2.24 or later. Check with `docker compose config`: the services `app` and `vibrance` must show no `ports`.
- **Do not add `encode`** (compression) to the Caddyfile for Vibrance: it changes the `ETag` of the audio and the covers, which apps use to resume and cache them, and audio does not compress anyway.
- Vibrance ignores `X-Forwarded-*` headers: its log shows Caddy's address, not the device's.
- Another DNS provider: replace the plugin on the `xcaddy build` line of `compose.caddy.yaml` (see Caddy's list of `dns.providers` modules), the `dns` line of the `Caddyfile` and the token's variable, then `docker compose up -d --build --wait`.
- To keep MusicLib reachable from your home network only, refuse the other addresses in its block of the `Caddyfile`:

  ```text
  	@musiclib host musiclib.your-domain.net
  	handle @musiclib {
  		@outside not remote_ip private_ranges
  		abort @outside
  		reverse_proxy app:8080
  	}
  ```

  Check it from a device outside your network, such as a phone without Wi-Fi: the name must answer nothing.
- Caddy's certificates and keys are in the volume `musiclib_caddy_data`, which the backups of Vibrance and MusicLib do not include: keep it.
- Caddy is yours to update: change the versions and digests of the two images and the plugin's version in `compose.caddy.yaml`, then `docker compose up -d --build --wait`.

#### A name for your home network only

Without a domain of your own, Caddy can sign the certificates with its own local certificate authority, for names such as `vibrance.home.arpa` and `musiclib.home.arpa` (`home.arpa` is reserved for home networks). In the `Caddyfile`, use the names under `home.arpa` and replace the `tls { … }` block with one line, `tls internal`; leave `CF_API_TOKEN` empty. Point the names to the server's address in your router or home DNS, then trust Caddy's root certificate on each device: copy it with `docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt .` and install it as [MusicLib's guide](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#a-name-for-your-home-network-only) explains, with its warnings: a device that trusts that root accepts every certificate Caddy signs.

#### Other reverse proxies

Any reverse proxy works if it:

- passes the client's `Host` header unchanged, port included (nginx: `proxy_set_header Host $http_host;`);
- leaves the other headers alone, in particular `Origin`, `Cookie`, `Authorization`, `Range`, `If-Range`, `If-Match`, `If-None-Match`, `ETag` and `X-Vibrance-Request`;
- serves Vibrance at the root of its own name, never under a path;
- does not compress or buffer the responses (nginx: `proxy_buffering off;`), and lets a long audio stream run.

## Backups, restore and moving

Vibrance and MusicLib keep different things, and back up separately:

| | What it keeps | Its backup |
|---|---|---|
| MusicLib | the catalog, the originals of your music, the library it writes | a folder in MusicLib's backup volume (`MUSICLIB_BACKUP`) |
| Vibrance | the accounts, favorites, playlists, and the index of the library with the ids of the tracks | a folder in Vibrance's backup volume (`VIBRANCE_BACKUP`) |

Vibrance's database is small but precious: the ids of the tracks, which playlists and favorites point to, cannot be rebuilt from the library. The thumbnails are a cache and are not saved.

### Making a backup

Vibrance's backup runs while Vibrance runs:

```sh
docker compose exec vibrance vibrance backup --to "/backup/$(date +%F-%H%M)"
```

It prints `Backup completed: /backup/2026-10-05-2130` and exits with 0. MusicLib's backup needs MusicLib stopped; Vibrance keeps running meanwhile:

```sh
docker compose stop app
docker compose run --rm --no-deps app backup --to "/backup/$(date +%F-%H%M)"
docker compose start app
```

Both at once, with the same name:

```sh
N=$(date +%F-%H%M)
docker compose exec vibrance vibrance backup --to "/backup/$N" &&
docker compose stop app && docker compose run --rm --no-deps app backup --to "/backup/$N"
docker compose start app
```

What to know about Vibrance's backup:

- It is a new folder under `/backup` with `vibrance.db`, a consistent copy made with SQLite's `VACUUM INTO` (mode 0600: it holds the password hashes), and `manifest.json` (versions, date, the SHA-256 of the copy, and counts). The copy is checked with `PRAGMA integrity_check` and read again for its SHA-256.
- It **never overwrites**: a name that exists is refused (`backup_exists`). The destination is always a plain absolute path of a new folder under `/backup`, such as `/backup/2026-10-05-2130`; anything else is refused before anything is written (`backup_outside_backup`).
- It is written under a temporary name, `.vibrance-backup-….tmp`, and renamed only when complete. A backup that fails or is killed leaves that temporary folder: delete it yourself. Never use it as a backup.
- **The default backup volume is on the same disk as everything else**: set `VIBRANCE_BACKUP` (and `MUSICLIB_BACKUP`) to a folder on another disk, keep several backups, and copy them to an external device with `sudo cp -a`, which keeps their owner, uid 1000.
- The two backups need not be made at the same moment. Vibrance finds the library as it is whenever it starts: an album missing from the library is shown as unavailable, never deleted, and comes back with the same ids when MusicLib publishes it again.

To check Vibrance's database at any time, also while it runs (it changes nothing): `docker compose exec vibrance vibrance doctor`. It prints one line per finding and `Doctor complete: no damage found.` (exit 0), or the number of problems (exit 1): see [Errors of the maintenance commands](#errors-of-the-maintenance-commands). What it finds wrong in the search is repaired without a backup: see [Repairing the search](#repairing-the-search).

### Restore into a new installation

A restore puts the backups into a **new, empty installation**. Vibrance's restore writes its database only into a state volume without one; it never overwrites.

1. On the new machine, or in a new folder, paste the [install block](#first-start) **without its last line**.
2. In `.env`, set `VIBRANCE_BACKUP` and `MUSICLIB_BACKUP` to the host folders that hold the backups, such as `/mnt/backup/vibrance` and `/mnt/backup/musiclib`. A copy of a backup must belong to uid 1000: `sudo chown -R 1000:1000 /mnt/backup/vibrance`.
3. Run:

   ```sh
   docker compose up -d --wait postgres
   docker compose run --rm --no-deps app restore --from /backup/2026-10-05-2130
   docker compose run --rm --no-deps vibrance restore --from /backup/2026-10-05-2130
   docker compose up -d --wait
   ```

   - The first two lines restore MusicLib, as [MusicLib's guide, "Restore"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#restore-use-new-empty-destinations) explains.
   - The third restores Vibrance's database. It verifies the manifest and the SHA-256 of the copy, refuses a backup made by a newer Vibrance, and prints `Restore completed: /backup/2026-10-05-2130. Start the server.` A backup of an older version is upgraded when the server starts.
   - The last line starts everything. Vibrance comes back with the same accounts, favorites, playlists and track ids; the admin password of `.env` is ignored, because accounts exist.

Vibrance's restore must run **before** Vibrance's first start in the new installation: the first start creates an empty database, and the restore then refuses (`restore_database_exists`).

### Restoring Vibrance only

To put back Vibrance's database on an installation that runs (after a damage that `doctor` found and that is not [of the search alone](#repairing-the-search), for example), set the current database aside in the same volume, restore, and start:

```sh
docker compose stop vibrance
docker compose run --rm --no-deps --entrypoint sh vibrance -c 'cd /var/lib/vibrance && d=set-aside-$(date +%s) && mkdir "$d" && for f in vibrance.db vibrance.db-wal vibrance.db-shm vibrance.db-journal; do [ ! -e "$f" ] || mv "$f" "$d/"; done'
docker compose run --rm --no-deps vibrance restore --from /backup/2026-10-05-2130
docker compose up -d --wait
```

The second line moves the database and its companion files into a new folder `set-aside-…` of the state volume, where they stay until you delete them. Accounts, favorites and playlists changed after the backup are lost; the index catches up with MusicLib's current library at the first scan, with the ids of the backup.

### Moving to another machine

1. On the old machine, [make both backups](#making-a-backup) and copy them, with `sudo cp -a`, to a disk the new machine can mount.
2. On the new machine, check [What you need](#what-you-need), copy the backups into folders such as `/mnt/backup/vibrance` and `/mnt/backup/musiclib`, give them to uid 1000 (`sudo chown -R 1000:1000 /mnt/backup/vibrance /mnt/backup/musiclib`), and [restore](#restore-into-a-new-installation).
3. Copy the settings you changed in the old `.env` (addresses, `MUSICLIB_PASSWORD`, Caddy's), not the database password: the new database has the one the install block wrote.
4. Keep the old installation until the new one works and has backups of its own.

## Upgrading

The stack is upgraded **only with Vibrance's releases**: each one names the MusicLib version it was tested with, in its `compose.yaml` and in [compat.md](compat.md). **Never upgrade MusicLib with MusicLib's own `compose.yaml`**: it would remove Vibrance from the stack (its container would be left behind as an orphan, and MusicLib's version would no longer be one Vibrance was tested with).

**Back up both before every upgrade.** A new version can upgrade a database at its first start, and there is no way back except restoring the backup with the older version.

```sh
cd ~/musiclib
N=before-update-$(date +%F-%H%M)
docker compose exec vibrance vibrance backup --to "/backup/$N" &&
docker compose stop app &&
docker compose run --rm --no-deps app backup --to "/backup/$N" &&
curl -fsSLO https://github.com/tommasonovelli/vibrance/releases/latest/download/compose.yaml &&
curl -fsSLO https://github.com/tommasonovelli/vibrance/releases/latest/download/env.example &&
docker compose pull && docker compose up -d --wait
docker compose run --rm --no-deps vibrance version
```

- The commands are chained with `&&`: if a backup fails, nothing is updated, and MusicLib stays stopped until you fix the cause (`docker compose start app` restarts the old version).
- `curl` replaces `compose.yaml` with the latest release's. Your settings are in `.env`, which stays. `env.example` comes for reference: compare it with `.env` (`diff .env env.example`) for new settings.
- If the new release names a newer MusicLib, read MusicLib's upgrade notes for the versions in between: the upgrade of MusicLib happens in the same `docker compose up`.
- With Caddy, download `compose.caddy.yaml` too when the release notes say it changed, then `docker compose up -d --build --wait`.
- The last line prints Vibrance's running version.

At its first start the new version applies its database migrations before it is ready. When the new image carries another `ffmpeg`, Vibrance computes the audio fingerprints again in the background, on the same rows; the ids do not change.

**Downgrading is not possible**: an older Vibrance refuses a database that a newer one has upgraded (`store_schema_too_new`). To go back, restore the backups made before the upgrade, with the older release's `compose.yaml`.

## Troubleshooting

### Reading the log

```sh
docker compose logs --tail=100 vibrance
```

Vibrance logs one JSON object per line. A problem is a line with `"level":"ERROR"` and a stable `code`, such as `"code":"admin_password_missing"`, and a message that says what is wrong. Passwords, tokens and cookies never appear in the log.

When Vibrance refuses to start, the process exits and Docker starts it again (`restart: unless-stopped`), so the same line repeats until you fix the cause; `docker compose ps` shows it `restarting` or `unhealthy`.

### Common problems

| Symptom | Cause | What to do |
|---|---|---|
| The answer is `421 host_not_allowed` | the address is not `VIBRANCE_PUBLIC_ORIGIN` (for example `localhost` instead of `127.0.0.1`) | use exactly the address of `VIBRANCE_PUBLIC_ORIGIN`, or [change it](#access-from-other-devices) |
| `403 request_header_required` | a request other than GET without the header `X-Vibrance-Request: 1` | add the header; every client of the API sends it, and the page at `/api/docs` fills it in by itself |
| Nothing answers at all | Vibrance listens on another address or port, or is stopped | `docker compose ps`; check `VIBRANCE_BIND`, `VIBRANCE_PORT` and `VIBRANCE_PUBLIC_ORIGIN` |
| The admin password is refused | it was changed through the API, or `.env` was changed after the first start | `grep VIBRANCE_ADMIN_PASSWORD .env` is only the first password; [reset it](#the-passwords-and-the-accounts) |
| Vibrance lists no album | MusicLib has not published any yet, the scan has not run, or the library cannot be read | wait for the next scan, or ask for one; `GET /api/v1/admin/library` shows the state and the problems |
| The state of the library is `unavailable` | `/musiclib/.musiclib-store` is missing (Vibrance does not see MusicLib's data volume) or `library/` cannot be listed | check `MUSICLIB_DATA`: Vibrance must mount the same volume or folder as MusicLib |
| The state of the library is `maintenance` | MusicLib is rebuilding or restoring its library | nothing: the index stays as it is until MusicLib is done |
| An album is missing, with a problem such as `probe_failed` | Vibrance could not read it | render it again in MusicLib; the problems of the last scan are in `GET /api/v1/admin/library` |
| A track answers `503 library_changing` | MusicLib has just rewritten its album | try again after the seconds of `Retry-After` |
| `docker compose stop` takes 10 seconds | a long request, such as an audio stream, was open: Vibrance waits up to 10 seconds, then closes it and exits | nothing: the log ends with `stopped` |
| Vibrance's log ends without `stopped`, and `docker compose ps -a` shows it `Exited (137)` | the stop took more than the 15 seconds of `stop_grace_period` and Docker killed it: a request was open and, after it, the database could not be closed at once (a `backup` or a `doctor` was reading it), or `compose.yaml` is not the released one | nothing: the next start recovers |

### What Vibrance does when it starts

In this order; a refusal stops at its step, with one `ERROR` line and a `code`:

1. It reads its settings and refuses to run as root.
2. It listens: `/health/live` answers 200 from here on; `/health/ready` answers `503 not_ready` until the end of the start.
3. It checks that `/var/lib/vibrance` is writable, opens the database and applies its migrations.
4. It checks `ffmpeg` and `ffprobe` (`8.1.3-musiclib1`, the tools of MusicLib).
5. On an empty database, it creates the first admin.
6. It prepares the index (the sort keys) and deletes expired sessions.
7. It starts the scanner and becomes ready.

It is ready whatever the state of MusicLib: with MusicLib stopped, or with an empty data volume, everything but the audio, the covers and the lyrics works.

### When Vibrance refuses to start

| `code` | What it means | What to do |
|---|---|---|
| `config_invalid` (exit 2) | a setting is invalid (`VIBRANCE_PUBLIC_ORIGIN`, `VIBRANCE_SCAN_INTERVAL`, `VIBRANCE_WORKERS`, `VIBRANCE_HTTP_ADDR`); the message lists all of them | fix `.env`, then `docker compose up -d --wait`. `VIBRANCE_PUBLIC_ORIGIN` must be exactly `scheme://host[:port]`, lowercase, no trailing slash, no default port. Check the [line endings](#configuration) |
| `run_as_root` (exit 2) | Vibrance would run as uid 0 | set `VIBRANCE_UID` and `VIBRANCE_GID` to a normal user, or remove them |
| `admin_password_missing` (exit 2) | the database has no account and `VIBRANCE_ADMIN_PASSWORD` is empty | set it in `.env` (`openssl rand -base64 24`), then `docker compose up -d --wait` |
| `admin_password_invalid` (exit 2) | it is shorter than 12 bytes, longer than 1024, or has a control character such as a line break | set another one; check the [line endings](#configuration) |
| `admin_username_invalid` (exit 2) | `VIBRANCE_ADMIN_USERNAME` is not 3 to 32 of `a-z`, `0-9`, `.`, `_`, `-` | fix it, or remove it for `admin` |
| `http_listen` | Vibrance cannot listen on `VIBRANCE_HTTP_ADDR` inside the container | keep `compose.yaml` as released |
| `state_unwritable` | `/var/lib/vibrance` is not writable by `VIBRANCE_UID` | see [Another uid](#configuration) |
| `store_open`, `store_migrate` | the database cannot be opened, or a migration failed | read the message; run `doctor`; restore a backup if the file is damaged |
| `store_schema_too_new` | the database was written by a newer Vibrance | use that version; never downgrade |
| `media_tool_unavailable`, `media_tool_version` | `ffmpeg` or `ffprobe` is missing or not the pinned version | pull the image again: `docker compose pull vibrance` |
| `musiclib_folder` | `/musiclib` does not exist in the container | keep `compose.yaml` as released |
| `library_index` | the index could not be prepared | read the message; run `doctor` |

At the stop, `store_close` means that the database could not be closed cleanly (another process was reading it); nothing is lost, and the next start recovers.

### Repairing the search

Searches go through an index of their own inside Vibrance's database, a copy of the names of the artists, albums and tracks. If it ever disagrees with the library, searches answer `500 internal` or miss what is there, and `vibrance doctor` reports findings whose code begins with `doctor_search_` (and `doctor_integrity` lines that name a `search_` table). `vibrance rebuild-search` makes that index again from the library index, and changes nothing else: accounts, favorites, playlists and track ids are not touched.

**Stop Vibrance first.** The rebuild is one transaction: while it runs, everything else that writes to the database waits, and fails after 5 seconds. On a large library or a slow disk the rebuild can last longer than that.

```sh
docker compose stop vibrance
docker compose run --rm --no-deps vibrance rebuild-search
docker compose up -d --wait
docker compose exec vibrance vibrance doctor
```

- The second line prints `Search index rebuilt. Run vibrance doctor to check it.` It took 2.4 seconds for a library of 200,000 tracks on the machine it was measured on.
- If you run it while Vibrance is running and writing (a scan of many new albums), it may not get its turn: it waits 5 seconds for it, then fails with `rebuild_search_failed`, and can take up to 15 seconds to exit, because it waits again to close the database.
- The last line must print `Doctor complete: no damage found.` If it still reports a `doctor_search_` or `doctor_integrity` finding, the file itself is damaged: [restore a backup](#restoring-vibrance-only).
- A rebuild that fails or is interrupted has changed nothing: run it again. It refuses to run without a database, or on a database of another version of Vibrance (see [the table below](#errors-of-the-maintenance-commands)).
- Vibrance never rebuilds the search by itself, and you never need to after an update or a restore: the scanner keeps the index in step with the library. Run the command only when `doctor` says so.

### Errors of the maintenance commands

`backup`, `restore`, `doctor` and `rebuild-search` exit with 0 when done, 2 when they refused before writing anything (bad arguments, root, the codes marked *refusal*), and 1 when they failed or found damage. A failure is logged with its `code` and an `advice`.

| `code` | Command | What it means | What to do |
|---|---|---|---|
| `backup_outside_backup` (refusal) | backup | the destination is not a plain absolute path of a new folder under `/backup` | use `/backup/NAME` |
| `backup_exists` (refusal) | backup | a file or folder with that name exists | choose another name; backups are never overwritten |
| `backup_destination` (refusal) | backup | the parent folder does not exist or cannot be written | check `VIBRANCE_BACKUP` and its owner (uid 1000) |
| `database_missing` (refusal) | backup, doctor, rebuild-search | there is no `vibrance.db` in the state volume | start Vibrance once, or restore |
| `store_schema_old` (refusal) | backup, doctor, rebuild-search | the database was written by an older Vibrance | start the server once, which upgrades it |
| `store_schema_too_new`, `store_open` (refusal) | backup, doctor, rebuild-search | the database is newer than this version, or is not a database | use the newer version; restore a backup |
| `backup_failed`, `backup_verify` | backup | the copy could not be written, or is damaged | delete the temporary folder `.vibrance-backup-….tmp`, run `doctor`, retry |
| `restore_outside_backup` (refusal) | restore | the backup is not a folder under `/backup` | use `/backup/NAME` |
| `restore_database_exists` (refusal) | restore | the state volume has a database, or what is left of one | restore into a [new installation](#restore-into-a-new-installation), or [set it aside](#restoring-vibrance-only) |
| `restore_manifest_invalid`, `restore_hash`, `restore_schema_too_new` (refusal) | restore | the backup does not pass its checks, or a newer Vibrance made it | use another backup, or that version |
| `restore_destination` (refusal), `restore_failed` | restore | the state volume cannot be written, or writing failed; nothing was restored | check the volume and its owner, retry |
| `doctor_failed` | doctor | the inspection could not be completed | read the message, retry |
| `doctor_integrity`, `doctor_foreign_key` | doctor | the database file is damaged | if every `doctor_integrity` line names a `search_` table, [repair the search](#repairing-the-search) and run `doctor` again; otherwise keep the file, and [restore](#restoring-vibrance-only) the latest backup that `doctor` finds sound |
| `doctor_search_index`, `doctor_search_extra`, `doctor_search_missing`, `doctor_search_stale` | doctor | the search index is damaged, or does not agree with the library index: searches that meet such a row fail or miss it | [repair the search](#repairing-the-search) with `rebuild-search` |
| `doctor_album_counters` | doctor | an album shows a wrong number of tracks or duration | nothing urgent: it is right again when the album is indexed again |
| `doctor_no_admin` | doctor | no admin is enabled | `vibrance user create --role admin`, [as above](#the-passwords-and-the-accounts) |
| `rebuild_search_failed` | rebuild-search | the search index could not be rebuilt, for example because Vibrance was running and writing; nothing was changed | stop Vibrance and [retry](#repairing-the-search) |

MusicLib's codes are in [MusicLib's guide, "Troubleshooting"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#troubleshooting).
