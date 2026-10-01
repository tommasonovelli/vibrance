# Vibrance — avanzamento

Design: DESIGN.md (versione 0.1). Orchestratore: aggiorna questo file a ogni passo.

| Passo | Titolo | Stato | Round | Commit | Note |
|---|---|---|---|---|---|
| S0 | Bootstrap del repository e toolchain | done | 2 | b68c8e1 | 2026-09-30 |
| S1 | Spike: le ipotesi del contratto con MusicLib reale | done | 1 | (vedi git log: «Step S1») | 2026-10-01 · H1–H9 confermate, nessun BLOCCO |
| S2 | Configurazione, avvio, salute e arresto | todo | | | |
| S3 | Store SQLite, migrazioni, transazioni | todo | | | |
| S4 | Ricevuta, accesso confinato e classificazione | todo | | | |
| S5 | Adapter media: processi, tag, durata, impronta | todo | | | |
| S6 | Identità e riconciliazione (puro) | todo | | | |
| S7 | Indicizzare un album | todo | | | |
| S8 | Scanner: ciclo, trigger, stato | todo | | | |
| S9 | Cover e miniature | todo | | | |
| S10 | Testi LRC | todo | | | |
| S11 | OpenAPI completa e pipeline di generazione | todo | | | |
| S12 | Confine HTTP e modello degli errori | todo | | | |
| S13 | Autenticazione: nucleo | todo | | | |
| S14 | Endpoint di autenticazione, account e amministrazione utenti | todo | | | |
| S15 | Catalogo: artisti, album, tracce | todo | | | |
| S16 | Endpoint media: audio, cover, testi | todo | | | |
| S17 | Ricerca | todo | | | |
| S18 | Preferiti | todo | | | |
| S19 | Playlist | todo | | | |
| S20 | Stato della libreria, documentazione servita, completamento dell'API | todo | | | |
| S21 | Backup, ripristino, doctor | todo | | | |
| S22 | Immagine e stack Compose | todo | | | |
| S23 | Contratto end-to-end con MusicLib reale | todo | | | |
| S24 | Prestazioni | todo | | | |
| S25 | Rilascio 0.1.0 | todo | | | |

Stati: `todo`, `in-progress`, `done`, `blocked`.

## Debiti (nit non bloccanti del revisore)
- (S0) CLAUDE.md/AGENTS.md: il riassunto di I13 omette che prima di S25 una migrazione si può correggere dichiarandolo in NOTES (più severo del §2.3).
- (S0) `.gitignore` senza `/.claude/worktrees/` (MusicLib lo ha; `.dockerignore` esclude già `/.claude/`).
- (S0) ~~`.gitattributes` senza marcatori `binary` per i fixture audio~~ — chiuso in S1 (`/testdata/library-v1/** -text`).
- (S0) Intestazione di `docker/gate.sh`: non dice che la build scarta l'output.
- (S1) `scripts/spike/report.go` (`runReport`): frammenti uniti senza riga vuota prima del titolo successivo (estetica del Markdown in `docs/spike-report.md`).
- (S1) `scripts/spike/inputs.go:89`: snapshot Debian via `http://` mentre MusicLib usa `https://`; il commento dice «the same snapshot».
- (S1) `scripts/spike/inputs.go` (`ffmpegGenerate`): non azzera `cmd.Env` (eredita `MUSICLIB_PASSWORD`) e non limita stderr.
- (S1) `scripts/spike/receipt.go:154` (`scanLibrary`): errore se una cartella artista sparisce fra due `ReadDir` (fallimento rumoroso sporadico, mai falso CONFIRMED).
- (S1) `scripts/spike.sh:104`: `docker stop --time 30` è un alias deprecato; meglio `-t 30`.
- (S1) `docs/spike-report.md`, H5: non dice che «non aumenta per un cambio di `render_version`» non è verificabile con una sola versione di MusicLib; H6: l'arrotondamento per difetto è provato solo da `TestDurationMS`.
- (S1) `scripts/spike/rebuild.go:113-116`: qualunque errore di `ReadDir` conta come «absent».
- (S1, osservazione) I file M4A di due esecuzioni indipendenti hanno SHA-256 diversi a parità di input (impronta identica); origine non indagata.

## TO CONFIRM aperti (da riportare all'utente a fine fase)
- N-014 (S1) Dopo un `rebuild` offline di MusicLib `.maintenance` sparisce ma `library/` resta vuota finché l'app non riparte: in quell'intervallo Vibrance vede tutti gli album non disponibili (poi tornano con gli stessi ID). Riportato all'utente a fine fase 0 (2026-10-01).

## Errata al design
Vedi la sezione «Errata» in fondo a DESIGN.md.
