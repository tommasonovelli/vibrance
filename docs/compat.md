# MusicLib compatibility

Vibrance reads only the `library/` folder of Vibrance MusicLib, and the two markers `.maintenance` and `.musiclib-store` next to it. The format of that folder and of its receipts (`.musiclib.json`) is not a public interface of MusicLib: Vibrance declares here the MusicLib versions each of its versions was tested with, and the contract suite `scripts/contract.sh` is the judge.

| Vibrance | MusicLib | Status |
|---|---|---|
| 0.1.0 | 1.2.0 | Tested: `scripts/contract.sh` passes every scenario, A1–A20, with `ghcr.io/tommasonovelli/musiclib:1.2.0@sha256:52204bdf0ca23eeae71453f2ae8a1ee0a8d9b52ef4ce58df42ace369ab8012dd`. |

A Vibrance release fixes the MusicLib version of its stack: the `compose.yaml` it ships is MusicLib's own file of that version, plus Vibrance's service (`docs/operations.md`). Upgrade the two together, only with a Vibrance release, never with MusicLib's own `compose.yaml`.

## What Vibrance relies on

These are the facts the contract suite checks against the real MusicLib (the scenarios are the table of `DESIGN.md` §12.3). If a new MusicLib changes one of them, the suite fails.

- `library/<artist>/<album>/` holds one album: its `.musiclib.json` receipt (`schema_version` 1, `album_id`, `album_revision`, `build_id`, the files with size and SHA-256), the tracks (FLAC, MP3, M4A), `Disc N/` folders for several discs, `cover.jpg` or `cover.png`, the `.lrc` lyrics next to their track.
- Every track carries the managed tags of the album and of the track. Names and numbers come from the tags, never from the paths.
- Writing the tags again keeps the compressed audio packets: the fingerprint of a track survives a new title, new numbers, a new cover, a renamed album or artist, a move to another artist, a render of every album, the offline rebuild, and a move of the track to another album (A1–A11, A17–A19).
- An album keeps its `album_id` through every change, the trash and its restore (A4–A10). After "Empty trash" an album imported again has a new `album_id` (A20).
- An album folder is replaced at once; a file already open stays readable (A13). During a rename two folders with the same `album_id` may exist for a moment (A4).
- `.maintenance` exists while an offline `rebuild` runs, for a few tens of milliseconds; `library/` is then empty until MusicLib's app starts again and renders every album (A11). `.musiclib-store` is always there in a working installation.

## Running the suite

```sh
scripts/contract.sh
```

It needs Docker with Compose v2 and network access (MusicLib's images), and takes a few minutes (about one once the images are built). It runs the stack in the Compose project `vibrance-contract`, with new volumes, random passwords and no port published on the host, and deletes that project when it ends; it never touches the project `musiclib` of an installation. `CONTRACT_SEED=<n> scripts/contract.sh` repeats the random choices of a run (its first lines print the seed).

## Moving to a new MusicLib version

1. Change `ARG MUSICLIB_IMAGE` in the `Dockerfile` (version and digest), the one place that names MusicLib's version, and take MusicLib's `compose.yaml` and `.env.example` of that version into the stack files (`scripts/check-compose-sync.sh` says what differs).
2. Run `scripts/spike.sh` (the hypotheses on `library/`), then `scripts/contract.sh` twice.
3. Add a row to the table above. If MusicLib changed something Vibrance relies on, change Vibrance and the list above with it, and add a scenario to `DESIGN.md` §12.3 and to the suite.
