# Vibrance

Vibrance lets you and the people at home listen to the music collection that [Vibrance MusicLib](https://github.com/tommasonovelli/vibrance-musiclib) keeps tidy.

1. **MusicLib** imports your albums, lets you fix their metadata and writes a clean library folder.
2. **Vibrance reads that folder, and only reads it**: it keeps an index of your artists, albums and tracks up to date by itself, and never changes, moves or deletes a file.
3. **Everyone has an account**, with their own favorites and private playlists, and listens in the browser, through Vibrance's web interface, or in an app, through its HTTP API: streaming of the original files, covers, synchronised lyrics and search.

A track keeps its identity when you rename it, renumber it, retag it or move it to another album in MusicLib: playlists and favorites follow the audio. Vibrance is self-hosted, runs in Docker on a Linux machine next to MusicLib, and keeps working while MusicLib is stopped.

**Vibrance has a web player.** Its address opens a listening interface in your browser: sign in, browse the library, play, keep favorites and playlists. It also serves a documented API for apps and scripts, with a page at `/api/docs` where you can try every request. (Version 0.1 was the server alone: its address opened that documentation.)

## Contents

- [Install](#install): six steps, from checking your machine to your first album
- [Everyday use](#everyday-use): start, stop, logs, accounts, backups, updates
- [What it does and doesn't do](#what-it-does-and-doesnt-do)
- [The guide](#the-guide)
- [Building from source](#building-from-source)
- [Security](#security)
- [License](#license)

## Install

Each step says what it does and what a good result looks like. The [operations guide](docs/operations.md) explains every step in more depth.

Vibrance is installed **together with MusicLib**, as one Docker Compose stack: MusicLib's own `compose.yaml`, unchanged, plus the `vibrance` service. Each release of Vibrance names the MusicLib version it was tested with (MusicLib 1.2.0 for Vibrance 0.1.0).

**Already running MusicLib?** Do not use the block of step 2: follow [Adopting an existing MusicLib installation](docs/operations.md#adopting-an-existing-musiclib-installation), which keeps your containers, your volumes and your `.env`.

### 1. Check your machine

The stack needs what MusicLib needs: **Ubuntu 24.04 or later** on an **amd64** (x86-64) machine, **Docker Engine** with the **Compose v2** plugin (2.24 or later), usable without `sudo`, **local ext4 storage**, and `curl` and `openssl` (`sudo apt install curl openssl`). Docker Desktop, NAS and network filesystems (NFS, SMB) and ARM machines are **not supported**.

Run these three checks:

```sh
uname -m                                   # must print x86_64
docker compose version                     # prints "Docker Compose version v2...." (2.24 or later)
findmnt -no FSTYPE -T /var/lib/docker      # must print ext4
```

- If `docker` is missing, install it with `sudo apt install docker.io docker-compose-v2`. If it says "permission denied", run `sudo usermod -aG docker "$USER"`, then sign out and in again.
- To keep MusicLib's data in a folder of your choice instead of Docker's storage, **decide before the first start**: follow [MusicLib's guide, "The data in a host folder"](https://github.com/tommasonovelli/vibrance-musiclib/blob/v1.2.0/docs/operations.md#the-data-in-a-host-folder), with the `compose.yaml` and `env.example` of Vibrance's release in place of MusicLib's. Vibrance mounts the same folder, read-only.

More: [What you need](docs/operations.md#what-you-need).

### 2. Install and start

Paste this block into a terminal. It creates `~/musiclib`, downloads the two files of the latest release, writes three random passwords into `.env` (MusicLib's database, MusicLib's sign-in, Vibrance's first admin), and starts MusicLib and Vibrance:

```sh
mkdir -p ~/musiclib/import && cd ~/musiclib
[ -e compose.yaml ] || curl -fsSLO https://github.com/tommasonovelli/vibrance/releases/latest/download/compose.yaml
[ -e .env ] || { curl -fsSL -o .env https://github.com/tommasonovelli/vibrance/releases/latest/download/env.example && chmod 600 .env && sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 32)/" .env && sed -i "s|^MUSICLIB_PASSWORD=.*|MUSICLIB_PASSWORD=$(openssl rand -base64 24)|" .env && sed -i "s|^VIBRANCE_ADMIN_PASSWORD=.*|VIBRANCE_ADMIN_PASSWORD=$(openssl rand -base64 24)|" .env; }
docker compose up -d --wait
```

The first time, Docker downloads the images; the last command returns when everything is ready. Check it:

```sh
docker compose ps                                    # the three containers show "healthy"
curl -f http://127.0.0.1:8090/health/ready           # Vibrance: prints {"status":"ready"}
curl -f http://127.0.0.1:8080/health/ready           # MusicLib: prints {"status":"ready"}
```

- **Run every command of this README from `~/musiclib`** (`cd ~/musiclib`).
- The block is safe to paste twice: it never replaces an existing `compose.yaml` or `.env`. What each line does: [First start](docs/operations.md#first-start).
- `.env` holds the three passwords: keep it private (the block makes it readable by you only).
- If a container is reported unhealthy, `docker compose logs --tail=20 vibrance` (or `app`, for MusicLib) shows a line with a `code`: look it up in [Troubleshooting](docs/operations.md#troubleshooting).

### 3. Import your first album in MusicLib

MusicLib is at **<http://127.0.0.1:8080>** (exactly this address: `localhost` is refused). Sign in with its password, `grep MUSICLIB_PASSWORD .env`, copy an album folder into `~/musiclib/import`, and choose **Import everything in …** on the **Import** page. [MusicLib's README](https://github.com/tommasonovelli/vibrance-musiclib#install) explains its side.

### 4. Sign in to Vibrance

Open **<http://127.0.0.1:8090>** in a browser on the same machine (exactly this address). It opens the web interface, which asks you to sign in. The first account is the admin: its name is `admin`, and its password is in `.env`:

```sh
grep VIBRANCE_ADMIN_PASSWORD .env                    # prints VIBRANCE_ADMIN_PASSWORD=...
```

The password is everything after `=`: sign in with it, and the interface shows your library. To try the API instead, open <http://127.0.0.1:8090/api/docs>, the documentation of the API: there, open **Sign in with a cookie** (`POST /auth/login`), choose **Test Request**, fill in `username` and `password`, and send. (The **Headers** table already has `X-Vibrance-Request: 1`, which Vibrance asks for on every request that changes something: leave it there.) A good result is `200` with your account. From then on the requests of the page are signed in.

The same from a terminal, with [`curl`](docs/api.md):

```sh
curl -sS -c cookies.txt -H 'X-Vibrance-Request: 1' -H 'Content-Type: application/json' \
  --data-binary @- http://127.0.0.1:8090/api/v1/auth/login <<EOF
{"username": "admin", "password": "$(grep '^VIBRANCE_ADMIN_PASSWORD=' .env | cut -d= -f2-)"}
EOF
```

On a machine without a desktop, open both from your computer through an SSH tunnel: run `ssh -L 8090:127.0.0.1:8090 -L 8080:127.0.0.1:8080 you@server` on your computer, and while it stays connected open the two addresses there.

### 5. Find your album

Vibrance looks at MusicLib's library when it starts and then every 5 minutes: an album appears a few minutes after MusicLib has written it. As the admin, you can ask for a scan at once and read how it went:

```sh
curl -sS -b cookies.txt -H 'X-Vibrance-Request: 1' -X POST http://127.0.0.1:8090/api/v1/admin/library/scan
curl -sS -b cookies.txt http://127.0.0.1:8090/api/v1/admin/library      # "state": "idle", the albums and tracks counted
curl -sS -b cookies.txt 'http://127.0.0.1:8090/api/v1/albums?limit=5'   # your album
```

A good result is your album in the last answer. The first indexing of a large library reads every track once and takes a while; the second answer shows how far it has come. More requests, from streaming a track to making a playlist: [Using the API](docs/api.md).

### 6. Add the people who listen, and configure what you need (optional)

- **An account for each person.** Only an admin makes accounts; nobody can sign up alone. From the machine:

  ```sh
  read -rs -p 'Password: ' P && echo
  printf '%s' "$P" | docker compose exec -T vibrance vibrance user create --username anna --role user --password-stdin
  unset P
  ```

  A name is 3 to 32 of `a-z`, `0-9`, `.`, `_`, `-`; a password is at least 12 characters. More: [The passwords and the accounts](docs/operations.md#the-passwords-and-the-accounts).

- **Other devices.** By default only the machine itself can open Vibrance and MusicLib. For your home network or for HTTPS with a name, follow [Access from other devices](docs/operations.md#access-from-other-devices). **Over plain HTTP the passwords cross the network in clear**: use it only on a network you trust, and never expose the ports to the Internet.

- **The backup folder** (`VIBRANCE_BACKUP`). By default Vibrance's backups go to a Docker volume on the same disk as everything else. Prefer a folder on another disk, owned by uid 1000:

  ```sh
  sudo mkdir -p /mnt/backup/vibrance && sudo chown 1000:1000 /mnt/backup/vibrance
  sed -i '/^#\?VIBRANCE_BACKUP=/d' .env && echo 'VIBRANCE_BACKUP=/mnt/backup/vibrance' >> .env
  docker compose up -d --wait
  ```

- **`VIBRANCE_ADMIN_PASSWORD` counts only at the first start.** Once an account exists, changing it in `.env` changes nothing: change a password through the API (`PUT /api/v1/me/password`) or with `vibrance user reset-password`.

After changing `.env`, apply it with `docker compose up -d --wait`. Every setting, with its default: [Configuration](docs/operations.md#configuration).

## Everyday use

Run these from `~/musiclib`. More in [Everyday commands](docs/operations.md#everyday-commands).

| Task | Command |
|---|---|
| Stop everything | `docker compose stop` |
| Start it again | `docker compose up -d --wait` |
| Stop or start Vibrance only | `docker compose stop vibrance`, `docker compose start vibrance` |
| Show the status | `docker compose ps` |
| Read Vibrance's log | `docker compose logs --tail=100 vibrance` |
| List Vibrance's accounts | `docker compose exec vibrance vibrance user list` |
| Show Vibrance's version | `docker compose run --rm --no-deps vibrance version` |

The stack starts again by itself after a reboot, unless you stopped it. Vibrance works with MusicLib stopped: `docker compose stop app`, which MusicLib's backups use, does not touch it.

> **Never run `docker compose down -v`: it deletes your library, both databases and the backup volumes.** To stop the stack, use `docker compose stop`.

### Back up

Vibrance's database holds the accounts, the favorites, the playlists and the ids of the tracks they point to: it cannot be rebuilt from the library. Its backup runs while Vibrance runs:

```sh
docker compose exec vibrance vibrance backup --to "/backup/$(date +%F-%H%M)"
```

A good result is `Backup completed: /backup/2026-10-05-2130`: a new folder in the backup folder (step 6). A backup never overwrites another. MusicLib has its own backup, of other things: make both. Restoring, and moving to another machine: [Backups, restore and moving](docs/operations.md#backups-restore-and-moving).

### Update to a new version

**Update the stack only with Vibrance's releases**, never with MusicLib's own `compose.yaml`: each release of Vibrance names the MusicLib version it was tested with, and updates both.

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

The block backs up both products first, and updates nothing if a backup fails. `compose.yaml` names the versions to run, so the update downloads the latest release's file; your settings stay in `.env`. The last line prints the new version. Going back to an older version is possible only by restoring the backups: see [Upgrading](docs/operations.md#upgrading).

## What it does and doesn't do

**It does:**

- index MusicLib's library by itself, in the background, and show what it could not read and why;
- keep the id of a track through every change MusicLib makes to it, so that playlists and favorites never lose it; albums that leave the library are shown as unavailable, never deleted, and come back as they were;
- give each person an account (`admin` or `user`), with sign-in by cookie for browsers and by token for apps;
- serve artists, albums and tracks, sorted and paged; the original audio files with ranges (**FLAC**, **MP3**, **M4A** with AAC or ALAC, as MusicLib writes them); the album covers, as they are or as thumbnails; the **LRC lyrics** as structured lines, synchronised or plain with their stanzas;
- search artists, albums and tracks as you type, without regard to case, accents and the way a system writes accented letters;
- keep favorites and private playlists for each user;
- document its API with OpenAPI, serve that document and a page to try it;
- back up, restore and check its database, and rebuild its search index if it is ever damaged.

**It doesn't:** have an app of its own yet; convert audio (a browser plays only the formats it supports: ALAC only in Safari); change your music or its metadata (that is MusicLib's work); share playlists between users; scrobble, recommend or download anything; let people sign up by themselves. Search does not split Chinese or Japanese text into words and does not forgive typing mistakes. The documentation is in English.

## The guide

Everything else is in the [operations guide](docs/operations.md), in the order you need it:

- [Configuration](docs/operations.md#configuration): every setting of `.env`, with its default and an example.
- [Adopting an existing MusicLib installation](docs/operations.md#adopting-an-existing-musiclib-installation).
- [The passwords and the accounts](docs/operations.md#the-passwords-and-the-accounts).
- [Access from other devices](docs/operations.md#access-from-other-devices): an SSH tunnel, your home network, or [HTTPS and two names](docs/operations.md#https-and-two-names) with Caddy.
- [Backups, restore and moving](docs/operations.md#backups-restore-and-moving), for both products.
- [Upgrading](docs/operations.md#upgrading).
- [Troubleshooting](docs/operations.md#troubleshooting): what each error code means and what to do.

And next to it:

- [Using the API](docs/api.md): the main requests with `curl`; the full reference is the page Vibrance serves, from [api/openapi.yaml](api/openapi.yaml).
- [MusicLib compatibility](docs/compat.md): which MusicLib each Vibrance was tested with.
- [CHANGELOG.md](CHANGELOG.md): what each version changed.

## Building from source

The image holds one Go program and the `ffmpeg` and `ffprobe` of MusicLib, all pinned to exact versions; its database is one SQLite file. Everything is built and tested in Docker: the host needs no Go. To build the image from a clone of the repository:

```sh
docker build --target runtime -t vibrance:local .
```

How the server works inside, the tests, the pinned dependencies and the releases are in the [developer guide](docs/development.md). To contribute, read [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

Vibrance is made for a home network or a private name behind HTTPS, not for the open Internet. To report a vulnerability, read [SECURITY.md](SECURITY.md): please do not open a public issue for it.

## License

Vibrance's code and documentation are released under the [MIT License](LICENSE), copyright 2026 tommasonovelli.

Third-party software keeps its own licenses. [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) lists everything the Docker image contains, with versions, licenses and where to get the sources; it is also in the image, under `/usr/share/doc/vibrance/`, with the license texts. Each GitHub release attaches it together with the source tarball of FFmpeg.
