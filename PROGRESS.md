# Vibrance — avanzamento

Design: DESIGN.md (versione 0.1). Orchestratore: aggiorna questo file a ogni passo.

| Passo | Titolo | Stato | Round | Commit | Note |
|---|---|---|---|---|---|
| S0 | Bootstrap del repository e toolchain | done | 2 | (vedi git log: «Step S0») | 2026-09-30 |
| S1 | Spike: le ipotesi del contratto con MusicLib reale | todo | | | |
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
- (S0) `.gitattributes` senza marcatori `binary` per i fixture audio (da valutare in S1).
- (S0) Intestazione di `docker/gate.sh`: non dice che la build scarta l'output.

## TO CONFIRM aperti (da riportare all'utente a fine fase)

## Errata al design
Vedi la sezione «Errata» in fondo a DESIGN.md.
