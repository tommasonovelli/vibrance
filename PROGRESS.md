# Vibrance — avanzamento

Design: DESIGN.md (versione 0.1). Orchestratore: aggiorna questo file a ogni passo.

| Passo | Titolo | Stato | Round | Commit | Note |
|---|---|---|---|---|---|
| S0 | Bootstrap del repository e toolchain | done | 2 | b68c8e1 | 2026-09-30 |
| S1 | Spike: le ipotesi del contratto con MusicLib reale | done | 1 | 438cb1f | 2026-10-01 · H1–H9 confermate, nessun BLOCCO |
| S2 | Configurazione, avvio, salute e arresto | done | 1 | fb94888 | 2026-10-01 |
| S3 | Store SQLite, migrazioni, transazioni | done | 2 | b318e5b | 2026-10-01 |
| S4 | Ricevuta, accesso confinato e classificazione | done | 1 | 653e554 | 2026-10-01 |
| S5 | Adapter media: processi, tag, durata, impronta | done | 1 | bf75408 | 2026-10-02 |
| S6 | Identità e riconciliazione (puro) | done | 1 | 3753895 | 2026-10-02 · segue round di errata al §5.4 (S6e) |
| S6e | Errata al §5.4: planner più robusto (gemelle, impronte fra versioni) | done | 1 | cdf0d4e | 2026-10-02 · passo aggiunto dall'errata · 11 mutazioni del revisore, 1 equivalente (N-049) |
| S6m | Passaggio a MusicLib 1.2.0: pin, spike H1–H12, documenti | done | 1 | HASH_S6M | 2026-10-02 · passo aggiunto dall'errata · H1–H12 confermate, spike rieseguito dal revisore |
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
- (S2) `internal/config/config.go:115,119`: un'origine con credenziali e schema non http(s) (`ftp://admin:pw@host`) o porta non valida (`http://admin:pw@host:x`) viene ripetuta nel log `config_invalid`; N-019 promette di non ripeterla. Rimedio: non ripetere il valore se contiene `@`, ed estendere `TestLoadWithholdsUserInformation`.
- (S2) `config.go:120-122`: `http:host` (forma opaca) riceve il messaggio fuorviante «no user information (value withheld)».
- (S2) `internal/app/app.go:152`: `<-served` scarta l'errore di `Serve` dopo lo shutdown; per I9 alla lettera verificarlo con `errors.Is(http.ErrServerClosed)`.
- (S2) `app.go:215`: anche un errore di chiusura del server porta il codice `http_listen` (nome improprio).
- (S2) `cmd/vibrance/main_test.go` (`freeAddr`) e `internal/app/app_test.go` (`TestRun`): porta liberata e poi riusata; possibile fallimento raro (mai osservato in 25 ripetizioni).
- (S2) `cmd/vibrance/process_test.go:389`: `TestProcessStopCutsAnOpenRequest` usa `time.Sleep(500ms)`; `internal/app` usa già `ConnState`, deterministico.
- (S3) `internal/app/app.go:162`: `if ctx.Err() != nil` tratta come arresto normale qualunque errore di avvio che coincide con un SIGTERM (un rifiuto vero, p. es. `store_schema_too_new`, finirebbe a INFO con uscita 0). Più stretto: solo se l'errore è la cancellazione del context. Legato a N-027.
- (S3) `internal/store/store.go` `Close()`: usa `context.Background()` senza limite proprio (l'attesa è comunque limitata dal `busy_timeout`); nei test un `t.Fatalf` che lascia una `*sql.Conn` aperta blocca il cleanup fino al timeout: rilasciare le connessioni con `t.Cleanup`.
- (S3) `internal/store/tx_test.go` `TestWithWriteTxRollsBackOnError`: usa `t.Context()` senza scadenza; se il rollback si rompe fallisce solo per timeout del pacchetto.
- (S3) `internal/store/store.go:119`: `url.Values{"_pragma": connPragmas}` + `q.Add` fa `append` su una slice che condivide l'array della variabile di pacchetto; meglio `slices.Clone`.
- (S3) `README.md`, tabella dei codici: manca `store_close` (da aggiungere quando N-027 sarà confermato).
- (S3) `cmd/vibrance/process_test.go`: `-ldflags "-X main.stateDir=<path>"` si rompe se `TMPDIR` contiene spazi (non accade nel gate).
- (S3) `tx_test.go` `TestTransactionsWhileTheContextEnds`: 2000 commit con fsync, 2–15 s per esecuzione (`internal/store` nel gate da ~22 s a 26–33 s); 500 probabilmente bastano.
- (S3) `tx.go`: il nome `endOf` dice poco (`withContextErr`); commento a `tx_test.go:264-267` poco leggibile.
- (S3, per S12+) A context finito l'errore di `fn` soddisfa `errors.Is` sia per il context sia per la causa (p. es. `sql.ErrNoRows`): i handler devono controllare prima il context, altrimenti un client disconnesso può diventare un 404 (N-029).
- (S3, per S13/S21) Un `BEGIN IMMEDIATE` in attesa del lock di un altro processo non è interrotto dal context e dura fino ai 5 s di `busy_timeout` (N-029); `VACUUM INTO` è rifiutato su una connessione `query_only` (N-028).
- (S4) `internal/library/root.go:122`: nessun test fallisce se si toglie `io.LimitReader` da `ReadReceipt` (è provato il rifiuto oltre 16 MiB, non che la lettura si ferma). Rimedio: ricevuta sparsa enorme (`os.Truncate`) rifiutata subito.
- (S4, per S8/S24) `Discovery.Candidates` tiene in memoria le ricevute di tutti gli album (~200 byte per file): da misurare rispetto a T30.
- (S4) `discover_test.go:515`: in `TestDiscoverWhileAnAlbumIsRenamed` il controllo `count > 1` non può mai scattare (la mappa deduplica).
- (S4) `receipt.go:102-134`: `schema_version` 2 con una chiave nota ripetuta dà `receipt_invalid` invece di `receipt_schema_unsupported` (il controllo dei doppioni viene prima).
- (S4) `confinement_test.go`: non vieta `Stat` né `FS()` su `os.Root` (seguirebbero link interni); oggi coperto dai test di comportamento.
- (S4) Un artista rinominato da MusicLib fra `Artists()` e `Albums()` compare per un ciclo come `listing_failed` (scelta prudente, N-033).
- (S5) `-protocol_whitelist fd` è tenuto fermo solo dai test degli argomenti (`TestProbeArgs`, `TestFingerprintArgs`): la mutazione che lo toglie sopravvive ai test di comportamento (difesa in profondità: il demuxer è forzato).
- (S5) N-036: finestra teorica sul riuso del pid in `kill(-pid)` dopo la raccolta del processo (caso peggiore: uccidere un altro nostro ffmpeg). MusicLib la chiude con `golang.org/x/sys`, che il §2.4 non ammette (è già dipendenza indiretta): da proporre all'utente.
- (S5) `Command`, `Result`, `Runner.Run`, `ProbeTimeout`, `FingerprintTimeout` esportati ma usati solo dentro `internal/media`; `server.tools`, `Probe`, `MapTags`, `Fingerprint` usati solo dai test fino a S7/S8 (ordine del piano).
- (S5) `TestOnlyTheRunnerStartsProcesses` vieta qualunque selettore `.Name` in `internal/media` (un futuro campo `Name` innocuo lo farebbe fallire).
- (S5) `TestRunSemaphoreBoundsTheTools`: picco esattamente uguale agli slot con uno sleep di 0,2 s; dipende dai tempi su una macchina molto carica (20/20 con `-race`).
- (S5, per S7) `Probe` e `Fingerprint` danno codici diversi su un file non audio (`media_not_supported` / `media_tool_failed`, N-043): S7 deve mapparli su `probe_failed` e `fingerprint_failed`, guardando prima il context.
- (S6e) `reconcile.go:173-177`: il commento sui limiti cita 6(a) e 6(b) ma non il limite di N-050 (gemelle entrambe non disponibili).
- (S6e) `reconcile_property_test.go:~792-800`: manca una riga vuota fra `fingerprintOf`, `(*model).fingerprint` e `durationOf`.
- (S6e) N-050 descrive «prende il numero della cancellata, poi cestino e file riscritto»; il test copre la variante «cestino, poi torna spostata a un numero libero». Il caso esatto della nota non ha un test suo.
- (S6, per S7) Il fallback della singola traccia senza tag del §5.3 (titolo dal nome del file, `tags_incomplete`) è lasciato a S7 (N-045).
- (S6, per S8) Nel lavoro del §6.6 la coppia `(fingerprint, occurrence)` nuova può essere già occupata: va presa la prossima libera (N-048 punto 2).
- (S5, da chiudere in S7 per errata) `internal/media/runner_test.go:22,87-92`: `TestRunCancelKillsTheGroup` ha una corsa nel test (legge il gruppo mentre il figlio `mv` dello script `family` è ancora vivo); fallito una volta nel gate di S6m. Correzione: ripetere `groupMembers` finché coincide con `pids`, con scadenza.
- (S6m) `scripts/spike/moves_test.go:208-213`: `TestPollMove` dipende da due `time.Sleep(20ms)`.
- (S6m) `scripts/spike/moves.go:114` (`highest`): nessun test con due cartelle dello stesso `album_id` (mutazione Min/Max sopravvive; tocca solo l'osservazione senza esito).
- (S6m) `scripts/spike/compose.yaml`: il commento del servizio `app` cita ancora N-002 (il pin ora è in N-051); `report.go` `reportHeader`: il titolo resta «Spike S1».

## TO CONFIRM aperti (da riportare all'utente a fine fase)
- N-014 (S1) Dopo un `rebuild` offline di MusicLib `.maintenance` sparisce ma `library/` resta vuota finché l'app non riparte: in quell'intervallo Vibrance vede tutti gli album non disponibili (poi tornano con gli stessi ID). Riportato all'utente a fine fase 0 (2026-10-01).
- N-017 (S2) È marcato `DECIDED` in NOTES.md ma ha un effetto visibile: dopo la chiusura forzata all'arresto (richieste aperte oltre i 10 s di tolleranza) il processo esce con 0, mentre MusicLib esce con 1. Il revisore ritiene la lettura corretta; da riportare all'utente a fine fase A.
- (S2, osservazione del revisore, per S22) La tolleranza di arresto di 10 s coincide con il timeout di default di `docker stop` (10 s, poi SIGKILL): da S3 in poi, con uno stream aperto, il SIGKILL diventa probabile (checkpoint saltato; recuperabile perché crash-only). Il servizio Compose del §11.6 non ha `stop_grace_period`: possibile errata per S22, da chiedere all'utente a fine fase A.
- N-027 (S3) Codici di uscita attorno al database: un SIGTERM durante l'avvio (apertura o migrazione) è un arresto normale con uscita 0; se all'arresto un altro processo legge ancora il database, il checkpoint finale aspetta 5 s, poi `store_close` e uscita 1 (nessun dato perso).
- (S3, suggerimento del revisore) N-023: tre PRAGMA (`journal_mode`, `optimize`, `wal_checkpoint(TRUNCATE)`) sono costanti in `internal/store/store.go`, fuori da `sql/`, perché sqlc 1.31.1 scarta i PRAGMA; I7 alla lettera ammette solo l'eccezione FTS5. Proporre all'utente un'errata a I7 (tocca un'invariante: serve la sua approvazione, §0.8).
- N-033 (S4) Codici di problema scelti dove il §6.5 tace: ricevuta oltre 16 MiB → `receipt_too_large`; ricevuta illeggibile, non regolare o link → `receipt_invalid`; cartella album sparita fra elenco e lettura → nessun problema; cartella artista sparita → `listing_failed`; nomi che MusicLib non può scrivere (barra rovesciata, non UTF-8) → esclusi dagli elenchi senza problema.
- N-040 (S5) ffprobe non dichiara il bitrate dello stream FLAC: `format.bitrate` di una traccia FLAC sarà `null` nell'API. Alternativa: il bitrate dell'intero file (conta anche cover e tag).
- N-045 (S6) Un album senza alcun tag ALBUM prende il titolo visibile `Unknown Album` (il §5.3 tace; con MusicLib non può accadere).
- N-055 (S6m) `CLAUDE.md` e `AGENTS.md` nominano ancora MusicLib 1.1.0 (righe 16 e 33 di ciascuno): la sessione degli agenti non ammette modifiche a `CLAUDE.md` senza il consenso diretto dell'utente. Da aggiornare a 1.2.0 con il suo consenso.

## Errata al design
Vedi la sezione «Errata» in fondo a DESIGN.md.
- 2026-10-01 · S4 · firma `Discover(ctx, root, registered)` (N-032).
- 2026-10-02 · §0 · il design si può correggere per robustezza; agenti come `vibrance-worker` (Opus 5.5, effort medium).
- 2026-10-02 · S6 · §5.4: firme del planner, ordine di F1 senza `(disc, no)`, F2 abbina impronte uguali di qualunque versione e preferisce gli `old` disponibili, `occurrence` dopo F3, limiti noti.
- 2026-10-02 · S8 · §6.6: quando parte il lavoro delle nuove impronte e cosa fa se `(fingerprint, occurrence)` è occupata.
- 2026-10-02 · S8, S11, S18, S19, S23 · i riferimenti seguono l'audio: in P6 playlist e preferiti di una riga non disponibile passano alla riga disponibile con la stessa impronta (traccia spostata in un altro album da MusicLib 1.2.0); indice `tracks(fingerprint)`; scenari A17–A20. Il planner non cambia.
- 2026-10-02 · S6e · §5.4: limite noto (d), due gemelle entrambe non disponibili (N-050).
- 2026-10-02 · S6m · passo nuovo: passaggio a MusicLib 1.2.0 (pin, spike con H10–H12 su spostamento e aggiunta di tracce, libreria di prova, documenti). `.ref/musiclib` è ora il tag `v1.2.0`.
- 2026-10-02 · S7 · chiude nel passo S7 la corsa del test `TestRunCancelKillsTheGroup` (debito di S5).
