# Vibrance

**Specifica implementativa — versione 0.1 — 2026-09-30**

Questo documento è l'unica fonte normativa del progetto. Le decisioni qui descritte sono finali: non sono alternative fra cui scegliere durante lo sviluppo. È scritto per essere letto da un **agente orchestratore** che guida il lavoro di altri due agenti (un ingegnere e un revisore) passo dopo passo. **Se sei l'orchestratore, inizia dal §0 e non saltare nulla.**

> **Poche funzioni, fatte bene.** Un server di ascolto in sola lettura sopra la libreria che Vibrance MusicLib produce: più utenti, preferiti, playlist, ricerca, un'API OpenAPI ben documentata. La semplicità è un requisito permanente, non una fase prima di un'architettura più grande.

---

## 0. Per l'orchestratore

### 0.1 Il tuo ruolo

Sei l'**orchestratore**. Non scrivi codice di produzione. Il tuo lavoro è far avanzare il piano del §14 un passo alla volta, nell'ordine, con questo ciclo:

```text
per ogni passo Sx del §14, dal primo non completato:
    round = 1
    ripeti:
        lancia l'INGEGNERE sul passo           (prompt dell'Appendice A)
        aspetta che termini e leggi il suo rapporto
        lancia il REVISORE sul lavoro fatto    (prompt dell'Appendice B)
        aspetta che termini e leggi il verdetto
        se VERDICT: APPROVED  -> committa in locale, aggiorna PROGRESS.md, passa al passo dopo
        altrimenti            -> round += 1 e rilancia l'ingegnere con i rilievi bloccanti
        se round > 3          -> FERMATI e chiedi all'utente (§0.8)
```

Tu sei l'unico che decide quando un passo è finito, e lo decidi **solo** sulla base del verdetto del revisore. Non approvi mai di tua iniziativa e non saltare mai il revisore, nemmeno per passi che sembrano banali.

L'utente ti ha detto soltanto «leggi DESIGN.md, sei l'orchestratore». Tutto ciò che ti serve per lavorare da solo è in questo documento. Se ti sembra che manchi qualcosa, applica il §0.8 (quando chiedere) e non inventare.

### 0.2 Prima di iniziare

1. Leggi per intero i §0, §1, §2 e §14. Consulta le altre sezioni quando ti servono per giudicare un rapporto: le leggeranno gli agenti, non serve che tu le tenga a memoria.
2. Verifica l'ambiente: `docker --version` e `docker compose version` (serve Docker con Compose v2; sulla macchina dell'utente è Windows con Docker Desktop e Git Bash). Non installare mai Go, ffmpeg o altri strumenti sull'host: tutto gira in Docker (§3.6).
3. Se `PROGRESS.md` esiste, riprendi dal primo passo che non è `done`. Altrimenti crealo dal modello dell'Appendice C, con tutti i passi del §14 in stato `todo`.
4. Se la cartella non è un repository git, esegui `git init -b main` e fai un primo commit locale che contiene solo `DESIGN.md` e `PROGRESS.md` (messaggio: `Add design and progress log`).
5. Prepara il riferimento a MusicLib, in sola lettura, per gli agenti:

   ```sh
   git clone --depth 1 --branch v1.1.0 https://github.com/tommasonovelli/vibrance-musiclib.git .ref/musiclib
   echo '.ref/' >> .git/info/exclude
   ```

   Gli agenti vi leggono codice e documentazione di MusicLib (formato della ricevuta, adapter ffprobe, script, Dockerfile) per imitarne stile e pin. Non si modifica mai e non entra mai in un commit.

### 0.3 Il ciclo di un passo, in dettaglio

1. **Stato pulito.** Prima di lanciare l'ingegnere, `git status` deve essere pulito (l'ultimo commit è l'ultimo passo approvato). Se non lo è, e non sei in un round di riscrittura, fermati: qualcuno ha modificato l'albero (§0.8).
2. **Segna il passo `in-progress`** in `PROGRESS.md` (round e data).
3. **Lancia l'ingegnere** (§0.4) e **aspetta** la sua notifica di completamento. Non fare polling, non predire l'esito, non scrivere mai tu un rapporto al posto suo.
4. **Leggi il rapporto** dell'ingegnere. Se contiene un `BLOCCO:` (ipotesi del design falsa, contraddizione, impossibilità) non lanciare il revisore: applica il §0.10.
5. **Lancia il revisore** (§0.5) con un agente **nuovo** e aspetta.
6. **Interpreta il verdetto** (§0.6).
7. **Se approvato:** §0.7. **Se servono modifiche:** rilancia un ingegnere **nuovo** passandogli i rilievi bloccanti e dicendogli di correggere l'albero di lavoro esistente (non di ricominciare).
8. Dopo il terzo round fallito: §0.8.

### 0.4 Come lanciare l'ingegnere

- Strumento `Agent`, tipo `general-purpose`, modello di default della sessione. Non dipendere da file di definizione degli agenti: il prompt dell'Appendice A è la fonte di verità.
- Riempi i segnaposto del prompt con: identificativo e titolo del passo, la **persona** professionale indicata nel passo, il numero del round, e (dal round 2) l'elenco dei rilievi bloccanti del revisore, copiati alla lettera.
- Non riassumere il passo: l'ingegnere legge il §14 e le sezioni che il passo cita. Tu gli dai solo il riferimento.
- Gli agenti lavorano in background e ti avvisano quando finiscono. Finché non arriva la notifica, non hai un esito.

### 0.5 Come lanciare il revisore

- Stesso strumento, sempre un agente **nuovo** (indipendenza: non deve ereditare il contesto dell'ingegnere).
- Prompt dell'Appendice B, con la stessa persona dell'ingegnere: ora è il suo pari che ne controlla il lavoro.
- Il revisore vede le modifiche non committate (`git diff` più i file nuovi) e deve **eseguire lui stesso** il gate (`scripts/check.sh`), senza fidarsi del rapporto dell'ingegnere.
- Non passargli il rapporto dell'ingegnere come prova: al massimo come elenco di dove guardare.

### 0.6 Interpretare il verdetto

Il revisore chiude con una sola di queste righe:

- `VERDICT: APPROVED` — il lavoro è pronto. Eventuali *nit* non bloccano: copiali nella sezione «Debiti» di `PROGRESS.md` e vai avanti. **Non fare un round solo per i nit.**
- `VERDICT: CHANGES REQUIRED` — seguito da rilievi bloccanti numerati (file:riga, cosa non va, perché, cosa deve ottenere la correzione). Rilancia l'ingegnere con questi rilievi e solo questi.

Un verdetto senza una di queste due righe non è valido: rilancia il revisore chiedendo il formato corretto (non conta come round dell'ingegnere).

### 0.7 Dopo l'approvazione

1. Se il rapporto dell'ingegnere o `NOTES.md` contengono voci `TO CONFIRM`, tienine il conto: le riassumerai all'utente a fine fase (§0.11).
2. Aggiorna `PROGRESS.md`: stato `done`, round impiegati, hash del commit, debiti nuovi.
3. Committa **in locale**: `git add -A && git commit`. Oggetto: la riga proposta dal revisore (imperativo, inglese). Corpo: `Step Sx: <titolo>`, una riga con l'esito del gate, e le righe di attribuzione previste dalle regole del tuo ambiente.
4. **Non fare mai `git push`**, non creare remoti, non creare tag. Pubblicare è dell'utente (§0.8).
5. Passa al passo successivo.

### 0.8 Quando fermarti e chiedere all'utente

Fermati, spiega in poche righe e attendi, nei casi seguenti. Fuori da questi casi procedi da solo.

- Un passo ha fallito tre round: riassumi i rilievi bloccanti rimasti e cosa l'ingegnere ha tentato.
- Un agente riporta un `BLOCCO:` che non puoi risolvere con le regole del §0.10.
- Il passo richiede qualcosa che non puoi ottenere: rete assente, immagine `ghcr.io` privata, permessi Docker negati più volte, credenziali.
- L'albero di lavoro è sporco quando non dovrebbe esserlo.
- Sta per accadere un'azione difficile da annullare o esterna: `git push`, tag, creazione di repository o pacchetti pubblici, `docker volume rm`, `docker compose down -v`, qualunque comando che tocchi un'installazione reale di MusicLib o Vibrance (progetti Compose diversi da quelli di sviluppo del §3.6).
- Una modifica al design cambierebbe il contratto API (§8), le invarianti (§2.3), lo scope (§1.3) o una decisione (§2.2): serve l'approvazione dell'utente (§0.10).
- Alla fine del passo S25 (rilascio): il resto è dell'utente.

### 0.9 Cosa non devi fare

- Scrivere o correggere codice di produzione. Puoi modificare solo `PROGRESS.md`, la sezione «Errata» in fondo a questo file (§0.10) e fare i commit.
- Eseguire due passi in parallelo o cambiare l'ordine del §14 (le dipendenze sono reali).
- Approvare un passo senza verdetto `APPROVED`, o modificare un verdetto.
- Riassumere o «migliorare» il testo di un passo nel prompt: riferiscilo per identificativo.
- Toccare `.ref/` o installazioni reali (§0.8).
- Aggiungere funzioni, dipendenze o passi che il design non prevede.

### 0.10 Se il design è ambiguo, sbagliato o impossibile

Gli agenti non devono mai deviare in silenzio dal design. Tre casi:

1. **Silenzio o ambiguità che non cambia il comportamento visibile.** L'ingegnere sceglie la lettura più semplice e conservativa e la registra in `NOTES.md` come `DECIDED`. Tu non intervieni.
2. **Ambiguità che cambia il comportamento visibile.** L'ingegnere sceglie l'opzione più conservativa, la registra come `TO CONFIRM` e prosegue. Tu la riporti all'utente a fine fase.
3. **Contraddizione o ipotesi falsa** (per esempio un'ipotesi del passo S1 che la realtà smentisce, o un requisito impossibile). L'ingegnere si ferma e scrive `BLOCCO:` nel rapporto con le prove. Tu:
   - se la correzione non tocca contratto API, invarianti, scope o decisioni: aggiungi una voce alla sezione «Errata» in fondo a questo file (data, passo, testo), rilancia l'ingegnere;
   - altrimenti: fermati e chiedi all'utente (§0.8).

### 0.11 Cosa comunicare all'utente

- **Dopo ogni passo approvato:** una riga, per esempio `S7 completato (2 round) — indicizzazione di un album con commit transazionale, gate verde`.
- **A fine fase** (fine di S1, S10, S20, S23, S25): un breve riepilogo di cosa funziona ora, dei `TO CONFIRM` accumulati e dei debiti aperti, con le domande che richiedono una sua risposta.
- **Alla fine:** il rapporto finale del §14 (S25).
- Niente messaggi intermedi lunghi. Tutto il dettaglio sta in `PROGRESS.md`, `NOTES.md` e nei commit.

### 0.12 Se perdi il contesto

Il tuo stato vive nei file, non nella memoria: rileggi i §0, §1, §2, §14, poi `PROGRESS.md`, `NOTES.md` e `git log --oneline`. Riprendi dal primo passo non `done`. Se un passo è `in-progress` e l'albero è sporco, non sai se il lavoro è completo: lancia il revisore su quello stato prima di decidere (il round non aumenta).

---

## 1. Il prodotto

### 1.1 Risultato atteso

L'utente ha già **Vibrance MusicLib** (repository `tommasonovelli/vibrance-musiclib`): uno strumento da curatore che importa album, corregge i metadati e produce una cartella `library/` ordinata, con tag completi e cover. **Vibrance** è il secondo prodotto: l'esperienza di *ascolto*, pensata per chi vuole solo godersi la musica, come aprire Spotify.

La separazione è voluta, anche a livello di percezione: chi entra in MusicLib ha in mente di modificare; chi entra in Vibrance ha in mente di ascoltare. Sono due siti, due identità.

```text
musiclib.miodominio.net   MusicLib (curatore: tocca tutto, resta locale o dietro VPN)
vibrance.miodominio.net   Vibrance (ascolto: sola lettura, per la famiglia)
vibrance.miodominio.net/api/v1/...   l'API di Vibrance, documentata con OpenAPI
```

Vibrance è un server con un'API pensata su misura per i client dell'utente (prima un client web, poi uno mobile). **Non** implementa l'API Subsonic e non la imita. Questo documento copre il **server e la sua API**; l'interfaccia web è una fase successiva e non è descritta qui (§1.3).

### 1.2 Funzioni della v0.1

- Più utenti con ruoli `admin` e `user`: login con nome utente e password, sessioni per browser (cookie) e per app mobile (token), revoca delle sessioni.
- L'admin crea, modifica e rimuove gli utenti e vede lo stato della libreria.
- Catalogo in sola lettura: artisti, album, tracce, ricerca full-text.
- Riproduzione: il file originale in streaming con Range, le cover (con miniature), i testi sincronizzati.
- Preferiti (tracce) e playlist private per utente.
- Identità delle tracce stabile: preferiti e playlist sopravvivono a qualunque modifica fatta in MusicLib (§5.4).
- Documentazione OpenAPI servita dal server stesso.
- Installazione con Docker Compose, insieme a MusicLib, come avviene per MusicLib.
- Backup e ripristino dei dati di Vibrance; comando `doctor`.

### 1.3 Fuori scope

Interfaccia web (fase successiva), transcodifica, cronologia di ascolto e scrobbling, preferiti di album e artisti, immagini degli artisti, playlist condivise o intelligenti, più librerie, compatibilità Subsonic/OpenSubsonic, podcast, radio, ricerca online e riconoscimento musicale, modifica di tag o metadati, upload e importazione (sono di MusicLib), analisi sonora, Chromecast e simili, Redis, Typesense, repliche o più istanze.

L'Appendice D elenca le idee rimandate, con il motivo: servono a non perderle, non a implementarle.

### 1.4 Criterio di completamento della v0.1

La v0.1 è pronta quando, su un'installazione pulita con MusicLib e Vibrance nello stesso Compose:

1. un'utente importa album di prova in MusicLib, li corregge (titoli, numeri, cover, artista, cestino e ripristino) e Vibrance li mostra coerenti entro un ciclo di scansione;
2. le playlist e i preferiti creati prima di ogni modifica restano corretti dopo la modifica (la tabella degli scenari del passo S23 passa per intero);
3. ogni endpoint del §8 si comporta come la specifica OpenAPI, validato dai test;
4. il gate (`scripts/check.sh`) e la suite di contratto con MusicLib reale (`scripts/contract.sh`) passano;
5. i budget di prestazioni del passo S24 sono rispettati;
6. backup, ripristino e `doctor` funzionano su dati reali di prova.

---

## 2. Principi, decisioni e invarianti

### 2.1 Principi

- **Semplice, noioso, verificabile.** Ogni tecnologia nuova costa una «fiche di innovazione» (Dan McKinley, *Choose Boring Technology*). Ne spendiamo due e solo due: l'**identità delle tracce tramite impronta audio** e il **contratto API come fonte del codice e dei test**. Tutto il resto è tecnologia già nota.
- **Indipendenza da MusicLib.** I due prodotti comunicano **soltanto** tramite la cartella `library/`. Nessuna API fra loro, nessun database condiviso, nessuna dipendenza di avvio. Se MusicLib è fermo, Vibrance continua a funzionare in ogni sua parte.
- **Riconciliazione a livello, non a eventi.** Lo scanner confronta lo stato *desiderato* (la libreria su disco) con quello *osservato* (l'indice) e li riallinea. È idempotente: si può ripetere in qualunque momento e dopo qualunque interruzione (Bowes, *Level Triggering and Reconciliation in Kubernetes*). Gli eventi del filesystem, se mai arriveranno, saranno solo suggerimenti.
- **Crash-only.** Non esiste un arresto «pulito» speciale né un avvio «speciale»: fermarsi è uscire, avviarsi è recuperare (Candea e Fox, *Crash-Only Software*). Ogni stato intermedio deve essere sicuro da interrompere.
- **Non distruttivo.** Lo scanner non cancella mai righe dell'indice: segna la disponibilità. Un errore di lettura, un volume smontato o un album nel cestino di MusicLib non distruggono mai playlist o preferiti; tutto ricompare quando i file tornano.
- **Le misure prima delle ottimizzazioni.** Nessuna cache, pool o parallelismo che non nasca da una misura (passo S24).

### 2.2 Decisioni

| ID | Decisione | Perché |
|---|---|---|
| D1 | Due prodotti, un solo contratto: la cartella `library/` in sola lettura. | Sicurezza (in LAN c'è solo ciò che non può modificare) e isolamento dei guasti. |
| D2 | Stessi pin di toolchain di MusicLib (Go 1.25.14, Debian, immagini per digest). Nota: Go 1.25 è fuori supporto dal 2026-08-19 (è uscito Go 1.27.0): il passo S25 ha un controllo di rilascio che propone all'utente il passaggio a Go 1.27.x. | Coerenza voluta dall'utente; per Vibrance il passaggio è banale perché non ha chiavi congelate. |
| D3 | SQLite in un solo file, driver puro Go `modernc.org/sqlite`. Niente PostgreSQL, **niente Redis**, niente Typesense. | I dati sono pochi e letti in-process: le query sono chiamate di funzione, non round-trip di rete. Un secondo archivio costa coerenza, operatività e invalidazione. Se una misura mostrerà un punto caldo, si userà una cache *in-process*, mai un servizio. |
| D4 | Ricerca con FTS5 (`unicode61 remove_diacritics 2`, prefissi). | Basta alla scala di una collezione personale; nessun servizio in più. La ricerca sta dietro un'interfaccia interna: passare a Typesense non cambierebbe l'API. |
| D5 | OpenAPI 3.0.3 scritto a mano, *prima* del codice; `oapi-codegen` (strict server) per i tipi; `kin-openapi` per validare richieste e, nei test, risposte. | La documentazione non può divergere dal comportamento. |
| D6 | L'identità di una traccia è un UUID assegnato da Vibrance alla prima comparsa e mantenuto tramite **impronta audio** (hash dei pacchetti compressi), mai tramite percorso o numero. Le righe non si cancellano mai. | I metadati cambiano, l'audio no (§5.4). |
| D7 | Scanner a riconciliazione, non distruttivo, con commit transazionale per album (§6). | Crash-only e sicuro da ripetere. |
| D8 | Sessioni nel database, stessa tabella per cookie (web) e token (mobile); password con argon2id; verifica delle password **una alla volta**. | Sopravvivono al riavvio; revocabili; un attaccante non può provare password in fretta né esaurire la memoria. |
| D9 | API nella stessa origine dell'interfaccia: Caddy instrada tutto `vibrance.miodominio.net` al server, che serve `/api/v1`. Niente CORS. | Il cookie funziona, `<audio src>` si autentica da solo, l'API funziona identica senza Caddy. |
| D10 | Nessuna transcodifica nella v0.1. Il parametro `profile` dell'endpoint audio è riservato: qualunque valore diverso da assente o `original` dà `400 unsupported_profile`. | Scope. Sarà la prima aggiunta (ALAC nei browser diversi da Safari). |
| D11 | Il file originale si serve con `http.ServeContent`; `ETag` = SHA-256 del file dalla ricevuta; guardia `(size, mtime)` contro file sostituiti (§9.1). | Range, `If-Range` e cache corretti senza codice nostro. |
| D12 | Le cover sono **solo dell'album** (le tracce ereditano quella dell'album); miniature 256 e 640 px generate su richiesta e messe in cache su disco; l'URL contiene l'hash della cover. | MusicLib incorpora in ogni traccia al massimo la cover dell'album. |
| D13 | I testi LRC si convertono sul server in JSON strutturato. | Nessun client deve rifare il parsing. |
| D14 | Playlist private. `If-Match` obbligatorio solo per operazioni che dipendono dalle posizioni (§8.6). | Aggiungere una traccia da ovunque deve essere una richiesta sola. |
| D15 | Lo stack Compose è **il `compose.yaml` di MusicLib alla versione fissata, invariato**, più il servizio `vibrance`; stesso nome di progetto `musiclib`. Un controllo automatico verifica che le parti di MusicLib non siano derivate. | Le installazioni esistenti di MusicLib si adottano senza migrare volumi; la documentazione di MusicLib (`docker compose stop app`, …) resta valida. |
| D16 | Vibrance monta `/data` di MusicLib in sola lettura. La sua *readiness* non dipende dallo stato di MusicLib. | D1. |
| D17 | Errori `{code, message, details}` con codici stabili; paginazione `?limit=&after=` con risposta `{..., "next": "..."\|null}`; JSON rigoroso (chiavi ignote o duplicate rifiutate). | Stesse convenzioni di MusicLib. |
| D18 | `ffmpeg` e `ffprobe` sono i binari statici fissati di MusicLib (versione `8.1.3-musiclib1`), copiati dalla sua immagine pubblicata. | Gli stessi strumenti che hanno verificato l'audio leggono i file. |
| D19 | Timestamp come interi (millisecondi Unix UTC); ID come `TEXT` UUID minuscolo; UUIDv7 per ciò che crea Vibrance, UUIDv5 per gli artisti, `album_id` di MusicLib per gli album. | Niente ambiguità di formato fra driver e `sqlc`. |
| D20 | Nessuna interfaccia web in questo design. `/` reindirizza a `/api/docs`. Il router lascia un punto di innesto per la UI futura. | Scope. |
| D21 | Questo documento è in italiano; **tutto ciò che sta nel repository** (codice, commenti, messaggi, log, documentazione, commit) è in inglese. | Come MusicLib. |

### 2.3 Invarianti

Sono non negoziabili. Il revisore le controlla a ogni passo.

- **I1.** Vibrance non scrive mai in `/musiclib`. Legge soltanto `library/` e i marcatori `.maintenance` e `.musiclib-store` della radice, sempre tramite `os.Root`.
- **I2.** L'input del client non forma mai un percorso del filesystem. I percorsi vengono solo dalle colonne `rel_path` del database, scritte dallo scanner.
- **I3.** Lo scanner non cancella righe di `artists`, `albums`, `tracks`: cambia solo `available`. Un ID di traccia, una volta creato, non cambia mai.
- **I4.** Ogni richiesta diversa da GET e HEAD richiede `X-Vibrance-Request: 1`; `Host` deve corrispondere a `VIBRANCE_PUBLIC_ORIGIN` (421) e `Origin`, se presente, deve coincidere (403). Niente CORS. GET e HEAD non hanno effetti collaterali visibili (l'aggiornamento di `last_used_at` della sessione è l'unica eccezione e non cambia risposte).
- **I5.** Password, token, cookie e hash non compaiono mai in log, errori, risposte o messaggi di test. I token si salvano solo come SHA-256; le password solo come stringa PHC argon2id; i confronti sono a tempo costante.
- **I6.** Ogni endpoint è coperto dalla matrice di autorizzazione dei test (§12.4): un endpoint nuovo senza riga nella matrice fa fallire il gate. Una risorsa di un altro utente risponde `404`, mai `403`.
- **I7.** Tutto l'SQL sta in `sql/*.sql` (sqlc), con l'unica eccezione dell'FTS5 in `internal/search`. Nessuna stringa SQL costruita con input dell'utente.
- **I8.** Nessun processo esterno senza `context`, timeout, limite di stderr e file passati per descrittore (mai per percorso).
- **I9.** Nessun errore ignorato, in particolare di `Close`, `Rollback`, `Commit`, `Rows.Err`.
- **I10.** Ogni risposta dell'API è conforme alla specifica OpenAPI (verificato dai test con `kin-openapi`).
- **I11.** Una sola connessione di scrittura SQLite; transazioni di scrittura brevi; **nessun I/O** (ffmpeg, disco, rete) dentro una transazione di scrittura.
- **I12.** Tutto è fissato: immagini per digest, moduli a versione esatta, mai `latest`.
- **I13.** Le migrazioni sono solo in avanti e non si modificano dopo essere state applicate a dati reali. Finché non esiste un rilascio (S25) e un'installazione reale, il database vive solo in fixture e volumi di sviluppo: un passo può correggere una migrazione esistente se lo dichiara nel rapporto e in `NOTES.md`; dopo il primo rilascio si aggiungono solo migrazioni nuove. Un database più nuovo del binario si rifiuta sempre.
- **I14.** Vibrance funziona senza MusicLib: login, preferiti, playlist, ricerca e stato si servono anche con MusicLib spento e con `library/` assente (i file non leggibili diventano `track_unavailable`).
- **I15.** Le colonne `available` descrivono ciò che lo scanner ha osservato; nessun altro codice le modifica.
- **I16.** Nessuna funzione, dipendenza, opzione o passo che il design non prevede.

### 2.4 Dipendenze Go ammesse

`modernc.org/sqlite`, `github.com/pressly/goose/v3`, `github.com/google/uuid`, `golang.org/x/crypto` (argon2), `golang.org/x/text`, `golang.org/x/image`, `golang.org/x/sync`, `github.com/getkin/kin-openapi`, `github.com/oapi-codegen/runtime`, `github.com/oapi-codegen/nethttp-middleware`, `github.com/google/go-cmp` (solo test), e lo strumento `oapi-codegen` come direttiva `tool` di `go.mod`. Qualunque altra dipendenza va motivata nel rapporto e in `NOTES.md` come `TO CONFIRM`. Versioni esatte, compatibili con `go 1.25.0`.

### 2.5 Stile del codice

- Funzioni piccole con ingressi e uscite espliciti; tipi concreti; interfacce solo ai confini utili ai test. Le dipendenze si passano nei costruttori.
- Niente ORM, contenitore di dependency injection, event bus, framework di job, code esterne, cache speculative, configurazioni speculative.
- Errori tipizzati con codici stabili e contesto aggiunto lungo la catena (`fmt.Errorf("...: %w", err)`). `context.Context` nella firma di ogni operazione lunga.
- Il *planner* della riconciliazione non fa I/O; lo store non conosce percorsi assoluti; l'adapter media non conosce il dominio; i handler HTTP non contengono SQL.
- Una sola implementazione di normalizzazione dei nomi, di chiavi di ordinamento, di codifica dei cursori, di validazione dei percorsi.
- Commenti solo dove il codice non spiega il *perché*. Documentazione e test cambiano insieme a qualsiasi modifica delle garanzie.

### 2.6 Lingua

Il design è in italiano. Il repository è in inglese: codice, identificatori, commenti, messaggi di errore, log, test, documentazione, commit. Nei commenti si cita il design per sezione (`DESIGN.md §6.3`) solo finché il file esiste nel repository; le regole importanti vanno comunque scritte per esteso, come in MusicLib.

---
## 3. Architettura e stack

### 3.1 Panoramica

```text
                 ┌────────────────────── Compose (progetto `musiclib`) ──────────────────────┐
                 │                                                                           │
Browser / app ──►│ Caddy (opz.) ─► vibrance ◄── /musiclib (sola lettura) ◄── app (MusicLib) ─┤─ postgres
  HTTPS          │                   │                 library/  .maintenance  .musiclib-store│
                 │                   ├── SQLite  /var/lib/vibrance/vibrance.db               │
                 │                   └── cache   /var/lib/vibrance/thumbs/                   │
                 └───────────────────────────────────────────────────────────────────────────┘
```

Un solo binario Go (`vibrance`) con: server HTTP, scanner in background, sottocomandi operativi. Nessun altro servizio oltre a `ffmpeg`/`ffprobe` lanciati come processi figli.

### 3.2 Stack

| Area | Decisione |
|---|---|
| Linguaggio | Go 1.25.x con gli stessi pin di MusicLib (D2). `CGO_ENABLED=0`. |
| HTTP | `net/http` con `ServeMux` (pattern con metodo); niente framework. |
| Contratto | `api/openapi.yaml` (OpenAPI 3.0.3) → `oapi-codegen` modalità `std-http-server` + `strict-server`; `kin-openapi` per validazione. |
| Database | SQLite, `modernc.org/sqlite`, WAL, una connessione di scrittura + un pool di sola lettura (§5.1). |
| Query | `sqlc` (motore `sqlite`, pacchetto `database/sql`), codice generato e committato. FTS5 scritto a mano (§10). |
| Migrazioni | `goose`, SQL incorporato nel binario, solo in avanti. |
| Password | argon2id (`x/crypto/argon2`), formato PHC. |
| Audio | `ffprobe` (tag, durata, codec) e `ffmpeg -c copy -f hash` (impronta): processi senza shell, file per descrittore. |
| Immagini | `image/jpeg`, `image/png`, `golang.org/x/image/draw`. |
| Log | `log/slog` in JSON. |
| Configurazione | Solo variabili d'ambiente, validate all'avvio. |
| Test | `testing`, `httptest`, `go-cmp`, fuzzing nativo, `-race`; `kin-openapi` per la conformità. |
| Immagine | Multi-stadio, base Debian slim, utente non root, filesystem in sola lettura. |

### 3.3 Struttura del repository

```text
cmd/vibrance/           main e sottocomandi: serve, healthcheck, version, user, backup, restore, doctor
internal/config/        lettura e validazione dell'ambiente
internal/app/           cablaggio dei componenti, avvio e arresto (ordine del §11.2)
internal/httpx/         middleware del confine HTTP, modello degli errori, JSON rigoroso, cursori
internal/api/           codice generato (api.gen.go) e handler, un file per area
internal/auth/          password, token, sessioni, limitatore di login, bootstrap dell'admin
internal/library/       accesso confinato (os.Root), ricevuta, classificazione dei file,
                        riconciliazione (pura), indicizzatore, scanner
internal/media/         runner dei processi, ffprobe, mappatura dei tag, impronta
internal/catalog/       servizi di lettura (liste, dettagli), preferiti, playlist
internal/search/        FTS5: manutenzione e costruzione delle query
internal/covers/        miniature e cache su disco
internal/lyrics/        parser LRC
internal/names/         normalizzazione, chiavi di ordinamento, id degli artisti
internal/store/         codice sqlc, migrazioni incorporate, apertura e transazioni
internal/buildinfo/     versione stampata in fase di build
api/                    openapi.yaml e configurazione di oapi-codegen
migrations/  sql/       migrazioni goose e query sorgenti di sqlc
scripts/  docker/       gate, shell di sviluppo, generazione, contratto, wrapper operativi
docs/                   operations.md (guida all'uso), compat.md (versioni di MusicLib provate)
testdata/               libreria di prova prodotta da MusicLib (§12.2) e altri fixture
```

Regole di dipendenza: `library` non conosce HTTP né `api`; `store` non conosce percorsi assoluti né HTTP; `media` non conosce il dominio; `api` chiama i servizi di `catalog`/`auth`/`search`/`covers`/`lyrics` e non contiene SQL; `app` è l'unico che collega tutto. Niente import circolari aggirati con interfacce inutili.

### 3.4 Modello di esecuzione

- **Processi e goroutine:** il server HTTP; **uno** scanner (con un pool di worker per il lavoro costoso); la pulizia oraria delle sessioni scadute. Niente altro.
- **Un'istanza per database.** Il file `vibrance.db` ha un solo processo server. I sottocomandi operativi (`user`, `backup`, `doctor`) possono girare mentre il server è attivo: SQLite in WAL con `busy_timeout` lo consente. `restore` richiede uno stato vuoto.
- **Percorsi fissi nel container:** `/musiclib` (sola lettura, contiene `library/`), `/var/lib/vibrance` (stato: `vibrance.db`, `thumbs/`), `/backup`. Non sono configurabili.

### 3.5 Piattaforma supportata

Linux amd64 con Docker e Compose v2, come MusicLib (Ubuntu 24.04+). Il disco di `/var/lib/vibrance` deve essere **locale** (SQLite WAL non funziona su NFS/SMB). Sviluppo su Windows con Docker Desktop e Git Bash è supportato (§13, T22); il rilascio richiede una prova su Linux nativo.

### 3.6 Toolchain, Docker e script di sviluppo

Tutto si costruisce, si prova e si esegue in Docker; l'host ha solo Docker con Compose v2. Come in MusicLib:

- **Pin di riferimento** (copia *esattamente* tag e digest dal clone `.ref/musiclib`, file `docs/docker.md`, sezione «Pinned images», e dal suo `Dockerfile`; non ricopiarli da questo documento): Go 1.25.14 su Debian trixie; runtime `debian:trixie-…-slim`; `docker/dockerfile` frontend; `sqlc` 1.31.1; `shellcheck` 0.11.0; PostgreSQL 17.11 (solo nel compose di stack); Caddy 2.11.4 (solo nella documentazione).
- **ffmpeg e ffprobe:** stadio del Dockerfile `FROM ghcr.io/tommasonovelli/musiclib:1.1.0@sha256:<digest>` da cui si copiano `/usr/local/bin/ffmpeg` e `/usr/local/bin/ffprobe`. Il digest si risolve con `docker buildx imagetools inspect` (il passo S1 verifica che l'immagine sia scaricabile; se è privata, FERMATI, §0.8). All'avvio il server verifica che `ffmpeg -version` e `ffprobe -version` riportino il token `8.1.3-musiclib1` (`media_tool_version`, come MusicLib).
- **Progetto Compose di sviluppo:** `vibrance-dev`; di contratto: `vibrance-contract`; di prova a mano: `vibrance-spike`. **Mai** il progetto `musiclib` dell'utente. Tutti gli script impostano esplicitamente il nome.
- **Script** (modellati su quelli di MusicLib, `.ref/musiclib/scripts` e `docker/`): `scripts/check.sh` (il gate: `sqlc diff`, generazione OpenAPI senza differenze, build, vet, gofmt, `go test -race`, in un container senza rete), `scripts/dev.sh` (shell o comando nel container di toolchain sui sorgenti vivi), `scripts/sqlc.sh`, `scripts/generate.sh` (oapi-codegen; `generate.sh diff` per il gate), `scripts/fuzz.sh`, `scripts/lint-shell.sh`, `scripts/contract.sh` (suite con MusicLib reale, **non** nel gate veloce), `scripts/check-compose-sync.sh` (§11.7).
- **Windows:** gli script si eseguono da Git Bash con `MSYS_NO_PATHCONV=1`; il checkout ha finali di riga LF (`.gitattributes`).
- Il gate testa una **fotografia** dell'albero presa al momento della build, come in MusicLib; `dev.sh` lavora sui sorgenti vivi.

---

## 4. Il contratto con MusicLib

Questa sezione descrive ciò su cui Vibrance può contare. Le affermazioni marcate **[S1]** sono ipotesi ricavate dalla documentazione di MusicLib 1.1.0 che il passo S1 deve verificare con il prodotto reale; se una è falsa, il passo S1 si ferma (§0.10).

### 4.1 Il layout su disco

```text
library/
  <Artista>/
    <Album>/
      .musiclib.json              ricevuta tecnica (§4.2)
      cover.jpg | cover.png       cover dell'album, alla radice
      NN - Titolo.ext             tracce (flac | mp3 | m4a); con "Disc N/" se multidisco
      Disc 2/NN - Titolo.ext
      NN - Titolo.lrc             testo, accanto alla traccia, stesso nome base
      Extras/...                  allegati di ogni genere: Vibrance li ignora
```

- I nomi delle cartelle e dei file sono **ripuliti** (`/ \ : * ? " < > |` diventano `_`, nomi lunghi accorciati con suffisso hash). **Il percorso perde informazione: Vibrance non lo interpreta mai.** Nomi, titoli e numeri si leggono dai **tag**, che MusicLib scrive completi.
- La sostituzione di un album è atomica (`renameat2` con scambio): si vede la versione vecchia o la nuova, mai una a metà. Un file già aperto resta leggibile dopo la sostituzione. **[S1]**
- Durante una **rinomina** (titolo o artista) le due cartelle, vecchia e nuova, possono coesistere per un istante: due ricevute con lo stesso `album_id`. Vale quella con `album_revision` più alta (§6.2).

### 4.2 La ricevuta `.musiclib.json`

Una riga di JSON compatto, campi in questo ordine:

```json
{"schema_version":1,"album_id":"<uuid>","build_id":"<uuid>","album_revision":7,"render_version":"<s>",
 "files":[{"relative_path":"01 - So What.flac","size":123,"sha256":"<hex64>"}, ...]}
```

- `album_id`: l'identità dell'album, stabile per sempre (UUIDv7, creato da MusicLib). **È l'ID che Vibrance usa per gli album.**
- `album_revision`: aumenta a ogni modifica che cambia l'output dell'album; non aumenta per un render forzato o per un cambio di `render_version`. **[S1]**
- `build_id`: nuovo a **ogni** costruzione: la ricevuta cambia a ogni render anche a contenuto identico.
- `render_version`: identifica renderer, regole dei nomi e versioni degli strumenti. Quando cambia (aggiornamento di MusicLib), tutti gli album vengono riscritti con la stessa revisione.
- `files`: tutti i file dell'album **tranne la ricevuta stessa**, con dimensione e SHA-256. Ordinati per byte UTF-8 del percorso.
- **Nessun nome, titolo o timestamp.** La ricevuta è un'attestazione di integrità, non una fonte di metadati.

Il parsing di Vibrance: legge al massimo 16 MiB; richiede `schema_version == 1` (altrimenti l'album è ignorato con problema `receipt_schema_unsupported`); **ignora campi sconosciuti** dentro la versione 1; valida UUID, `album_revision > 0`, `sha256` esadecimale minuscolo di 64 caratteri, percorsi senza `..`, senza assoluti, senza separatori `\`. L'hash della ricevuta (`receipt_hash`) è lo SHA-256 dei byte del file.

La ricevuta **non è un'interfaccia pubblica** di MusicLib: il suo formato è bloccato da un test golden di MusicLib, ma MusicLib non promette nulla ad altri programmi. Per questo Vibrance dichiara le versioni provate (`docs/compat.md`) e la suite `scripts/contract.sh` è l'arbitro (§12.3).

### 4.3 Tag scritti da MusicLib (tutte le tracce)

| Significato | FLAC (Vorbis) | MP3 (ID3v2.4) | M4A |
|---|---|---|---|
| Titolo | TITLE | TIT2 | ©nam |
| Artista | ARTIST | TPE1 | ©ART |
| Artista album | ALBUMARTIST | TPE2 | aART |
| Album | ALBUM | TALB | ©alb |
| Traccia / totale | TRACKNUMBER / TRACKTOTAL | TRCK | trkn |
| Disco / totale | DISCNUMBER / DISCTOTAL | TPOS | disk |
| Anno | DATE | TDRC | ©day |
| Genere | GENRE | TCON | ©gen |
| Compilation | COMPILATION (`1` o assente) | TCMP | cpil |
| Cover | PICTURE front | APIC front | covr |

- I campi gestiti sono **riscritti completamente** a ogni render: un valore assente significa «non c'è». Il tag `ARTIST` è sempre presente (se la traccia eredita l'artista dell'album, MusicLib scrive quello).
- **I tag non gestiti sono conservati** (compositore, commenti, **ReplayGain**, testi incorporati…). Se i file originali avevano ReplayGain, Vibrance può leggerlo a costo zero (§5.2, colonne `rg_*`). **[S1]**
- I tag multivalore sono un'unica stringa con valori uniti da `; `: Vibrance non li spezza.
- Il *featuring* resta testo dell'artista della traccia. `Various Artists` è un artista come gli altri.
- I tag di ordinamento (`TITLESORT`, …) sono rimossi da MusicLib: Vibrance calcola le proprie chiavi (§5.5).
- Ogni traccia contiene l'**immagine della cover incorporata**: ffprobe la elenca come uno stream *video* (attached picture). Vibrance non la estrae mai: usa `cover.jpg|png` (T1, T29).
- Poiché i tag sono riscritti da MusicLib, **modificare un tag cambia lo SHA-256 del file** ma non i pacchetti audio compressi. Su questo si fonda l'impronta (§5.4).

### 4.4 Cover, testi, allegati

- Cover: `cover.jpg` o `cover.png` alla radice dell'album, JPEG o PNG validi, fino a 20 MiB e 40 megapixel. Assente se l'album non ha cover.
- Testi: un `.lrc` UTF-8 con lo stesso nome base della traccia, nella stessa cartella. Un `.lrc` non UTF-8 o ambiguo resta tra gli allegati (Vibrance non lo vede).
- Tutto ciò che sta in `Extras/` è ignorato.

### 4.5 I marcatori di manutenzione

- `/musiclib/.maintenance` esiste mentre un `rebuild` o un `restore` offline di MusicLib sono in corso: **`library/` può essere vuota o incompleta.** Se c'è, lo scanner non tocca l'indice e lo stato diventa `maintenance` (§6.1).
- `/musiclib/.musiclib-store` esiste in una installazione sana (contiene `store_id=<uuid>`). Se manca, `/musiclib` non è il volume di MusicLib (montaggio sbagliato o vuoto): lo scanner non fa nulla e lo stato diventa `unavailable`. Vibrance usa questi due file **solo come segnali**, e degrada con garbo se un giorno MusicLib li cambiasse (il passo S23 verifica).

### 4.6 Durata

La durata di una traccia è `duration_ts × time_base` dello stream audio, in millisecondi, arrotondata **per difetto**; se il contenitore non dichiara durata resta `NULL`, non `0` (stessa regola di MusicLib). Per un MP3 senza intestazione Xing/VBRI è la stima di libavformat.

---

## 5. Modello dati e identità

### 5.1 SQLite

- **Un file**, `/var/lib/vibrance/vibrance.db`, in modalità WAL. Contiene sia l'indice della libreria sia i dati degli utenti: poiché le identità delle tracce (§5.4) non si ricostruiscono, **tutto il database è prezioso** e ha un solo backup (§11.4). Le miniature sono cache e si possono cancellare.
- **Due handle `database/sql`:** *scrittura* (`SetMaxOpenConns(1)`, transazioni `BEGIN IMMEDIATE`) e *lettura* (pool, `query_only`). Le transazioni di scrittura sono brevi e senza I/O (I11). Parametri della connessione, via DSN, valgono per **ogni** connessione del pool: `foreign_keys=ON`, `busy_timeout=5000`, `journal_mode=WAL`, `synchronous=FULL` (i dati degli utenti contano più della velocità di scrittura, che è bassa). (T3, T4)
- Timestamp: interi in millisecondi Unix UTC. ID: `TEXT` UUID minuscolo di 36 caratteri. Booleani: `INTEGER` 0/1 con `CHECK`. (D19, T8)
- Le tabelle `artists`, `albums`, `tracks` hanno una chiave `seq INTEGER PRIMARY KEY` esplicita oltre all'`id` testuale: serve all'FTS5 (T6).
- Migrazioni `goose` incorporate; all'avvio, prima di servire. Un database più nuovo del binario si rifiuta (`store_schema_too_new`).
- Non si usa nessun ORM e nessuna colonna con tipi che `sqlc` non sa leggere.

### 5.2 Schema normativo

I nomi seguenti sono quelli da usare nelle migrazioni. Campi `NOT NULL` salvo indicazione `?`. Il passo S3 crea l'intero schema con migrazioni numerate (`migrations/0000N_nome.sql`, l'FTS5 in un file a parte); i passi successivi aggiungono solo le query (`sql/`) e, se scoprono un errore, correggono o aggiungono migrazioni secondo I13.

```text
meta
  key text PK
  value text                           # 'ffmpeg_version', 'collate_version'

users
  id text PK                           # UUIDv7
  username text UNIQUE                 # CHECK(username = lower(username)), 3..32 caratteri [a-z0-9._-], inizia con [a-z0-9]
  password_hash text                   # PHC argon2id
  role text                            # CHECK IN ('admin','user')
  disabled integer DEFAULT 0
  created_at integer
  password_changed_at integer

sessions
  id text PK                           # UUIDv7: maniglia pubblica per elenco e revoca
  token_hash text UNIQUE               # SHA-256 esadecimale del token
  user_id text FK users ON DELETE CASCADE
  kind text                            # CHECK IN ('cookie','token')
  device_name text?                    # al più 100 caratteri
  created_at integer
  last_used_at integer
  expires_at integer

artists
  seq integer PK
  id text UNIQUE                       # UUIDv5(namespace fisso, chiave di identità del nome)
  name text
  sort_key blob

albums
  seq integer PK
  id text UNIQUE                       # album_id della ricevuta di MusicLib
  artist_id text FK artists(id)
  artist_key blob                      # copia di artists.sort_key (ordinamento senza join)
  title text
  title_key blob
  year integer?                        # CHECK BETWEEN 1 AND 9999
  year_key integer                     # COALESCE(year, 10000)
  genre text?
  compilation integer
  rel_path text                        # "Artista/Album" come su disco, relativo a library/
  album_revision integer
  render_version text
  receipt_hash text
  cover_rel text?                      # 'cover.jpg' | 'cover.png'
  cover_sha256 text?
  cover_mime text?                     # 'image/jpeg' | 'image/png'
  cover_size integer?                  # dimensione del file di cover (guardia del §9.2)
  cover_mtime_ns integer?
  track_count integer                  # tracce disponibili
  duration_ms integer                  # somma delle durate note delle tracce disponibili
  available integer
  first_seen_at integer
  updated_at integer

tracks
  seq integer PK
  id text UNIQUE                       # UUIDv7, mai cambia (I3)
  album_id text FK albums(id)
  fingerprint text                     # SHA-256 esadecimale dei pacchetti audio compressi
  fp_version text                      # versione di ffmpeg che l'ha calcolata
  occurrence integer DEFAULT 1         # >= 1; UNIQUE (album_id, fingerprint, occurrence)
  disc integer                         # 1..99
  no integer                           # 1..999
  title text
  artist text
  genre text?
  rel_path text                        # relativo alla cartella dell'album: "Disc 1/01 - X.flac"
  file_size integer
  file_mtime_ns integer
  file_sha256 text                     # dalla ricevuta
  codec text                           # CHECK IN ('flac','mp3','aac','alac')
  sample_rate integer
  channels integer
  bit_depth integer?
  bitrate integer?
  duration_ms integer?
  lyrics_rel text?                     # "01 - X.lrc" relativo alla cartella dell'album
  lyrics_sha256 text?
  rg_track_gain real?  rg_track_peak real?  rg_album_gain real?  rg_album_peak real?
  available integer
  updated_at integer

favorites
  user_id text FK users ON DELETE CASCADE
  track_id text FK tracks(id)          # RESTRICT: le tracce non si cancellano
  created_at integer
  PRIMARY KEY (user_id, track_id)

playlists
  id text PK                           # UUIDv7
  user_id text FK users ON DELETE CASCADE
  name text                            # 1..200 caratteri
  description text DEFAULT ''          # al più 2000 caratteri
  revision integer                     # > 0; aumenta a ogni modifica, anche degli elementi
  created_at integer
  updated_at integer

playlist_items
  id text PK                           # UUIDv7: maniglia dell'elemento (la stessa traccia può comparire due volte)
  playlist_id text FK playlists ON DELETE CASCADE
  track_id text FK tracks(id)
  position integer                     # >= 0, NON unico (T5): densa 0..n-1 dopo ogni modifica
  added_at integer
```

Indici obbligatori (verificati con `EXPLAIN QUERY PLAN` nel passo S24): `albums(available, title_key, id)`, `albums(available, artist_key, year_key, title_key, id)`, `albums(available, year_key, title_key, id)`, `albums(available, first_seen_at, id)`, `albums(artist_id, available)`, `tracks(album_id, disc, no)`, `tracks(album_id, fingerprint, occurrence)` (è l'unico vincolo), `favorites(user_id, created_at, track_id)`, `playlist_items(playlist_id, position, id)`, `sessions(user_id)`, `sessions(expires_at)`.

FTS5: tre tabelle virtuali, `search_artists`, `search_albums`, `search_tracks`, con `rowid = seq` della tabella madre (§10.1). Le righe non disponibili non vi compaiono.

Limiti: playlist per utente ≤ 500; elementi per playlist ≤ 10.000; `track_ids` per richiesta ≤ 1.000; nome utente 3–32; password 12–1024 byte senza caratteri di controllo.

### 5.3 Come si ricava un album dalle tracce

Ogni traccia ha i tag completi (§4.3). Per l'album:

- **Titolo:** il tag `ALBUM` più frequente (parità: quello della traccia con `(disc, no)` minore).
- **Artista:** il tag `ALBUMARTIST` più frequente; se mancano, `ARTIST` più frequente; se mancano, `Unknown Artist`. Il nome si usa così com'è (NFC, spazi esterni tolti).
- **Anno:** dal tag `DATE`, le prime quattro cifre; il valore valido più frequente (parità: il minore); altrimenti `NULL`.
- **Genere:** il genere non vuoto più frequente (parità: ordine lessicografico); altrimenti `NULL`. I generi delle singole tracce restano sulle tracce.
- **Compilation:** vero se almeno una traccia lo dichiara.
- **Cover:** `cover_rel` se la ricevuta elenca `cover.jpg` o `cover.png`.

Una traccia a cui manca un tag non impedisce l'album: titolo dal nome del file senza prefisso `NN - `, artista dell'album, numeri da `disc` della cartella e da `NN` del nome; si registra un avviso `tags_incomplete`.

### 5.4 Identità delle tracce

**Problema.** Preferiti e playlist sono riferimenti a tracce. Ogni modifica in MusicLib cambia percorso, numero, titolo e lo SHA-256 del file. Restano stabili solo l'`album_id` e l'audio.

**Soluzione.** L'ID di una traccia (`tracks.id`, UUIDv7) è assegnato da Vibrance la prima volta che la vede e non cambia mai. Per ritrovare la stessa traccia dopo una modifica si usa l'**impronta**:

```text
fingerprint = SHA-256 dei pacchetti audio compressi del primo stream audio
            = ffmpeg -map 0:a:0 -c copy -f hash -hash sha256 -      (il valore esadecimale)
```

I pacchetti compressi non cambiano quando MusicLib riscrive i tag (la garanzia sta nella verifica che MusicLib fa a ogni render: l'audio decodificato prima e dopo i tag deve coincidere) e non dipendono dalla CPU né da un decoder in virgola mobile (al contrario del digest PCM di MusicLib, che è confrontabile solo sullo stesso binario e sulla stessa macchina). Costa una lettura del file, non una decodifica. **[S1]** verifica che l'impronta sia identica prima e dopo ogni tipo di modifica, per i quattro codec.

**Regole.**

- L'impronta vale **dentro un album**: la chiave naturale è `(album_id, fingerprint, occurrence)`. La stessa registrazione in due album sono due tracce diverse.
- `occurrence` distingue due file con lo stesso audio nello stesso album (MusicLib lo permette: due tracce possono condividere l'originale).
- **Le righe non si cancellano.** Una traccia che sparisce diventa `available = 0` e conserva gli ultimi metadati noti (titolo, artista, durata, percorso) per mostrarsi in grigio in playlist e preferiti. Se torna (cestino e ripristino), si riabbina e torna disponibile con lo stesso ID.
- L'impronta è legata alla versione di ffmpeg (`fp_version`). Se la versione cambia, il server ricalcola in background le impronte dei file correnti **sulla stessa riga** (§6.6): l'identità non dipende dall'algoritmo.

**Riconciliazione** (funzione pura in `internal/library`, il *planner*). Ingresso: le righe `old` dell'album (disponibili o no) e i file `new` della nuova ricevuta, ciascuno con `rel_path`, `file_sha256`, `disc`, `no`, `duration_ms`, `codec` e, quando richiesta, l'impronta. Le fasi sono in ordine; ciascuna considera solo ciò che le precedenti non hanno abbinato:

1. **F1 — contenuto identico.** `new.file_sha256 == old.file_sha256`. Se più righe hanno lo stesso sha (non dovrebbe accadere), si abbinano in ordine `(disc, no, rel_path)`. Non serve l'impronta.
2. **F2 — impronta.** Per i `new` rimasti, stesso `fingerprint` di un `old` rimasto con `fp_version` **uguale a quella corrente**. Dentro lo stesso gruppo di impronta: prima si abbinano le coppie con `(disc, no)` uguali, poi il resto per ordine crescente di `(disc, no)` dei `new` e di `occurrence` degli `old`.
3. **F3 — abbinamento debole**, solo per gli `old` con `fp_version` **diversa** da quella corrente (impronta non confrontabile): abbina se `disc`, `no`, `codec` e `duration_ms` (entrambi non nulli) sono **tutti** uguali. Se l'`old` ha la versione corrente ma un'impronta diversa, l'audio in quello slot è davvero cambiato: non si abbina.
4. **F4 — nuove.** I `new` senza abbinamento diventano righe nuove (UUIDv7) con `occurrence` = il più piccolo intero positivo non usato, per quell'impronta, da nessuna riga dell'album (disponibile o no) né da un inserimento precedente dello stesso piano.
5. **F5 — scomparse.** Gli `old` disponibili senza abbinamento diventano `available = 0`. Gli `old` già non disponibili restano come sono. Un `old` non disponibile abbinato torna disponibile.

Le righe abbinate aggiornano tutti i campi mutabili (titolo, artista, numeri, percorso, sha, dimensione, mtime, durata, tag) dal nuovo file; `fingerprint` e `fp_version` si aggiornano solo se il file è stato riesaminato (F2, F3). Il planner espone due funzioni: `NeedFingerprint(old, new) []int` (quali `new` non sono risolti da F1 e ne hanno bisogno) e `Reconcile(old, new) Plan`. Se `Reconcile` riceve un `new` senza impronta che non è risolto da F1, restituisce errore (è un bug del chiamante).

**Perché non altro.** Un ID derivato (UUIDv5 di album+impronta) renderebbe l'identità dipendente dall'algoritmo di impronta. Usare percorso, numero o titolo è ciò che fanno i server che perdono i preferiti quando si sposta un file (Jellyfin) o che hanno dovuto introdurre ID persistenti basati su metadati (Navidrome ≥ 0.55): qui l'audio, che è la sola cosa che non cambia, fa da chiave.

**Identità degli album:** l'`album_id` di MusicLib. **Identità degli artisti:** `UUIDv5(namespace fisso, chiave di identità del nome)`, dove la chiave è il nome con NFC, spazi esterni tolti, spazi interni compressi e `casefold` (`cases.Fold()` di `x/text`; si applica **una volta** al tag; MusicLib documenta un difetto di idempotenza di `cases.Fold` su lettere cherokee: vedi `.ref/musiclib/internal/names` prima di usarlo). Un artista rinominato in MusicLib ha un ID nuovo: nella v0.1 nessun dato degli utenti punta agli artisti, quindi è innocuo.

### 5.5 Chiavi di ordinamento

`sort_key`, `title_key`, `artist_key` sono `BLOB` calcolati con `golang.org/x/text/collate` (`language.Und`, opzione `collate.Numeric`, così «Traccia 2» precede «Traccia 10») sulla stringa in NFC. Si confrontano con memcmp (`ORDER BY` su `BLOB`). In `meta` si salva `collate_version` (la versione di `x/text` compilata): se cambia, l'avvio ricalcola tutte le chiavi prima di servire (T26). Non si ignorano articoli («The …») nella v0.1.

---

## 6. Indicizzazione

### 6.1 Panoramica

Lo **scanner** mantiene l'indice allineato a `library/`. Un ciclo si avvia: all'avvio del server (dopo migrazioni e verifica degli strumenti), a intervalli (`VIBRANCE_SCAN_INTERVAL`, default 5 minuti, minimo 30 s), su richiesta dell'admin (`POST /api/v1/admin/library/scan`) e quando un endpoint audio trova un file sostituito (§9.1). I trigger si coalescono: un solo ciclo attivo e al più uno in attesa. Nessun processo parallelo di scansione.

Stati (`state` dello stato della libreria): `idle`, `scanning`, `maintenance` (esiste `/musiclib/.maintenance`), `unavailable` (manca `.musiclib-store` o `library/` non si apre).

```text
Ciclo(ctx):
  P0 precondizioni   se maintenance/unavailable: aggiorna lo stato, esci senza toccare l'indice
  P1 scoperta        library/ → cartelle artista → cartelle album; per ogni album leggi .musiclib.json
  P2 scelta          per ogni album_id scegli una sola cartella (§6.2)
  P3 piano           confronta con l'indice: nuovi o cambiati → lista di lavoro; invariati → saltati
  P4 indicizzazione  pool di worker: indicizza un album (§6.3) e lo committa in una transazione
  P5 assenti         album disponibili non più visti → available = 0 (con le loro tracce), §6.4
  P6 chiusura        stato, contatori, PRAGMA optimize se è cambiato molto
```

### 6.2 Scoperta e scelta

- Si elencano (con `os.Root`) le cartelle di primo livello (artisti) e, dentro, le cartelle album. Si ignorano i file e le cartelle che iniziano con `.`. Non si scende oltre: il resto lo descrive la ricevuta.
- Un errore di elenco di una cartella artista si registra come problema `listing_failed` e **protegge** gli album sotto quella cartella dal passo P5 (non sono dichiarati assenti). Un errore sull'elenco della radice interrompe il ciclo senza toccare nulla.
- Una cartella album senza `.musiclib.json` è ignorata con problema `receipt_missing` (una cartella estranea in `library/`).
- Per più cartelle con lo stesso `album_id` (finestra di rinomina, §4.1) si sceglie quella con `album_revision` più alta; a parità, quella già registrata nell'indice; a parità, l'ordine lessicografico del percorso. Le altre non producono problemi.
- Gli album che hanno fallito l'indicizzazione con una certa `receipt_hash` non si riprovano finché la ricevuta non cambia o il processo non riparte (evita di rilanciare ffprobe ogni 5 minuti su un album rotto); restano elencati tra i problemi.

### 6.3 Indicizzare un album

Ingresso: la cartella scelta, la ricevuta, il suo `receipt_hash`. Nell'ordine:

1. **Classificazione dei file della ricevuta** (funzione pura):
   - *audio*: estensione `.flac`, `.mp3`, `.m4a`, non sotto `Extras/`, nella radice dell'album o in **una sola** cartella `Disc N/` (`N` intero positivo);
   - *cover*: esattamente `cover.jpg` o `cover.png` alla radice;
   - *testo*: `.lrc` il cui nome base coincide (confronto NFC) con quello di una traccia nella stessa cartella;
   - tutto il resto si ignora. Più di 20.000 file nella ricevuta: problema `receipt_too_large`.
2. **Verifica di presenza:** per ogni file audio, cover e testo, `Lstat` (via `os.Root`): deve esistere, non essere un link simbolico, avere la dimensione della ricevuta. Altrimenti problema `file_missing` o `file_size_mismatch` e l'album non si indicizza in questo ciclo (se è transitorio, il ciclo dopo lo risolve).
3. **Righe precedenti:** si leggono le righe `tracks` dell'album (transazione di lettura).
4. **Dati dei file.** `NeedFingerprint` indica i file non risolti da F1. Per **i file risolti da F1** si riusano i metadati già in indice (nessun processo). Per gli altri: `ffprobe` (tag, durata, codec, frequenza, canali, profondità, bitrate) e impronta (`ffmpeg -c copy -f hash`). I processi girano nel pool (capacità `VIBRANCE_WORKERS`) con timeout di 30 s (probe) e 10 minuti (impronta).
5. **Cover:** se `cover_sha256` è cambiato, si valida l'intestazione con `image.DecodeConfig` (formato JPEG/PNG coerente con l'estensione, al più 40 megapixel). Se non valida: avviso `cover_invalid` e album senza cover.
6. **Controllo di coerenza.** Si **rilegge** `.musiclib.json` e se il suo hash è diverso da quello iniziale si abbandona l'album (`album_changed_during_scan`: non è un errore, sarà ripreso al ciclo dopo). Questo chiude la finestra fra lettura dei file e sostituzione dell'album (T10).
7. **Piano:** si derivano i dati dell'album (§5.3) e si chiama `Reconcile`.
8. **Commit:** **una** transazione di scrittura (`BEGIN IMMEDIATE`): upsert dell'artista, upsert dell'album (con `available = 1`), applicazione del piano alle tracce, ricalcolo di `track_count` e `duration_ms`, aggiornamento delle tabelle FTS. Niente I/O dentro.
9. Dopo il commit: se la cover è nuova, si accoda il calcolo delle miniature in background (§9.2).

Gli errori dei passi 1, 2, 4 e 7 (`receipt_*`, `file_*`, `probe_failed`, `fingerprint_failed`) mettono l'**intero** album in stato di problema: non si fa mai un commit parziale.

### 6.4 Assenti

Alla fine del ciclo, gli album con `available = 1` il cui `album_id` non è stato visto **e** che non sono protetti da un errore di elenco (§6.2) si marcano `available = 0` insieme alle loro tracce, e si tolgono dalle tabelle FTS. Il resto dell'indice resta. Se i file tornano, il ciclo successivo li riabbina (F1 per sha, poi F2) e tutto ricompare con gli stessi ID. **Non c'è una soglia di sicurezza** per sparizioni di massa: il danno di un falso allarme è zero, perché nulla si cancella.

### 6.5 Stato e problemi

`GET /api/v1/admin/library` espone (§8.7): stato, inizio e fine dell'ultimo ciclo, esito, contatori degli album (disponibili, non disponibili, ignorati), avanzamento (album trovati, in coda, indicizzati), presenza del marcatore di manutenzione e l'elenco dei problemi (al più 200): `{rel_path, code, message}`. Problemi (errori) e avvisi (non bloccano): `receipt_missing`, `receipt_invalid`, `receipt_schema_unsupported`, `receipt_too_large`, `file_missing`, `file_size_mismatch`, `probe_failed`, `fingerprint_failed`, `listing_failed` (problemi); `cover_invalid`, `tags_incomplete`, `lyrics_unreadable` (avvisi). I problemi vivono in memoria e si ricostruiscono a ogni ciclo.

### 6.6 Cambio della versione di ffmpeg o delle chiavi

All'avvio, se `meta.ffmpeg_version` è diversa dalla versione corrente, un lavoro in background (a bassa concorrenza, interrompibile e ripetibile) **ricalcola l'impronta** di ogni traccia disponibile dal suo file corrente e aggiorna `fingerprint` e `fp_version` **sulla stessa riga**, saltando le tracce il cui `(size, mtime)` non coincide più con la riga (l'album è stato modificato: lo scanner se ne occupa). Alla fine si aggiorna `meta.ffmpeg_version`. Se `meta.collate_version` è diversa, si ricalcolano le chiavi di ordinamento prima di servire (§5.5).

### 6.7 Costi attesi

Ciclo senza modifiche: elenco di due livelli di cartelle più la lettura di una ricevuta (pochi KB) per album. Scansione iniziale: per ogni traccia un `ffprobe` e una lettura del file (la lettura del disco domina: circa un'ora per 500 GB su un disco meccanico). Un cambio di `render_version` di MusicLib riscrive gli album ma, dove i byte dei file non cambiano, F1 evita ogni processo. Una modifica di titolo ricalcola solo la traccia modificata (se gli altri file sono identici a livello di byte), altrimenti tutto l'album. **Misurare prima di ottimizzare** (S24); una possibile ottimizzazione (firma `(inode, mtime)` della cartella album per saltare la lettura della ricevuta) è rimandata.

### 6.8 Ciò che lo scanner non fa

Non segue link simbolici; non legge fuori da `library/`; non cancella righe; non tocca mai i file; non calcola nulla durante una transazione di scrittura; non dipende da `fsnotify` (eventuale futuro: solo come suggerimento per anticipare un ciclo).

---

## 7. Autenticazione e utenti

### 7.1 Modello

Utenti con ruolo `admin` o `user`. Niente registrazione autonoma: l'admin crea gli account. Nome utente ASCII minuscolo (`^[a-z0-9][a-z0-9._-]{2,31}$`); il login confronta il nome in minuscolo. Password di 12–1024 byte senza caratteri di controllo.

### 7.2 Password

Hash **argon2id** in formato **PHC** (`$argon2id$v=19$m=65536,t=3,p=1$salt$hash`), salt casuale di 16 byte, hash di 32 byte; i parametri stanno nella stringa, così si possono cambiare in futuro e verificare i vecchi. Codifica e decodifica PHC sono codice nostro (poche decine di righe, con test su vettori noti). Nei test i parametri di costo sono ridotti tramite una variabile di pacchetto; un solo test verifica i parametri di produzione.

**Verifica una alla volta.** Un semaforo di capacità 1 protegge ogni calcolo argon2id (login e cambio password): limita la memoria usata e rende inutile tentare molte password in parallelo. Un tentativo **rifiutato** trattiene il turno per 1 secondo. Per un nome utente inesistente si verifica comunque contro un hash fittizio (stesso costo e stesso ritardo): la risposta non rivela se l'utente esiste (`invalid_credentials`). Conseguenza nota e accettata, come in MusicLib: chi invia tentativi sbagliati in LAN può rallentare i login altrui. (T15)

### 7.3 Sessioni e token

- Un token è `vb_` + 32 byte casuali (`crypto/rand`) in base64url senza padding. Nel database si salva solo `SHA-256(token)` (I5).
- **Cookie** (`POST /auth/login`): `vibrance_session`, `HttpOnly`, `SameSite=Strict`, `Path=/`, `Secure` se e solo se l'origine pubblica è `https`. Durata 30 giorni. **Token** (`POST /auth/tokens`, per app mobile): in `Authorization: Bearer`, durata 90 giorni. Stessa tabella `sessions`, campo `kind`. Il nome del cookie è diverso da quello di MusicLib (`musiclib_session`) per non confliggere sullo stesso host. (T14)
- **Rinnovo:** se alla richiesta è trascorsa più della metà della durata, `expires_at` torna alla durata piena. `last_used_at` si aggiorna al più ogni 10 minuti. Una sessione scaduta o revocata dà `401 login_required`. (T16)
- **Revoca:** `POST /auth/logout` (quella corrente), `DELETE /me/sessions/{id}`, cambio password (tutte le altre), reset password o disattivazione o eliminazione da parte dell'admin (tutte quelle dell'utente). Pulizia delle sessioni scadute ogni ora e all'avvio.
- Bearer e cookie nella stessa richiesta: vince il bearer.

### 7.4 Primo admin e recupero

- All'avvio, se la tabella `users` è vuota, si crea l'admin da `VIBRANCE_ADMIN_USERNAME` (default `admin`) e `VIBRANCE_ADMIN_PASSWORD`. Senza password valida in quel caso il server **non parte** (`admin_password_missing` / `admin_password_invalid`, uscita 2). Se esiste già un utente, le due variabili si ignorano.
- Sottocomandi (funzionano anche con il server acceso, perché le sessioni stanno nel database e la revoca è immediata):

  ```text
  vibrance user create --username U --role admin|user --password-stdin
  vibrance user reset-password --username U --password-stdin     # revoca le sue sessioni
  vibrance user list
  ```

  La password arriva **solo** da standard input, mai da riga di comando (I5).

### 7.5 Regole dell'amministrazione

- L'ultimo admin **abilitato** non si può eliminare, retrocedere o disattivare (`last_admin`, 409). Un admin non può eliminare né disattivare sé stesso; può cambiare la propria password da `/me/password`.
- L'eliminazione di un utente elimina in cascata sessioni, preferiti, playlist e i loro elementi. Un utente disattivato non può accedere e perde le sessioni.
- Nome utente già usato: `409 username_taken`.
- L'admin **non** vede playlist o preferiti degli altri (privacy); non esiste un'API per farlo.

### 7.6 Confine del browser

`VIBRANCE_PUBLIC_ORIGIN` è obbligatorio (stesse regole di forma di `PUBLIC_ORIGIN` di MusicLib: `scheme://host[:port]`, minuscolo, senza barra finale, senza porta di default). `Host` deve coincidere (`421 host_not_allowed`); `Origin`, se presente, deve essere identico (`403 origin_not_allowed`); ogni richiesta che non è GET o HEAD richiede `X-Vibrance-Request: 1` (`403 request_header_required`), login compreso. Nessun header CORS, mai. Tutte le risposte: `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`; le risposte JSON anche `Cache-Control: private, no-store` e `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`. Gli header `X-Forwarded-*` si **ignorano** (come in MusicLib): per i log l'indirizzo del client è quello del proxy. Gli endpoint di salute sono fuori dal controllo di `Host`.

---
## 8. API

### 8.1 Convenzioni

- Base: `/api/v1`. JSON UTF-8, chiavi in `snake_case`, ID UUID in stringa, durate in millisecondi, date in RFC 3339 UTC con `Z`. Dentro la v1 si fanno solo aggiunte compatibili.
- **Autenticazione:** due schemi nella specifica, `cookieAuth` (cookie `vibrance_session`) e `bearerAuth` (`Authorization: Bearer vb_…`), sulla stessa tabella di sessioni (§7.3). Senza sessione valida ogni endpoint risponde `401 login_required`, **tranne** quelli marcati *pubblico* nel §8.3.
- **Errori:** `{"code": "...", "message": "...", "details": {}}` con `code` stabile (tabella del §8.4). Niente percorsi assoluti, SQL o stderr nelle risposte. `400` per richiesta malformata (JSON, parametri, enum, cursore), `422` per contenuto semanticamente non valido, `409` per conflitti, `404` per risorse inesistenti **o di un altro utente**, `503` per indisponibilità temporanea.
- **Corpi JSON:** `Content-Type: application/json`, al più 1 MiB, **chiavi sconosciute e duplicate rifiutate**, un solo valore JSON per corpo. Ogni schema di richiesta ha `additionalProperties: false`. I campi obbligatori sono quelli elencati nella specifica; `null` è ammesso solo dove indicato.
- **Liste:** `?limit=` (default 50, massimo 200) e `?after=` (cursore opaco preso dal campo `next` della pagina precedente). Risposta: `{"<nome>": [...], "next": "<cursore>"|null}`. Cursore non valido: `400 invalid_cursor`. Niente `OFFSET`: paginazione a chiave (§8.5).
- **ETag e `If-Match`** (solo playlist, §8.6): ETag forte `"playlist:<id>:<revision>"` nell'header e nel campo `etag` del corpo (se un proxy comprime o indebolisce l'header, il corpo resta affidabile); nel confronto si ignora un eventuale prefisso `W/`. Senza precondizione dove è obbligatoria: `428 precondition_required`; revisione vecchia: `412 precondition_failed`; il controllo avviene nella stessa transazione della modifica.
- **Caching:** JSON con `Cache-Control: private, no-store`. Audio: `private, no-cache` con `ETag`. Cover: §9.2.
- **Header di risposta:** `X-Request-Id` (UUIDv7 generato per richiesta, anche negli errori).
- **Metodi:** `HEAD` supportato dove c'è `GET` su audio, cover e testi; percorso inesistente `404 not_found`; metodo non ammesso `405 method_not_allowed` con `Allow`.
- **Nessuna rotta di scrittura accetta dati da cui costruire un percorso** (I2): gli ID sono UUID e si risolvono solo nel database.

### 8.2 Schemi principali

Forma di riferimento; la specifica OpenAPI è la versione completa con esempi. `T|null` significa campo sempre presente con valore nullo ammesso.

```jsonc
Error        { "code": "track_unavailable", "message": "…", "details": {} }
User         { "id", "username", "role": "admin|user", "disabled": false, "created_at" }
Session      { "id", "kind": "cookie|token", "device_name": "…"|null,
               "created_at", "last_used_at", "expires_at", "current": true }
Cover        { "hash": "<sha256>", "url": "/api/v1/albums/<id>/cover?v=<sha256>" }
ArtistRef    { "id", "name" }
ArtistSummary{ "id", "name", "album_count" }
AlbumRef     { "id", "title", "artist": ArtistRef, "year": 1959|null, "cover": Cover|null }
AlbumSummary { "id", "title", "artist": ArtistRef, "year"|null, "genre"|null, "compilation",
               "track_count", "duration_ms", "cover": Cover|null, "added_at" }
AlbumDetail  { ...AlbumSummary, "disc_count", "tracks": [Track] }        // ordinate per (disc, number)
Track        { "id", "title", "artist", "album": AlbumRef, "disc", "number",
               "duration_ms"|null, "genre"|null,
               "format": { "codec": "flac|mp3|aac|alac", "sample_rate", "channels",
                           "bit_depth"|null, "bitrate"|null, "size" },
               "has_lyrics",
               "replay_gain": { "track_gain_db"|null, "track_peak"|null,
                                "album_gain_db"|null, "album_peak"|null } | null,
               "available": true, "favorite": false }
Playlist     { "id", "name", "description", "item_count", "duration_ms", "revision", "etag",
               "created_at", "updated_at" }
PlaylistItem { "id", "position", "added_at", "track": Track }
LibraryStatus{ "state": "idle|scanning|maintenance|unavailable",
               "last_scan": { "started_at", "finished_at"|null, "ok", "error"|null } | null,
               "albums": { "available", "unavailable" }, "tracks": { "available", "unavailable" },
               "progress": { "discovered", "pending", "indexed" } | null,
               "musiclib_maintenance": false,
               "problems": [ { "rel_path", "code", "message" } ] }     // al più 200
ServerInfo   { "name": "Vibrance", "version", "api_version": 1 }
```

Note: `Track.album` è sempre valorizzato (anche per tracce non disponibili, con gli ultimi dati noti). `favorite` è calcolato per l'utente della richiesta. `duration_ms` e `item_count` delle playlist contano solo le tracce disponibili. `Track.artist` è l'artista della traccia (l'artista dell'album sta in `album.artist`). Gli album non disponibili non compaiono nelle liste e `GET /albums/{id}` risponde 404 `album_not_found`; le tracce non disponibili si leggono con `GET /tracks/{id}` (200, `available: false`) ma il loro audio risponde 404.

### 8.3 Endpoint

Legenda accesso: **pubblico** (nessuna sessione), **utente** (qualunque utente abilitato), **admin**. `operationId` è il nome da usare nella specifica.

| Metodo e percorso | operationId | Accesso | Richiesta | Successo | Errori principali |
|---|---|---|---|---|---|
| `GET /server` | `getServerInfo` | pubblico | — | 200 `ServerInfo` | — |
| `POST /auth/login` | `login` | pubblico | `{username, password, device_name?}` | 200 `{user, session}` + cookie | 401 `invalid_credentials` |
| `POST /auth/tokens` | `createToken` | pubblico | `{username, password, device_name}` | 201 `{token, user, session}` | 401 `invalid_credentials` |
| `POST /auth/logout` | `logout` | utente | — | 204 (cookie cancellato) | — |
| `GET /me` | `getMe` | utente | — | 200 `User` | — |
| `PUT /me/password` | `changePassword` | utente | `{current_password, new_password}` | 204 | 422 `current_password_invalid`, `password_invalid` |
| `GET /me/sessions` | `listSessions` | utente | — | 200 `{sessions: [Session]}` | — |
| `DELETE /me/sessions/{id}` | `revokeSession` | utente | — | 204 | 404 `session_not_found` |
| `GET /admin/users` | `listUsers` | admin | — | 200 `{users: [User]}` | 403 `forbidden` |
| `POST /admin/users` | `createUser` | admin | `{username, password, role}` | 201 `User` | 409 `username_taken`; 422 `username_invalid`, `password_invalid` |
| `GET /admin/users/{id}` | `getUser` | admin | — | 200 `User` | 404 `user_not_found` |
| `PUT /admin/users/{id}` | `updateUser` | admin | `{role, disabled}` | 200 `User` | 409 `last_admin`, `cannot_modify_self` |
| `DELETE /admin/users/{id}` | `deleteUser` | admin | — | 204 | 409 `last_admin`, `cannot_modify_self` |
| `PUT /admin/users/{id}/password` | `resetUserPassword` | admin | `{password}` | 204 | 422 `password_invalid` |
| `GET /admin/library` | `getLibraryStatus` | admin | — | 200 `LibraryStatus` | — |
| `POST /admin/library/scan` | `scanLibrary` | admin | — | 202 `LibraryStatus` | — |
| `GET /artists` | `listArtists` | utente | `limit, after` | 200 `{artists: [ArtistSummary], next}` | 400 `invalid_cursor` |
| `GET /artists/{id}` | `getArtist` | utente | — | 200 `{id, name, albums: [AlbumSummary]}` | 404 `artist_not_found` |
| `GET /albums` | `listAlbums` | utente | `sort, order, artist, limit, after` | 200 `{albums: [AlbumSummary], next}` | 400 `invalid_cursor` |
| `GET /albums/{id}` | `getAlbum` | utente | — | 200 `AlbumDetail` | 404 `album_not_found` |
| `GET /tracks/{id}` | `getTrack` | utente | — | 200 `Track` | 404 `track_not_found` |
| `GET /search` | `search` | utente | `q, types, limit` | 200 `{artists, albums, tracks}` | 400 `invalid_request` |
| `GET /tracks/{id}/audio` | `getTrackAudio` | utente | `profile?`; `Range` | 200/206 audio; 304 | 404 `track_unavailable`; 400 `unsupported_profile`; 503 `library_changing` |
| `GET /albums/{id}/cover` | `getAlbumCover` | utente | `size?, v?` | 200 immagine; 304 | 404 `cover_not_found`; 503 `library_changing` |
| `GET /tracks/{id}/lyrics` | `getTrackLyrics` | utente | — | 200 `{synced, lines}` | 404 `lyrics_not_found` |
| `GET /me/favorites/tracks` | `listFavoriteTracks` | utente | `limit, after` | 200 `{favorites: [{favorited_at, track}], next}` | 400 `invalid_cursor` |
| `PUT /me/favorites/tracks/{id}` | `addFavoriteTrack` | utente | — | 204 (idempotente) | 404 `track_not_found` |
| `DELETE /me/favorites/tracks/{id}` | `removeFavoriteTrack` | utente | — | 204 (idempotente) | 404 `track_not_found` |
| `GET /playlists` | `listPlaylists` | utente | — | 200 `{playlists: [Playlist]}` | — |
| `POST /playlists` | `createPlaylist` | utente | `{name, description}` | 201 `Playlist` + ETag | 422 `too_many_playlists`, `invalid_request` |
| `GET /playlists/{id}` | `getPlaylist` | utente | — | 200 `Playlist` + ETag | 404 `playlist_not_found` |
| `PUT /playlists/{id}` | `updatePlaylist` | utente | `{name, description}` + `If-Match?` | 200 `Playlist` | 404; 412 |
| `DELETE /playlists/{id}` | `deletePlaylist` | utente | `If-Match?` | 204 | 404; 412 |
| `GET /playlists/{id}/items` | `listPlaylistItems` | utente | `limit, after` | 200 `{items: [PlaylistItem], next}` + ETag | 404; 400 `invalid_cursor` |
| `POST /playlists/{id}/items` | `addPlaylistItems` | utente | `{track_ids, position\|null}` + `If-Match` (obbligatorio se `position` non è null) | 200 `{playlist, added: [{item_id, track_id, position}]}` | 404; 412; 422 `too_many_items`, `unknown_track`, `track_unavailable`, `invalid_position`; 428 |
| `DELETE /playlists/{id}/items/{item_id}` | `removePlaylistItem` | utente | `If-Match?` | 200 `Playlist` | 404 `item_not_found`; 412 |
| `POST /playlists/{id}/items/{item_id}/move` | `movePlaylistItem` | utente | `{position}` + `If-Match` (obbligatorio) | 200 `Playlist` | 404; 412; 422 `invalid_position`; 428 |

Percorsi fuori dalla specifica OpenAPI (infrastruttura, pubblici): `GET /health/live`, `GET /health/ready`, `GET /api/openapi.yaml`, `GET /api/docs`, `GET /` (reindirizza a `/api/docs`, D20). Il test della matrice di autorizzazione (§12.4) li elenca esplicitamente.

### 8.4 Codici di errore

| HTTP | `code` | Quando |
|---|---|---|
| 400 | `invalid_request` | JSON malformato, chiavi sconosciute o duplicate, parametro o enum non valido |
| 400 | `invalid_cursor` | `after` non valido o di un altro ordinamento |
| 400 | `unsupported_profile` | `profile` diverso da assente o `original` |
| 401 | `login_required` | nessuna sessione valida |
| 401 | `invalid_credentials` | nome o password errati, utente disattivato incluso |
| 403 | `forbidden` | utente non admin su endpoint admin |
| 403 | `origin_not_allowed` | `Origin` diverso da `VIBRANCE_PUBLIC_ORIGIN` |
| 403 | `request_header_required` | manca `X-Vibrance-Request: 1` |
| 404 | `not_found` | percorso inesistente |
| 404 | `*_not_found`, `track_unavailable`, `cover_not_found`, `lyrics_not_found` | risorsa inesistente (o di un altro utente) o non disponibile |
| 405 | `method_not_allowed` | metodo non ammesso |
| 409 | `username_taken`, `last_admin`, `cannot_modify_self` | conflitti di dominio |
| 412 | `precondition_failed` | `If-Match` non coincide con la revisione |
| 413 | `body_too_large` | corpo oltre 1 MiB |
| 421 | `host_not_allowed` | `Host` diverso da `VIBRANCE_PUBLIC_ORIGIN` |
| 422 | `username_invalid`, `password_invalid`, `current_password_invalid`, `too_many_playlists`, `too_many_items`, `unknown_track`, `track_unavailable`, `invalid_position` | contenuto non valido |
| 428 | `precondition_required` | manca `If-Match` dove è obbligatorio |
| 500 | `internal` | errore inatteso (mai con dettagli interni) |
| 503 | `not_ready`, `shutting_down` | avvio o arresto |
| 503 | `library_changing` | il file è stato sostituito da MusicLib; con `Retry-After: 5` |

Ogni codice stabile compare nella specifica (enum o descrizione) e in un test.

### 8.5 Liste e cursori

- **Album** (`GET /albums`): `sort` ∈ `title` (default), `artist`, `year`, `added`; `order` ∈ `asc` (default), `desc`; `artist=<id>` filtra. Chiavi di ordinamento complete: `title` → `(title_key, id)`; `artist` → `(artist_key, year_key, title_key, id)`; `year` → `(year_key, title_key, id)`; `added` → `(first_seen_at, id)`. Con `order=desc` tutte le chiavi si invertono. Gli album senza anno hanno `year_key = 10000` (in fondo in ordine crescente).
- **Artisti:** `(sort_key, id)`, solo quelli con almeno un album disponibile. **Preferiti:** `(created_at, track_id)` dal più recente. **Elementi di playlist:** `(position, id)`.
- **Cursore:** `base64url(JSON)` contenente ordinamento, direzione e valori dell'ultima riga; il server lo decodifica in modo rigoroso (tipo e numero dei valori, stesso `sort` e `order` della richiesta). **Non** è firmato né segreto. Un cursore non valido non causa mai un errore 500 né un'iniezione (T9).
- I cursori a chiave restano validi anche se la riga a cui puntano viene rimossa (playlist: `(position, id)`).
- Le query usano il confronto di valori di riga o una condizione equivalente e **devono** usare gli indici (verificato con `EXPLAIN QUERY PLAN` e dal test di proprietà che confronta le pagine concatenate con l'elenco completo ordinato).

### 8.6 Playlist

- Private: ogni accesso a una playlist altrui risponde `404 playlist_not_found` (anche per admin).
- Ogni modifica (metadati o elementi) aumenta `revision`, ricalcola `updated_at` e rinumera le posizioni in modo denso (`0..n-1`) **nella stessa transazione** (T5).
- `POST /playlists/{id}/items` con `position = null` aggiunge in coda e **non richiede** `If-Match`; con `position` intero (da `0` a `item_count`) inserisce *prima* dell'elemento che occupa quella posizione (`item_count` = in coda) e richiede `If-Match`. Più `track_ids` si inseriscono in blocco, nell'ordine dato. Duplicati ammessi (ogni elemento ha un proprio `id`). Tracce sconosciute: `422 unknown_track` (con gli ID in `details`); non disponibili: `422 track_unavailable`. Nessuna modifica parziale: o tutto o niente.
- `move` porta l'elemento alla posizione finale indicata (`0..item_count-1`); richiede `If-Match`.
- `If-Match` è facoltativo (ma applicato se presente) per rinomina, eliminazione della playlist e rimozione di un elemento. Questa è una deviazione consapevole dalla regola di MusicLib, motivata da D14.
- Le tracce diventate non disponibili restano nella playlist con `available: false` e gli ultimi metadati noti.

### 8.7 Stato della libreria

`GET /admin/library` descrive lo stato dello scanner (§6.5). `POST /admin/library/scan` accoda un ciclo (risponde 202 anche se ce n'è già uno attivo o in attesa) e restituisce lo stato corrente. Nessun altro endpoint modifica lo scanner.

### 8.8 Documentazione servita

Il binario incorpora `api/openapi.yaml` e una pagina di documentazione statica (Redoc o Scalar, **un solo file JavaScript vendorizzato**, versione e SHA-256 fissati nel repository): `GET /api/openapi.yaml` e `GET /api/docs`, senza autenticazione, senza CDN. La pagina `/api/docs` ha una CSP propria, più larga di quella dell'API, strettamente necessaria al suo funzionamento (T23). La specifica pubblicata ha `servers: [{url: /api/v1}]`; per la validazione a runtime si usa una copia con `servers` rimosso (T23).

---

## 9. Media: audio, cover, testi

### 9.1 Audio

`GET /tracks/{id}/audio`, nell'ordine:

1. Sessione valida (§7.3). Traccia inesistente: `404 track_not_found`. `available = 0`: `404 track_unavailable`.
2. `profile` assente o `original`; altrimenti `400 unsupported_profile` (D10).
3. Percorso = `albums.rel_path` + `/` + `tracks.rel_path`, letto **solo** dal database; si valida con `fs.ValidPath` e si apre con `os.Root.Open` (I1, I2, T11).
4. **Guardia `(size, mtime)`** (T13): `fstat` del file aperto; deve essere un file regolare con `size = tracks.file_size` e `mtime_ns = tracks.file_mtime_ns`. `ENOENT` o disaccordo significano che MusicLib ha sostituito l'album dopo l'ultima scansione: si **avvia uno scan** (coalescente), si risponde `503 library_changing` con `Retry-After: 5` e si **non** serve il file.
5. Header: `Content-Type` dal codec (`flac` → `audio/flac`, `mp3` → `audio/mpeg`, `aac` e `alac` → `audio/mp4`); `ETag: "<file_sha256>"`; `Cache-Control: private, no-cache`; **imposta il `Content-Type` prima** di chiamare `ServeContent`, altrimenti lo indovina (T12).
6. `http.ServeContent(w, r, "", time.Time{}, file)`: gestisce `Range`, `If-Range` (se l'`ETag` non coincide ignora il `Range` e manda il file intero: è ciò che evita glitch quando un album viene riscritto mentre un client ha un `Range` vecchio), `If-None-Match` e `HEAD`.
7. Il gestore disattiva la scadenza di scrittura della risposta (`http.NewResponseController(w).SetWriteDeadline(time.Time{})`): un brano dura più di qualunque `WriteTimeout` ragionevole (T12).

Nessun file viene mai letto da un percorso diverso da quello del passo 3. Niente `Content-Disposition` (non rivela nomi).

### 9.2 Cover

`GET /albums/{id}/cover?size=256|640|original&v=<hash>`; `size` default `640`.

- Album inesistente o non disponibile, o senza cover: `404` (`album_not_found` / `cover_not_found`).
- `Cache-Control`: se `v` coincide con `cover_sha256` corrente, `private, max-age=31536000, immutable`; altrimenti `private, no-cache` (URL vecchio o senza `v`: si serve comunque la cover corrente).
- `ETag`: `"<cover_sha256>-<size>"`.
- **`original`:** si serve `cover_rel` dall'album con `ServeContent` (stessa guardia `(size, mtime)` di `cover_size` e `cover_mtime_ns`; in disaccordo: `503 library_changing` e scan).
- **Miniature 256 e 640:** cache su disco `/var/lib/vibrance/thumbs/<hash[0:2]>/<hash>_<size>.jpg`. Se manca, si genera con `singleflight` sulla chiave `<hash>_<size>` e un semaforo di capacità 2 sulle decodifiche (T20): (a) si legge il file intero (al più 20 MiB; oltre: si serve l'originale), (b) si **calcola lo SHA-256 dei byte letti e si confronta con `cover_sha256`**: se diverso l'album è stato sostituito (`503 library_changing`, scan), (c) `image.DecodeConfig` (formato, al più 40 megapixel; oltre: si serve l'originale), (d) decodifica, ridimensionamento in un quadrato `size × size` **senza ingrandire** (`draw.CatmullRom`, PNG con trasparenza composto su bianco), (e) JPEG qualità 85, (f) scrittura atomica (file temporaneo nella stessa cartella e `rename`). Se la generazione fallisce per qualunque motivo: log, e si serve l'originale.
- **Riscaldamento:** dopo il commit di un album con cover nuova, un lavoro di fondo a concorrenza 1 genera 256 e 640, così le griglie sono veloci.
- Nessuna rimozione automatica della cache nella v0.1 (circa 120 KB per album): si può cancellare la cartella `thumbs/` in qualunque momento.
- **Le tracce non hanno cover propria:** ogni `Track` porta `album.cover` e il client costruisce l'URL di quell'album (D12).

### 9.3 Testi

`GET /tracks/{id}/lyrics`:

- Traccia senza `lyrics_rel`, non disponibile, o file assente/oltre 2 MiB: `404 lyrics_not_found`. Il file si apre con `os.Root` dal percorso di database e si verifica che il suo SHA-256 coincida con `lyrics_sha256` (altrimenti `503 library_changing`, scan). `ETag` = `lyrics_sha256`.
- Risposta: `{"synced": true, "lines": [{"time_ms": 12340, "text": "…"}]}`. Se il file non ha alcun timestamp: `synced: false`, `time_ms: null` su ogni riga di testo non vuota.
- **Grammatica LRC** (parser puro in `internal/lyrics`, con fuzz):
  - si toglie un eventuale BOM UTF-8; le righe terminano con `\n`, `\r\n` o `\r`;
  - tag di identificazione `[ar:…] [ti:…] [al:…] [by:…] [length:…]`: ignorati; `[offset:±ms]`: si applica (`tempo = tempo − offset`, minimo 0);
  - tag di tempo `[mm:ss]`, `[mm:ss.x]`, `[mm:ss.xx]`, `[mm:ss.xxx]` (con `mm` di qualunque lunghezza, secondi 0–59, frazione 1–3 cifre, anche `[hh:mm:ss.xx]`);
  - una riga può iniziare con **più** tag di tempo: produce una riga per ciascuno con lo stesso testo;
  - i tag di parola `<mm:ss.xx>` dentro il testo si **tolgono** (LRC «enhanced»);
  - le righe con tempo e testo vuoto si **mantengono** (pause strumentali);
  - le righe sincronizzate si ordinano per tempo (ordinamento stabile); in un file con almeno un timestamp le righe senza timestamp si scartano;
  - al più 10.000 righe (oltre: si tronca).
- I testi incorporati nei tag non si leggono nella v0.1.

### 9.4 Altri file

`Extras/` non si serve mai. Non esiste un endpoint che legga un percorso arbitrario.

---

## 10. Ricerca

### 10.1 Tabelle FTS5

```sql
CREATE VIRTUAL TABLE search_artists USING fts5(name,
  tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3');
CREATE VIRTUAL TABLE search_albums  USING fts5(title, artist,
  tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3');
CREATE VIRTUAL TABLE search_tracks  USING fts5(title, artist, album,
  tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3');
```

`rowid` è il `seq` della tabella madre (T6). Contengono solo le righe **disponibili**. Le aggiorna lo scanner **nella stessa transazione** che modifica le tabelle madri: inserimento alla comparsa, sostituzione alla modifica, cancellazione quando una riga diventa non disponibile (§6.3, §6.4). L'FTS5 sta in una migrazione separata che `sqlc` non legge (T7); le poche query FTS stanno in `internal/search`, scritte a mano (eccezione di I7), senza costruire SQL da input.

### 10.2 Query

1. `q` ha 1–100 caratteri (altrimenti `400 invalid_request`). Si **divide** in token su qualunque carattere che non sia lettera o cifra Unicode; massimo 8 token di al più 64 caratteri; il resto si scarta. Nessun token: risultati vuoti (non un errore).
2. Ogni token diventa `"token"*` (racchiuso tra virgolette, con `*` per il prefisso); i token sono in AND implicito. **Mai** si passa testo dell'utente come sintassi di `MATCH` (T18): poiché i token hanno solo lettere e cifre, la virgoletta non può comparire.
3. Ordinamento: `bm25(tabella, pesi)` con peso maggiore per il campo principale (titolo/nome), poi `rowid` per determinismo. `types` (default `artist,album,track`) sceglie le tabelle; `limit` (default 10, massimo 50) vale **per tipo**.
4. Gli ID trovati si idratano con una query normale (con `favorite` per l'utente).

### 10.3 Limiti noti

Il tokenizer `unicode61` non segmenta cinese e giapponese: una sequenza di ideogrammi è un solo token e la ricerca per prefisso funziona solo dall'inizio (T19). Nessuna tolleranza agli errori di battitura. Entrambi sono in Appendice D.

---

## 11. Operatività, configurazione e deploy

### 11.1 Configurazione

Solo ambiente, validato all'avvio (`config_invalid`, uscita 2, con l'elenco di tutti gli errori). Percorsi del container fissi (§3.4).

| Variabile | Default | Significato |
|---|---|---|
| `VIBRANCE_PUBLIC_ORIGIN` | nessuno: obbligatoria | Origine esatta con cui si raggiunge il server (§7.6). |
| `VIBRANCE_HTTP_ADDR` | `:8080` | Indirizzo di ascolto nel container. |
| `VIBRANCE_ADMIN_USERNAME` | `admin` | Solo al primo avvio (§7.4). |
| `VIBRANCE_ADMIN_PASSWORD` | vuoto | Obbligatoria solo se non esiste alcun utente. |
| `VIBRANCE_SCAN_INTERVAL` | `5m` | Durata Go, da `30s` in su. |
| `VIBRANCE_WORKERS` | `max(1, min(4, CPU))` | Da 1 a 16: processi ffmpeg/ffprobe e indicizzazioni in parallelo. |

Impostazioni di sola Compose (nel `.env`): `VIBRANCE_BIND` (default `127.0.0.1`), `VIBRANCE_PORT` (default `8090`), `VIBRANCE_UID`/`VIBRANCE_GID` (default `1000`; mai `0`: il server rifiuta di girare come root, `run_as_root`, uscita 2), `VIBRANCE_BACKUP` (cartella o volume dei backup).

Timeout del server HTTP: `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `WriteTimeout` 30 s (**disattivato per richiesta** sull'audio, §9.1), `IdleTimeout` 120 s, `MaxHeaderBytes` 64 KiB.

### 11.2 Avvio e arresto

**Avvio**, nell'ordine; un rifiuto si ferma al suo passo con un log `level=ERROR` e un `code`:

1. Legge e valida la configurazione; rifiuta root (`config_invalid`, `run_as_root`).
2. Apre l'HTTP con *readiness* negativa: `/health/live` risponde 200, `/health/ready` risponde `503 not_ready`.
3. Verifica che `/var/lib/vibrance` sia scrivibile; apre il database; applica le migrazioni (`store_schema_too_new`).
4. Verifica `ffmpeg` e `ffprobe` alla versione fissata (`media_tool_unavailable`, `media_tool_version`).
5. Se non esistono utenti, crea l'admin (`admin_password_missing`, `admin_password_invalid`).
6. Controlla `meta.collate_version` (ricalcola le chiavi se serve) e `meta.ffmpeg_version` (programma il lavoro del §6.6); elimina le sessioni scadute.
7. Avvia lo scanner e la pulizia oraria; la *readiness* diventa positiva.

La *readiness* **non** dipende dallo stato di MusicLib né dell'indice (I14).

**Arresto** (SIGTERM o SIGINT): readiness negativa; `http.Server.Shutdown` con 10 secondi di tolleranza, poi chiusura forzata (gli stream in corso si interrompono); annulla lo scanner e i lavori, terminando i processi figli; `PRAGMA optimize` e `wal_checkpoint(TRUNCATE)`; chiude il database. Crash-only (§2.1): un'uccisione brutale è sempre recuperabile, senza un protocollo di arresto.

### 11.3 Salute

`GET /health/live` → 200 `{"status":"live"}`. `GET /health/ready` → 200 `{"status":"ready"}` oppure 503 con l'errore standard. Fuori dal controllo di `Host`. Il sottocomando `vibrance healthcheck` interroga `/health/ready` su `VIBRANCE_HTTP_ADDR` (locale, timeout 2 s) e termina con 0 o 1: l'immagine non ha `curl`.

### 11.4 Sottocomandi operativi

Codici d'uscita come in MusicLib: **0** successo, **1** operazione fallita o danni trovati, **2** rifiutato prima di fare qualunque cosa o argomenti non validi.

- `vibrance backup --to /backup/NOME`: crea la cartella `NOME` (deve **non** esistere: i backup non si sovrascrivono; il percorso deve stare sotto `/backup`) con `vibrance.db` (copia coerente con `VACUUM INTO`, consentita a server acceso) e `manifest.json` (`app_version`, versione dello schema, data, SHA-256 del file, conteggi). Verifica la copia con `PRAGMA integrity_check` e ne ricalcola lo SHA-256. Si scrive sotto un nome temporaneo e si rinomina solo a successo. Non è incluso `thumbs/` (cache).
- `vibrance restore --from /backup/NOME`: **solo** se `/var/lib/vibrance/vibrance.db` non esiste; verifica manifesto e SHA-256; rifiuta uno schema più nuovo; scrive e sincronizza il file. Niente sovrascritture.
- `vibrance doctor`: in sola lettura: `PRAGMA integrity_check`, `PRAGMA foreign_key_check`, `integrity-check` di ciascuna tabella FTS5, coerenza dei conteggi (`albums.track_count` e `duration_ms` contro le tracce disponibili; ogni riga FTS corrisponde a una riga madre disponibile e viceversa), utenti senza admin abilitato. Stampa un rilievo per riga e una riga finale (`Doctor complete: no damage found.` con uscita 0, oppure l'esito con uscita 1). Non ripara nulla.
- `vibrance user create|reset-password|list` (§7.4) e `vibrance version` (stampa `version: …`).

### 11.5 Log

`log/slog` JSON su stdout. Campi: `time`, `level`, `msg`, `code` (per errori), `request_id`, `method`, `route` (il *pattern*, non il percorso), `status`, `duration_ms`, `bytes`, `user_id`. Il log degli accessi è a livello `info`, tranne `getTrackAudio` e `getAlbumCover` che sono a `debug` (troppo rumorosi). Login riusciti e rifiutati si registrano con nome utente e indirizzo del proxy, **mai** con password, token, cookie o query string. Gli errori 5xx si registrano con la causa completa, mai nella risposta.

### 11.6 Immagine e servizio Compose

Immagine `ghcr.io/tommasonovelli/vibrance:<versione>`, multi-stadio come MusicLib (`.ref/musiclib/Dockerfile`): stadio dei binari ffmpeg copiati dall'immagine di MusicLib (§3.6), stadio `toolchain`, `deps`, `test`, `build-app`, `runtime` (Debian slim fissata per digest, utente `1000:1000`, `/musiclib`, `/var/lib/vibrance` e `/backup` creati con proprietario `1000:1000`, licenze in `/usr/share/doc/vibrance/`). Il servizio:

```yaml
  vibrance:
    image: ghcr.io/tommasonovelli/vibrance:0.1.0
    container_name: ${COMPOSE_PROJECT_NAME:-musiclib}-vibrance
    init: true
    restart: unless-stopped
    user: "${VIBRANCE_UID:-1000}:${VIBRANCE_GID:-1000}"
    security_opt: [no-new-privileges:true]
    cap_drop: [ALL]
    read_only: true
    tmpfs: [/tmp]
    environment:
      VIBRANCE_PUBLIC_ORIGIN: ${VIBRANCE_PUBLIC_ORIGIN:-http://127.0.0.1:${VIBRANCE_PORT:-8090}}
      VIBRANCE_HTTP_ADDR: ":8080"
      VIBRANCE_ADMIN_USERNAME: ${VIBRANCE_ADMIN_USERNAME:-admin}
      VIBRANCE_ADMIN_PASSWORD: ${VIBRANCE_ADMIN_PASSWORD:-}
      VIBRANCE_SCAN_INTERVAL: ${VIBRANCE_SCAN_INTERVAL:-5m}
      VIBRANCE_WORKERS: ${VIBRANCE_WORKERS:-}
    ports:
      - "${VIBRANCE_BIND:-127.0.0.1}:${VIBRANCE_PORT:-8090}:8080"
    volumes:
      - ${MUSICLIB_DATA:-data}:/musiclib:ro        # lo stesso volume di MusicLib, sola lettura
      - vibrance_state:/var/lib/vibrance
      - ${VIBRANCE_BACKUP:-vibrance_backup}:/backup
    depends_on:
      app:
        condition: service_started                 # solo per l'ordine di creazione dei volumi (T21)
    healthcheck:
      test: ["CMD", "/usr/local/bin/vibrance", "healthcheck"]
      interval: 10s
      timeout: 5s
      retries: 3
      start_period: 60s
      start_interval: 1s
```

`depends_on` serve **solo** a creare prima il volume dei dati con il proprietario di MusicLib (T21); a regime Vibrance non dipende da MusicLib (I14: fermare `app` per un backup non tocca Vibrance).

### 11.7 Lo stack è il compose di MusicLib più un servizio

`compose.yaml` di Vibrance = il `compose.yaml` di MusicLib alla versione fissata, **invariato** (nome di progetto `musiclib`, servizi `postgres` e `app`, ancore, volumi `db`, `data`, `backup`), più il servizio `vibrance` e i volumi `vibrance_state` e `vibrance_backup` (D15). `env.example` = `.env.example` di MusicLib più una sezione Vibrance. `scripts/check-compose-sync.sh` scarica il `compose.yaml` e l'`.env.example` della release di MusicLib fissata e verifica che tutto ciò che appartiene a MusicLib coincida (ignorando solo ciò che questo file dichiara aggiunto); il gate di rilascio lo esegue. La versione di MusicLib è fissata in un solo punto e ogni cambiamento passa dalla suite di contratto (§12.3) e da `docs/compat.md`.

Per un'installazione di MusicLib **già esistente** nessuna migrazione: si sostituiscono `compose.yaml` ed `env.example` con quelli di Vibrance (stesso nome di progetto, quindi stessi volumi `musiclib_*`), si aggiungono al `.env` le variabili Vibrance e si esegue `docker compose up -d --wait`.

### 11.8 Caddy e nomi

Due nomi sotto lo stesso dominio, ciascuno con la propria origine (`VIBRANCE_PUBLIC_ORIGIN=https://vibrance.miodominio.net`, `PUBLIC_ORIGIN=https://musiclib.miodominio.net`). Il server di Vibrance serve sia l'API sia (in futuro) l'interfaccia: Caddy non instrada `/api` a parte (D9). Per avere HTTPS senza aprire porte a internet si usa un certificato *wildcard* con la sfida DNS-01 (Caddy con il plugin del proprio provider DNS, compilato con `xcaddy` nel `compose.caddy.yaml` tramite `dockerfile_inline`; token in `.env`); alternativa `tls internal` come nella documentazione di MusicLib.

```text
*.miodominio.net {
	tls { dns cloudflare {env.CF_API_TOKEN} }

	@musiclib host musiclib.miodominio.net
	handle @musiclib { reverse_proxy app:8080 }

	@vibrance host vibrance.miodominio.net
	handle @vibrance { reverse_proxy vibrance:8080 }

	handle { abort }
}
```

Note operative (vanno in `docs/operations.md`): i nomi devono risolvere all'indirizzo **locale** del server sia in LAN sia in VPN (DNS del router o record pubblico verso IP privato: alcuni router lo bloccano come protezione dal *DNS rebinding*); **non** abilitare `encode` per l'API (inutile per audio e cover, indebolisce gli `ETag`); togliere le porte pubblicate dei due servizi con `!reset` (Compose ≥ 2.24); Caddy passa `Host` originale di default; `MusicLib` può restare raggiungibile solo dalla LAN con `@blocked not remote_ip …`.

---
## 12. Strategia di test

Come in MusicLib: si prova la cosa vera dove la cosa vera conta, e si controlla che i test falliscano davvero quando l'invariante si rompe.

### 12.1 Livelli

- **Unitari puri:** `names`, parser della ricevuta (+ fuzz), classificazione dei file, *planner* di riconciliazione (tabelle e test di proprietà), parser LRC (+ fuzz), codec dei cursori (+ fuzz), costruzione delle query FTS (+ fuzz), PHC/argon2id (+ fuzz), configurazione, JSON rigoroso (+ fuzz).
- **Store:** SQLite vero sul volume di prova ext4: migrazioni da vuoto, vincoli, chiavi esterne su ogni connessione, concorrenza (lettori durante una scrittura), `VACUUM INTO`.
- **Media:** `ffmpeg` e `ffprobe` veri, alla versione fissata, sui file di `testdata/library-v1/`.
- **Libreria e scanner:** cartelle temporanee vere, copie della libreria di prova, con mutazioni a caldo (rinomine, sostituzioni, file mancanti, permessi).
- **API:** stack intero con `httptest` (database vero, scanner vero sulla libreria di prova). **Ogni** risposta dei test si valida contro `api/openapi.yaml` con `kin-openapi` (helper `assertConforms`). Niente mock di SQL, filesystem o processi.
- **Contratto con MusicLib reale:** `scripts/contract.sh` (§12.3). **Non** è nel gate veloce; è obbligatorio prima di ogni rilascio e dopo ogni cambio di versione di MusicLib.
- **Prestazioni:** passo S24, etichetta di build `perf`, fuori dal gate veloce.
- **Sempre `-race`.** Il race detector non sostituisce i test di protocollo.
- **Non si testano:** l'aspetto di una UI, la formulazione della documentazione, librerie di terzi.

### 12.2 La libreria di prova

`testdata/library-v1/` è una **vera** cartella `library/` prodotta da MusicLib 1.1.0 (`scripts/make-fixture-library.sh`, passo S1), con file minuscoli e sintetici (seno di 2 secondi), quindi ridistribuibili. Album: **A** FLAC tre tracce con cover, `.lrc` e ReplayGain; **B** MP3 due tracce; **C** M4A-AAC due tracce; **D** M4A-ALAC due tracce; **E** FLAC multidisco; **F** FLAC con due tracce dallo stesso audio. `FIXTURE.md` registra la versione di MusicLib e come rigenerarla. Gli MP3 di ingresso si producono con `lame` in un container Debian usa-e-getta (l'`ffmpeg` di MusicLib non ha encoder MP3); il resto con l'`ffmpeg` dell'immagine di MusicLib.

I test che devono *modificare* album (rinomine, tag, cover, cestino) lo fanno sulla **copia** temporanea, con helper di test che riscrivono file e ricevuta in modo coerente (`ffmpeg -c copy -metadata …` produce un file con lo stesso audio e uno SHA-256 diverso). Ciò che solo MusicLib vero può produrre lo prova il contratto.

### 12.3 La suite di contratto

`scripts/contract.sh` avvia lo stack nel progetto Compose `vibrance-contract` (volumi nuovi, password casuali, **mai** il progetto `musiclib` dell'utente), usa le API documentate di MusicLib per creare e modificare album e quelle di Vibrance per verificarli, poi **spegne e cancella solo quel progetto**. Gli scenari (ognuno parte da playlist e preferiti già creati sulle tracce interessate):

| # | In MusicLib | Atteso in Vibrance |
|---|---|---|
| A1 | Cambia il titolo di una traccia | Stesso `track.id`, titolo nuovo; preferito e playlist intatti. |
| A2 | Scambia due numeri di traccia | Gli ID seguono l'audio: stessi ID, numeri scambiati. |
| A3 | Cambia la cover | Stessi ID; `cover.hash` nuovo; un URL con `v` vecchio serve la cover corrente con `no-cache`. |
| A4 | Rinomina l'album | Stessi ID di album e tracce; mai due album visibili. |
| A5 | Rinomina l'artista | Album e tracce invariati; nuovo ID artista; liste coerenti. |
| A6 | Sposta l'album a un altro artista | Come A5. |
| A7 | Elimina una traccia | Quella diventa `available: false` (resta in grigio nelle playlist), le altre invariate. |
| A8 | Sposta l'album nel cestino | Album fuori dalle liste; tracce `available: false`; audio `404 track_unavailable`; playlist e preferiti mostrano le tracce in grigio. |
| A9 | Ripristina l'album dal cestino | Stesso `album_id`, stessi ID delle tracce, di nuovo disponibili. |
| A10 | «Rebuild the library folder» online | Stessi ID, indice invariato. |
| A11 | `rebuild` offline (`app` fermata, `.maintenance` presente) | Durante: stato `maintenance`, indice intatto; dopo: tutti gli album tornano con gli stessi ID. |
| A12 | Importa un album nuovo | Compare entro un ciclo di scansione. |
| A13 | Riscrive un album mentre un client lo sta riproducendo | `If-Range` giusto con `Range` vecchio → file intero (200); oppure `503 library_changing` e poi successo dopo lo scan: mai byte di due versioni. |
| A14 | Due tracce con lo stesso audio nello stesso album; si cancella la prima | La seconda conserva il proprio ID. |
| A15 | Riavvii di Vibrance e di MusicLib in ordine casuale durante la scansione iniziale | Nessuna perdita e nessun duplicato. |
| A16 | `library/` momentaneamente illeggibile (rinominata dall'interno del container di MusicLib) | Tutto `available: false` ma nulla perso; al ritorno tutto com'era. |

Per aggiungere uno scenario: una riga in questa tabella, il test corrispondente, e una riga in `docs/compat.md` se cambia ciò su cui Vibrance conta.

### 12.4 Matrice di autorizzazione

Un test generato dalla specifica: per ogni `operationId` × {anonimo, utente, admin, altro utente proprietario della risorsa} dichiara lo stato atteso (200/201/204/401/403/404). Il test **fallisce** se la specifica contiene un'operazione che non è nella matrice, e se la matrice contiene un'operazione che la specifica non ha. Le rotte di infrastruttura (§8.3) sono elencate a parte. È il modo di non dimenticare l'autorizzazione su un endpoint nuovo (I6).

### 12.5 Fuzz

Bersagli obbligatori: `FuzzParseReceipt`, `FuzzParseLRC`, `FuzzCursor`, `FuzzSearchQuery`, `FuzzPHC`, `FuzzStrictJSON`, `FuzzMapTags`, `FuzzClassify` (percorsi). Ogni passo che introduce un bersaglio lo fa girare per almeno 60 secondi (`scripts/fuzz.sh`) e committa gli input che hanno trovato errori come test di regressione.

### 12.6 Controlli per mutazione

Per le invarianti importanti l'ingegnere **rompe il codice di proposito** e verifica che almeno un test fallisca; il rapporto dice quali mutazioni ha provato. Elenco minimo: ultimo admin; `If-Match` su `move` e sull'inserimento con posizione; proprietà delle playlist (A non legge B); scadenza e revoca delle sessioni; hashing dei token; `Host`/`Origin`/header anti-CSRF; fasi F1, F2, F3 del planner; «le righe non si cancellano mai» (I3); confinamento di `os.Root`; guardia `(size, mtime)`; nessun I/O dentro una transazione di scrittura (un test con un runner che fallisce se invocato con una transazione aperta).

---

## 13. Trappole note

Le trappole sono numerate (T1…T30) e i passi del §14 citano quelle che li riguardano. Sono cose che **sembrano funzionare** finché non si rompono.

- **T1 — Stream audio.** `ffprobe` elenca la cover incorporata come stream *video*. Scegli lo stream con `codec_type == "audio"`, mai `streams[0]`; per `ffmpeg` usa `-map 0:a:0`.
- **T2 — Processi esterni.** Nessuna shell; `exec.CommandContext`; gruppo di processi e `Pdeathsig` (Linux) perché un arresto brutale non lasci figli; file passati come descrittore 3 con `-protocol_whitelist fd -fd 3 -f <demuxer> -i fd:` (mai un percorso: un nome che inizia con `-` o contiene `:` sarebbe un'opzione o un protocollo); stderr limitato a 64 KiB e mai interpretato per dichiarare successo; `init: true` in Compose per raccogliere gli zombie.
- **T3 — Uno scrittore.** `BEGIN IMMEDIATE` (`_txlock=immediate` nel DSN dell'handle di scrittura) e `SetMaxOpenConns(1)`: una transazione che parte in lettura e poi scrive può fallire subito con `SQLITE_BUSY` anche con `busy_timeout`. Niente I/O dentro una transazione di scrittura (I11).
- **T4 — PRAGMA per connessione.** `foreign_keys`, `busy_timeout`, `journal_mode`, `synchronous` valgono per connessione: vanno nel DSN (parametro `_pragma`), non in un `Exec` isolato; un test li verifica su **ogni** connessione del pool, lettori compresi. Verifica che la versione scelta di `modernc.org/sqlite` accetti `_pragma` e `_txlock`.
- **T5 — `UNIQUE` non differibile.** In SQLite solo le chiavi esterne sono differibili: niente `UNIQUE(playlist_id, position)`. Le posizioni non sono uniche; si rinumerano a blocchi nella stessa transazione (`UPDATE … FROM` con `row_number()`), sempre ordinando per `(position, id)`.
- **T6 — `rowid` e `VACUUM`.** Senza una chiave `INTEGER PRIMARY KEY` esplicita, `VACUUM` (e `VACUUM INTO`, usato dal backup) può rinumerare i `rowid`, e le tabelle FTS5 che puntano a quei `rowid` si disallineano. Da qui la colonna `seq` esplicita. Un test fa un backup e verifica che la ricerca dia gli stessi risultati.
- **T7 — `sqlc` e FTS5.** `sqlc` potrebbe non capire `CREATE VIRTUAL TABLE`: la migrazione FTS5 è un file a parte, **escluso** dall'elenco `schema` di `sqlc`; le query FTS5 sono scritte a mano in `internal/search`. Verifica anche che `sqlc` legga le annotazioni di `goose`.
- **T8 — Tempo e UUID.** Timestamp come interi (ms), mai `time.Time` dal driver; UUID sempre minuscoli in `TEXT`; `uuid.NewV7` richiede `google/uuid` ≥ 1.6.
- **T9 — Paginazione a chiave.** `year_key` esiste perché `NULL` ordina in modo incoerente fra direzioni; con `order=desc` **tutte** le chiavi si invertono; il confronto di valori di riga può non usare l'indice se scritto male (`OR`): controllare `EXPLAIN QUERY PLAN`. Il test di proprietà confronta le pagine con l'elenco completo. Il cursore è input ostile: decodifica rigorosa, mai `panic`.
- **T10 — Ricevuta sostituita mentre la leggi.** Leggi la ricevuta, indicizza, **rileggi** la ricevuta e confronta l'hash; se è cambiata, abbandona l'album senza scrivere. Non fidarti di `mtime` delle cartelle. Durante una rinomina esistono due cartelle con lo stesso `album_id` (§6.2).
- **T11 — `os.Root`.** Tutta la lettura della libreria passa da `os.Root` (Go ≥ 1.24). `os.Root` segue link simbolici che restano *dentro* la radice: scarta comunque qualunque link con `Lstat`. I percorsi vengono dal database, si validano con `fs.ValidPath` e non si costruiscono mai con `filepath.Join(radice, input)`.
- **T12 — `ServeContent`, cache e scadenze.** Imposta `Content-Type` e `ETag` **prima** di `ServeContent` (altrimenti indovina il tipo); passa un nome vuoto; `If-Range` con `ETag` diverso fa ignorare il `Range`: è voluto. `WriteTimeout` globale interromperebbe i brani lunghi: disattivalo per richiesta con `http.NewResponseController(w).SetWriteDeadline(time.Time{})`; ogni wrapper di `ResponseWriter` nei middleware deve implementare `Unwrap() http.ResponseWriter` o il controller non lo raggiunge.
- **T13 — File sostituiti.** Fra una scansione e l'altra MusicLib può riscrivere un album. La guardia `(size, mtime_ns)` sul file aperto, il trigger dello scanner e `503 library_changing` con `Retry-After` evitano di servire byte del file sbagliato con l'`ETag` sbagliato.
- **T14 — Cookie.** `HttpOnly`, `SameSite=Strict`, `Secure` **solo** con origine `https` (con http e `Secure` il browser scarterebbe il cookie in LAN); nome diverso da quello di MusicLib (i cookie non sono separati per porta, e i due prodotti potrebbero stare sullo stesso host IP).
- **T15 — argon2id.** Usa molta memoria: verifica una alla volta (§7.2). Un utente inesistente deve costare come uno esistente (hash fittizio) e dare lo stesso errore. Confronti a tempo costante. Nei test, parametri ridotti.
- **T16 — Sessioni.** Il rinnovo a metà durata e l'aggiornamento limitato di `last_used_at` evitano una scrittura per richiesta (un solo scrittore!). Cambio password, disattivazione ed eliminazione revocano subito. Il clock è iniettabile nei test.
- **T17 — LRC.** I file reali sono sporchi: BOM, `\r`, più timestamp per riga, `[offset:…]`, tag di parola `<…>`, righe vuote, nessun timestamp (§9.3). Il parser non deve mai andare in panico, né produrre tempi negativi o righe fuori ordine.
- **T18 — Query FTS5.** Non passare mai testo dell'utente come sintassi di `MATCH`: `"`, `*`, `-`, `NEAR`, `OR`, `colonna:testo` hanno un significato. Si divide in token alfanumerici e si racchiude ciascuno tra virgolette con `*` finale (§10.2). Test di fuzz.
- **T19 — CJK.** `unicode61` non segmenta cinese e giapponese (§10.3): limite noto e documentato, non un bug da «risolvere» in questo passo.
- **T20 — Miniature.** `image.DecodeConfig` **prima** di decodificare (una cover da 40 megapixel in RGBA pesa ~160 MB); semaforo di capacità 2; `singleflight`; scrittura atomica (temporaneo + `rename`); PNG con alfa composto su bianco; mai ingrandire; se qualcosa fallisce, si serve l'originale.
- **T21 — Proprietario del volume e ordine di avvio.** Docker inizializza un volume nominato nuovo con contenuto e proprietario della cartella-immagine del **primo** container che lo monta. Se `vibrance` creasse `data` prima di `app`, il volume sarebbe di root e MusicLib non partirebbe (`volume_permission`). Soluzione: `depends_on: app: service_started` e `/musiclib` creata `1000:1000` nell'immagine. Da provare **nei due ordini** con volumi nuovi (S22).
- **T22 — Windows e Docker Desktop.** Git Bash con `MSYS_NO_PATHCONV=1`; finali di riga LF; **mai** bind mount di cartelle Windows per il database SQLite (il WAL non funziona su file system condivisi): volumi nominati; i test girano sul volume ext4 della VM di Docker. Un passo non è «provato su Linux nativo» finché non lo si è fatto (§3.5).
- **T23 — `oapi-codegen` e validatore.** Il validatore confronta la richiesta con `servers`: sulla copia della specifica usata a runtime si imposta `Servers = nil`. `AuthenticationFunc` no-op (l'autenticazione è nostra). `additionalProperties: false` in **ogni** schema di richiesta, o le chiavi sconosciute passano. Il validatore non vede le chiavi **duplicate**: serve il middleware a parte. Si usa OpenAPI 3.0.3 (la 3.1 non è supportata del tutto dagli strumenti). La pagina `/api/docs` ha bisogno di una CSP più larga dell'API (stili in linea): verificarla davvero in un browser una volta.
- **T24 — Proxy e compressione.** Caddy con `encode` indebolisce o cambia gli `ETag`: non abilitarlo per l'API; gli header `X-Forwarded-*` si ignorano; `Host` deve arrivare intatto.
- **T25 — Date.** Sempre RFC 3339 UTC con tre cifre di millisecondi (`2026-09-30T12:34:56.000Z`).
- **T26 — Chiavi di collazione.** Le chiavi dipendono dalla versione di `x/text`: si ricalcolano all'avvio se `collate_version` cambia (§5.5).
- **T27 — Arresto con stream lunghi.** `Shutdown` aspetta le connessioni aperte: 10 secondi di tolleranza e poi chiusura forzata (§11.2).
- **T28 — Log e segreti.** Mai `Authorization`, `Cookie`, `Set-Cookie`, corpi di login, password, token o query string nei log. Un test legge l'output dei log di una sessione completa e cerca i segreti usati.
- **T29 — Cover incorporata.** Ogni traccia contiene la cover: non estrarla, non usarla come fonte, non scambiare il suo stream per l'audio. La cover dell'album è `cover.jpg|png`.
- **T30 — Prima scansione.** Non tenere in memoria l'elenco di tutte le tracce; commit per album; pool di worker limitato; `PRAGMA optimize` a fine ciclo.

---
## 14. Piano di implementazione

Si esegue **nell'ordine**, un passo alla volta, con il ciclo del §0. Le dipendenze indicate sono reali: non riordinare.

```text
Fase 0  Fondamenta e ipotesi     S0  S1
Fase A  La libreria              S2  S3  S4  S5  S6  S7  S8  S9  S10      (fine fase: S10)
Fase B  L'API                    S11 S12 S13 S14 S15 S16 S17 S18 S19 S20  (fine fase: S20)
Fase C  Operatività e contratto  S21 S22 S23                              (fine fase: S23)
Fase D  Prestazioni e rilascio   S24 S25                                  (fine fase: S25)
```

### 14.1 Regole comuni a ogni passo (Definition of Done)

1. Il passo è implementato **per intero**, senza stub né TODO nel suo ambito, e **solo** ciò che chiede (I16).
2. `scripts/check.sh` (intero modulo) passa. I test nuovi di codice concorrente si ripetono con `-race -count=20`. Ogni bersaglio di fuzz nuovo gira almeno 60 secondi.
3. I test coprono i contratti del passo **compresi i percorsi di errore e la concorrenza**, come dal §12; le invarianti importanti sono controllate per mutazione (§12.6) e il rapporto dice quali mutazioni sono state provate.
4. Documentazione e specifica cambiano insieme al comportamento: `api/openapi.yaml` (e il codice generato, committato), `docs/operations.md` dove serve. Il codice generato (`sqlc`, `oapi-codegen`) è committato e il gate verifica che sia aggiornato.
5. Il componente è collegato al server in esecuzione (`internal/app`) se il passo lo dice; niente codice esportato e mai usato.
6. Decisioni, deviazioni, rischi e domande vanno in `NOTES.md` (formato dell'Appendice C), con stato `DECIDED` o `TO CONFIRM`.
7. Nessuna dipendenza fuori dal §2.4 senza una voce `TO CONFIRM`.
8. L'ingegnere **non committa** e **non modifica** `DESIGN.md` né `PROGRESS.md` (sono dell'orchestratore).
9. Rapporto finale nel formato dell'Appendice A.

Ogni passo ha: **Persona** (l'identità professionale con cui lavorano ingegnere e revisore), **Dipende da**, **Leggere**, **Da fare**, **Da non fare**, **Attenzione** (trappole del §13) e **Accettazione** (ciò che il revisore verifica, oltre a I1–I16 e alle regole comuni). Il riferimento `.ref/musiclib/…` è il clone di MusicLib in sola lettura (§0.2).

---

### Fase 0 — Fondamenta e ipotesi

#### S0 — Bootstrap del repository e toolchain

- **Persona:** ingegnere DevOps e build, esperto di Go e di Docker riproducibile.
- **Dipende da:** —
- **Leggere:** §2, §3 (tutto), §12.1–12.2, T2, T22; `.ref/musiclib/{Dockerfile, compose.dev.yaml, scripts/, docker/, .gitattributes, .dockerignore, .gitignore, CLAUDE.md}` e `docs/docker.md`.
- **Da fare:**
  - `go.mod` (`module vibrance`, `go 1.25.0`), struttura del §3.3 (cartelle con un `doc.go` solo dove serve), `cmd/vibrance` con il solo sottocomando `version` (stampa `version: …`), `internal/buildinfo` (versione stampata con `-ldflags`, default `devel`).
  - `Dockerfile` (stadi del §3.6 e §11.6) con `ffmpeg`/`ffprobe` copiati da `ghcr.io/tommasonovelli/musiclib:1.1.0@sha256:…` (risolvi il digest con `docker buildx imagetools inspect`; se l'immagine non è scaricabile: **BLOCCO**), `compose.dev.yaml` (servizi `test` e `dev` nel profilo `tools`, volume ext4 di prova, progetto `vibrance-dev`).
  - `scripts/check.sh`, `dev.sh`, `lint-shell.sh`, `docker/gate.sh`, `docker/with-testdata.sh`, modellati su quelli di MusicLib; il gate esegue build, vet, gofmt, `go test -race` in un container senza rete.
  - Un test `TestPinnedToolsInstalled` che, nel container di test, verifica `ffmpeg` e `ffprobe` alla versione `8.1.3-musiclib1`.
  - `CLAUDE.md` e `AGENTS.md` (contenuto identico) per gli agenti: riassumono §2 (invarianti, stile, lingua), come eseguire il gate, cosa non toccare, e il flusso di lavoro di questo documento (l'ingegnere non committa; `NOTES.md`; dove sta il design).
  - `NOTES.md` (intestazione e formato), `LICENSE` (MIT, «Copyright 2026 tommasonovelli»), `.gitignore`, `.dockerignore`, `.gitattributes` (`* text=auto eol=lf`), `README.md` minimo (cos'è, come si prova), `docs/compat.md` (segnaposto: MusicLib 1.1.0, da confermare in S23).
  - Il `runtime` ha utente `1000:1000` e le cartelle del §11.6; per ora `CMD` è `vibrance version`.
- **Da non fare:** nessun codice di dominio; nulla installato sull'host; nessun `latest`.
- **Attenzione:** T2, T22, I12 (tutti i pin si **copiano** dai file di MusicLib).
- **Accettazione:** `scripts/check.sh` verde da un checkout pulito; `docker build --target runtime` produce un'immagine che stampa la versione; `lint-shell.sh` pulito; i pin coincidono con quelli di `.ref/musiclib` (il revisore li confronta); `.ref/` non è tracciata da git.

#### S1 — Spike: le ipotesi del contratto con MusicLib reale

- **Persona:** ingegnere di integrazione e QA, esperto di formati audio e di ffmpeg.
- **Dipende da:** S0.
- **Leggere:** §4 (tutto), §5.4, §12.2; `.ref/musiclib/README.md`, `docs/operations.md` (sezioni «The library folder», «Importing and the Activity page», «The API»), `internal/media/{probe.go, digest.go}`.
- **Obiettivo:** verificare con il prodotto **reale** le ipotesi marcate **[S1]** del §4 e produrre la libreria di prova. **Se un'ipotesi critica è falsa, il passo si ferma con `BLOCCO:`**: non si «aggiusta» il design (§0.10).
- **Da fare:**
  1. `scripts/make-fixture-library.sh`: avvia MusicLib 1.1.0 nel progetto `vibrance-spike` (volumi nuovi; password casuale in un `.env` non committato), crea gli audio d'ingresso come descritto nel §12.2, li importa con l'API di MusicLib, copia `library/` in `testdata/library-v1/` e scrive `FIXTURE.md`. Album A–F del §12.2; dimensione totale inferiore a 2 MiB; poi spegne e cancella **solo** il progetto `vibrance-spike`.
  2. Uno strumento di verifica (script o programma di prova, non di produzione) che per ogni ipotesi registra `CONFERMATA` o `FALSA` con comando e output reali in `docs/spike-report.md`:
     - **H1** l'immagine è scaricabile; contiene `ffmpeg` e `ffprobe` in `/usr/local/bin` alla versione `8.1.3-musiclib1`; il muxer `hash` è disponibile.
     - **H2** l'impronta (`-map 0:a:0 -c copy -f hash -hash sha256 -`) è **identica prima e dopo**: cambio di titolo, scambio di numeri, cambio di cover, rinomina dell'artista, render forzato; per **FLAC, MP3, AAC e ALAC**; ed è **diversa** fra due tracce di audio diverso.
     - **H3** le chiavi di tag che `ffprobe` riporta per ciascun formato (tabella reale: titolo, artista, artista album, album, traccia, disco, anno, genere, compilation, ReplayGain).
     - **H4** gli stream «video» di ogni file sono la cover; lo stream audio si riconosce per `codec_type`.
     - **H5** semantica della ricevuta: `album_revision` aumenta con le modifiche e **non** con il render forzato; `render_version` c'è; `build_id` cambia a ogni render; la ricevuta non elenca sé stessa; i file elencati coincidono con quelli su disco.
     - **H6** la durata calcolata come nel §4.6 per i quattro codec.
     - **H7** comportamento osservato di: rinomina di album (finestra con due cartelle), cestino e ripristino (stesso `album_id`, stessi SHA-256), presenza di `.maintenance` durante un `rebuild` offline, presenza di `.musiclib-store`.
     - **H8** tempi di `ffprobe` e dell'impronta sui file di prova e su un FLAC sintetico di circa 50 MB.
     - **H9** i tag ReplayGain presenti negli originali sopravvivono al render e con quali chiavi li riporta `ffprobe`.
- **Da non fare:** nessun codice di prodotto; nessun progetto Compose diverso da `vibrance-spike`.
- **Attenzione:** T1, T2, T29.
- **Accettazione:** `testdata/library-v1/`, lo script di rigenerazione e `docs/spike-report.md` committati e completi. **BLOCCO** se H1 è irrisolvibile, o se H2 o H5 sono false per qualche codec. Il revisore riesegue H2 sul codec più rischioso (MP3) e confronta l'output con il rapporto.
- **Fine fase 0:** l'orchestratore riassume all'utente le ipotesi (§0.11).

---

### Fase A — La libreria

#### S2 — Configurazione, avvio, salute e arresto

- **Persona:** ingegnere backend Go (servizi di rete, ciclo di vita).
- **Dipende da:** S0.
- **Leggere:** §3.4, §7.6 (forma di `VIBRANCE_PUBLIC_ORIGIN`), §11.1–11.3, T27; `.ref/musiclib/cmd/musiclibd/{config.go, boot.go, health.go, main.go}`.
- **Da fare:** `internal/config` (lettura e validazione con elenco di **tutti** gli errori, `config_invalid`, uscita 2; regole di `VIBRANCE_PUBLIC_ORIGIN`, `VIBRANCE_SCAN_INTERVAL`, `VIBRANCE_WORKERS`; le variabili admin si validano in S13); `internal/app` con i passi 1–2 dell'avvio del §11.2 e l'arresto ordinato, più segnaposto dichiarati per i passi successivi; sottocomandi `serve` e `healthcheck`; server HTTP con i timeout del §11.1; `/health/live`, `/health/ready` (readiness negativa finché l'avvio non termina); `GET /` che reindirizza a `/api/docs` (la pagina arriva in S20); rifiuto di uid 0 (`run_as_root`) e `umask 022`; log `slog` JSON.
- **Da non fare:** database, scanner, autenticazione, controllo degli strumenti (S5).
- **Attenzione:** T27.
- **Test:** configurazione a tabella (ogni errore, più errori insieme); avvio e arresto con processo vero (SIGTERM e SIGKILL); readiness 503 poi 200; `healthcheck` con e senza server; `run_as_root`.
- **Accettazione:** il binario parte, risponde alla salute e si ferma in modo pulito; ogni errore di configurazione elenca tutte le variabili errate.

#### S3 — Store SQLite, migrazioni, transazioni

- **Persona:** ingegnere di database, specializzato in SQLite.
- **Dipende da:** S0.
- **Leggere:** §5.1, §5.2, §10.1, I7, I11, I13, T3–T8; `.ref/musiclib/{sqlc.yaml, migrations/, sql/, internal/store/, scripts/sqlc.sh}`.
- **Da fare:** `internal/store`: apertura con due handle (scrittura: una connessione, `BEGIN IMMEDIATE`; lettura: pool, `query_only`; pragma nel DSN); `goose` con migrazioni incorporate che creano **l'intero schema del §5.2** (vincoli `CHECK`, `FOREIGN KEY`, indici), con l'FTS5 in una migrazione separata; `sqlc.yaml` (motore `sqlite`, `database/sql`, schema = elenco esplicito dei file **esclusa** la migrazione FTS5) e `scripts/sqlc.sh` (+ `diff` nel gate); helper `WithWriteTx(ctx, fn)` (commit e rollback mai ignorati) e `Read`; lettura e scrittura di `meta`; `store_schema_too_new`; chiusura con `PRAGMA optimize` e `wal_checkpoint(TRUNCATE)`; `app` apre lo store al passo 3 dell'avvio.
- **Da non fare:** query di dominio oltre quelle di prova (arrivano con i passi che le usano).
- **Attenzione:** T3, T4, T5, T6, T7, T8.
- **Test:** migrazione da vuoto; ogni `CHECK`, `FOREIGN KEY` e `UNIQUE` rifiuta i dati non validi (tabella); `foreign_keys` attive su **ogni** connessione del pool; lettori concorrenti durante una scrittura lunga senza `SQLITE_BUSY`; due scrittori serializzati; `MATCH` di prova su FTS5; **`VACUUM INTO` preserva la corrispondenza fra `rowid` e FTS** (T6); `store_schema_too_new`; nessun I/O possibile dentro `WithWriteTx` è un test in S7.
- **Accettazione:** gate verde con `sqlc diff` pulito; il revisore controlla che lo schema coincida con il §5.2 colonna per colonna.

#### S4 — Ricevuta, accesso confinato e classificazione

- **Persona:** ingegnere backend Go con esperienza di filesystem sicuri e parsing rigoroso.
- **Dipende da:** S1.
- **Leggere:** §4.1–4.5, §6.2, §6.3 (passi 1–2), I1, I2, T10, T11; `.ref/musiclib/internal/render/receipt.go` (e i suoi test), `internal/fsops/`.
- **Da fare:** `internal/library` (parte I/O): `Root` (apre `/musiclib` con `os.Root`; elenco degli artisti e degli album; lettura della ricevuta con limite di 16 MiB; `Lstat`; `Open`; controllo dei marcatori `.maintenance` e `.musiclib-store`); parser della ricevuta (§4.2) e `receipt_hash`; `Classify(receipt)` puro (audio, cover, testi, ignorati: §6.3 passo 1); `Discover(ctx, root)` che esegue P1 e P2 del §6.1–6.2 **senza database** e restituisce candidati e problemi con i codici del §6.5.
- **Da non fare:** leggere tag o lanciare processi; toccare il database.
- **Attenzione:** T10, T11.
- **Test:** ricevute vere dalla libreria di prova e oltre 40 ricevute ostili a tabella; `FuzzParseReceipt`; classificazione (`Disc N`, `Extras`, `cover`, `.lrc` abbinato e non, estensioni in maiuscolo, percorsi con spazi, Unicode, `..`, assoluti, `\`); `Discover` su copie della libreria: ricevuta mancante, `schema_version` 2, ricevuta oltre 16 MiB, cartelle che iniziano con `.`, **link simbolici scartati**, due cartelle con lo stesso `album_id` (vince la revisione più alta), cartella artista illeggibile (`listing_failed` e protezione degli album sotto); confinamento: un link che punta fuori dalla radice non viene mai seguito né letto (file esca fuori radice).
- **Accettazione:** nessun accesso al filesystem della libreria passa da altro che `os.Root` (il revisore cerca `os.Open`, `filepath.Join`, `os.ReadDir` su percorsi della libreria).

#### S5 — Adapter media: processi, tag, durata, impronta

- **Persona:** ingegnere media, esperto di ffmpeg.
- **Dipende da:** S1, S2.
- **Leggere:** §4.3, §4.6, §5.4 (impronta), §11.2 passo 4, I8, T1, T2, T29; `docs/spike-report.md`; `.ref/musiclib/internal/media/{runner.go, probe.go, digest.go, tools.go}`.
- **Da fare:** `internal/media`: `Runner` (senza shell; `CommandContext`; gruppo di processi e `Pdeathsig` in un file con vincolo di build Linux; file per descrittore 3 con `-protocol_whitelist fd -fd 3 -f <demuxer> -i fd:`; timeout; stderr limitato a 64 KiB; semaforo globale di capacità `VIBRANCE_WORKERS`); `Probe` (stream per `codec_type`, durata del §4.6, codec fra `flac|mp3|aac|alac`, frequenza, canali, profondità, bitrate); `MapTags` secondo la **tabella reale** di `docs/spike-report.md` (chiavi senza distinzione di maiuscole; «N/M»; anno dalle prime quattro cifre; compilation; ReplayGain `"-7.12 dB"` → numero); `Fingerprint` (restituisce anche la versione di ffmpeg); controllo degli strumenti (`media_tool_unavailable`, `media_tool_version`) collegato al passo 4 dell'avvio del §11.2.
- **Da non fare:** conoscere il dominio (album, playlist); usare TagLib o altre librerie di tag.
- **Attenzione:** T1, T2, T29.
- **Test:** su tutti i file della libreria di prova: tag attesi, durata, codec; impronta stabile fra due esecuzioni e diversa fra audio diversi; un processo che non termina → timeout e **nessun figlio orfano** (verifica con `/proc`); contesto annullato → gruppo terminato; file non audio → errore tipizzato; stderr enorme troncato; nomi di file ostili non diventano mai argomenti; versione sbagliata → `media_tool_version`; `FuzzMapTags`.
- **Accettazione:** il server rifiuta di partire con strumenti alla versione sbagliata.

#### S6 — Identità e riconciliazione (puro)

- **Persona:** ingegnere di algoritmi e correttezza, esperto di test di proprietà.
- **Dipende da:** S5.
- **Leggere:** §5.3, §5.4, §5.5, I3, T26; `.ref/musiclib/internal/names/`.
- **Da fare:** `internal/names` (`Normalize`, `IdentityKey`, `ArtistID`, `SortKey`; attenzione a `cases.Fold`); `internal/library`: `DeriveAlbum(tracks) AlbumMeta` (§5.3) e il planner di riconciliazione (`NeedFingerprint`, `Reconcile`, tipi `OldTrack`, `NewFile`, `Plan`) **esattamente** come nel §5.4.
- **Da non fare:** nessun I/O, nessun database.
- **Test:** a tabella, per **ogni** scenario della tabella del §12.3 tradotto a livello di planner (cambio titolo, scambio di numeri, cambio cover, rinomina, cancellazione di una traccia, cestino e ripristino, `render_version` con sha uguali, cambio di versione di ffmpeg con album modificato (F3), due tracce con lo stesso audio e cancellazione della prima, inserimento di una traccia, tutte le righe non disponibili che ritornano); **test di proprietà** con generatori casuali: un `old` non è mai abbinato a due `new`; nessun `old` si perde; `occurrence` sempre unica; applicare due volte lo stesso piano è idempotente; F3 non abbina mai un `old` con `fp_version` corrente; `DeriveAlbum` è deterministico rispetto all'ordine d'ingresso. **Mutazioni:** togliere F2, togliere F3, invertire F1 e F2 devono far fallire almeno un test.
- **Accettazione:** il revisore verifica a mano due scenari del §12.3 contro le regole F1–F5.

#### S7 — Indicizzare un album

- **Persona:** ingegnere backend e database, esperto di concorrenza.
- **Dipende da:** S3, S4, S5, S6.
- **Leggere:** §5.2–5.4, §6.3, §6.5, §10.1, I3, I11, T3, T6, T7, T10.
- **Da fare:** `internal/library/indexer.go` con `IndexAlbum(ctx, candidate)` completo (§6.3 passi 1–8; il passo 9 è un'interfaccia `CoverWarmer` con implementazione nulla finché non c'è S9); query `sqlc` (upsert di artisti e album, upsert e disponibilità delle tracce, contatori); manutenzione FTS5 in `internal/search` (inserimento e cancellazione per album) nella stessa transazione; riuso dei metadati per F1 **senza lanciare processi**; errori di problema tipizzati (§6.5).
- **Da non fare:** il ciclo di scansione (S8); miniature (S9).
- **Attenzione:** T3, T6, T7, T10.
- **Test:** (libreria di prova + SQLite vero) indicizza A–F e i dati (titoli, artisti, anno, genere, compilation, numeri, durata, codec, ReplayGain) coincidono con i tag; l'FTS contiene le righe; **reindicizzare lo stesso album non lancia alcun processo** (il runner di test conta le chiamate) e non cambia nulla; una variante con titolo cambiato (helper di test del §12.2) mantiene lo stesso `tracks.id`; album sostituito fra il passo 4 e il passo 6 (hook di test) → abbandono senza scritture; un errore di probe non lascia scritture parziali (conteggi invariati); due album in parallelo; un lettore concorrente non vede mai un album a metà; **un test che invoca il runner mentre una transazione di scrittura è aperta deve fallire** (I11).
- **Accettazione:** il revisore controlla che nessuna chiamata a `media` o al filesystem avvenga dentro `WithWriteTx`.

#### S8 — Scanner: ciclo, trigger, stato

- **Persona:** ingegnere backend Go, esperto di concorrenza e di cicli di riconciliazione.
- **Dipende da:** S7.
- **Leggere:** §6 (tutto), §4.5, I3, I14, I15, T10, T30.
- **Da fare:** `internal/library/scanner.go`: ciclo P0–P6, pool di worker, trigger coalescenti (avvio, timer, richiesta, file sostituito), stato e problemi con un'API interna `Status()` e `Trigger(reason)`, assenti (§6.4), memoria degli album falliti, marcatori `.maintenance` e `.musiclib-store`, lavori del §6.6 (nuove impronte al cambio di versione di ffmpeg; ricalcolo delle chiavi al cambio di `collate_version`); collegamento al passo 6–7 dell'avvio; pulizia oraria delle sessioni è di S13.
- **Da non fare:** endpoint HTTP (S20).
- **Attenzione:** T10, T30.
- **Test:** sulla copia della libreria di prova, con mutazioni a caldo: album aggiunto, modificato, cartella rinominata, album rimosso e tornato (stesso `album_id`, stesse tracce, stessi ID), due cartelle con lo stesso `album_id`, ricevuta corrotta poi riparata, `.maintenance` presente (l'indice **non cambia**) e poi rimosso, `.musiclib-store` assente (`unavailable`), `library/` vuota (album non disponibili ma **nessuna riga persa**), errore di elenco di una cartella artista (protegge i suoi album), arresto brusco a metà ciclo e riavvio (riprende, nessun duplicato), cento `Trigger` → un ciclo più uno in attesa, cambio simulato di `meta.ffmpeg_version` → nuove impronte **sulla stessa riga**, cambio di `collate_version`; tutto con `-race`.
- **Accettazione:** il revisore verifica che nessun percorso di codice cancelli righe di `artists`, `albums`, `tracks` (I3).

#### S9 — Cover e miniature

- **Persona:** ingegnere di elaborazione immagini e prestazioni.
- **Dipende da:** S7.
- **Leggere:** §9.2, I1, T20; `.ref/musiclib/internal/media/cover.go`.
- **Da fare:** `internal/covers`: servizio con `Open(ctx, album, size)` per `original|256|640` e la pipeline a–f del §9.2; `Warm(hash)` a concorrenza 1, collegato all'indicizzatore tramite `CoverWarmer`; `singleflight` e semaforo di capacità 2; limiti di 20 MiB e 40 megapixel; scrittura atomica; campi `cover_size` e `cover_mtime_ns` popolati dall'indicizzatore.
- **Da non fare:** handler HTTP (S16).
- **Test:** miniatura corretta (dimensioni, JPEG valido) da JPEG e PNG (anche con trasparenza, palette, 16 bit); non ingrandisce; l'hash dei byte letti viene verificato (file sostituito → errore `stale`); immagine oltre 40 megapixel rifiutata senza allocare (`DecodeConfig`); venti richieste concorrenti della stessa miniatura → **una** generazione; cache troncata o corrotta → rigenerata; cartella piena (errore di scrittura) → si serve l'originale; `Warm` dopo l'indicizzazione di un album con cover nuova.
- **Accettazione:** nessuna decodifica prima di `DecodeConfig`; nessuna scrittura non atomica nella cache.

#### S10 — Testi LRC

- **Persona:** ingegnere di parsing e fuzzing.
- **Dipende da:** S0.
- **Leggere:** §9.3, T17.
- **Da fare:** `internal/lyrics.Parse([]byte) Lyrics` con la grammatica del §9.3.
- **Da non fare:** handler HTTP (S16).
- **Test:** a tabella per **ogni** regola (BOM, `\n`/`\r\n`/`\r`, più timestamp, `offset` positivo e negativo, frazioni di 1–3 cifre, `hh:mm:ss`, tag di parola, righe vuote con tempo, nessun timestamp, tag di identificazione, righe senza tag in un file sincronizzato, oltre 10.000 righe); `FuzzParseLRC` (mai panic; uscita sempre ordinata; `time_ms ≥ 0`); invariante: `synced = false` ⇒ ogni `time_ms` è nullo.
- **Accettazione:** il revisore prova tre file LRC reali sporchi a sua scelta.
- **Fine fase A:** riepilogo all'utente (§0.11).

---

### Fase B — L'API

#### S11 — OpenAPI completa e pipeline di generazione

- **Persona:** progettista di API, esperto di OpenAPI.
- **Dipende da:** S2.
- **Leggere:** §8 (tutto: è la fonte), D5, D17, T23; `.ref/musiclib/docs/operations.md` (sezione «The API») per lo stile degli errori.
- **Da fare:** `api/openapi.yaml` (3.0.3) con **tutti** gli endpoint del §8.3 e gli `operationId` della tabella; `tags`; `security` (`cookieAuth`, `bearerAuth`; `security: []` dove l'accesso è pubblico); gli schemi del §8.2 con `additionalProperties: false` nelle richieste; parametri con enum e limiti; risposte di successo **e di errore** (componente `Error` riusata; header `X-Request-Id`, `ETag`, `Retry-After`, `Set-Cookie`); esempi per ogni schema principale; descrizioni che spiegano la semantica (idempotenza, `If-Match`, cursori, disponibilità); tutti i codici del §8.4. `api/oapi-codegen.yaml`; `scripts/generate.sh` (e `generate.sh diff` nel gate) con `oapi-codegen` fissato come direttiva `tool` di `go.mod`; codice generato committato in `internal/api`; server *strict* con **ogni** handler non implementato che risponde `501 not_implemented` (da togliere un passo alla volta fino a S20).
- **Da non fare:** logica degli handler.
- **Attenzione:** T23, T25 (formato delle date nella specifica e negli esempi); verifica che la versione scelta di `oapi-codegen` produca codice per `go 1.25.0`.
- **Test:** `TestSpecValid` (validazione di `kin-openapi`); `TestSpecCompleteness` (operationId univoci, `security`, almeno una risposta d'errore, esempi); `TestSpecMatchesDesign` (l'elenco degli `operationId` atteso è la tabella del §8.3, copiata nel test); `generate.sh diff` pulito.
- **Accettazione:** il revisore confronta la specifica con il §8 riga per riga (percorsi, metodi, campi, codici di stato).

#### S12 — Confine HTTP e modello degli errori

- **Persona:** ingegnere di sicurezza applicativa web.
- **Dipende da:** S11.
- **Leggere:** §7.6, §8.1, §8.4, I4, I5, T12, T14, T23, T28; `.ref/musiclib/internal/http/{api.go, errors.go, body.go}`.
- **Da fare:** `internal/httpx` con i middleware nell'ordine: ripristino dai panic → `X-Request-Id` → header di sicurezza → controllo `Host` (421) → controllo `Origin` (403) → header anti-CSRF (403) → limite del corpo (413) → JSON rigoroso (chiavi duplicate, valori multipli) → validazione OpenAPI (`kin-openapi`, `Servers` rimosso, autenticazione no-op) → (autenticazione: S13); mappa degli errori verso `{code, message, details}`; `httpx.Error{Status, Code, Details}` usato da tutti i servizi; 404 e 405 in JSON; log degli accessi (§11.5); cablaggio nel router generato; salute fuori dal controllo di `Host`; ogni wrapper di `ResponseWriter` implementa `Unwrap`.
- **Da non fare:** autenticazione (S13).
- **Attenzione:** T12, T14, T23, T28.
- **Test:** matrice `Host`/`Origin`/header su GET, POST, PUT, DELETE (compreso `Origin: null`); corpo di 1 MiB + 1 byte; JSON con chiave duplicata a ogni livello, chiave sconosciuta, due valori, tipo sbagliato; parametro con enum errato; `Content-Type` errato; un panic → `500 internal` senza dettagli; ogni errore ha `X-Request-Id`; nessun header CORS in nessuna risposta; il log non contiene query string né `Authorization`/`Cookie`; `FuzzStrictJSON`.
- **Accettazione:** il revisore prova a mano cinque richieste ostili e confronta le risposte con il §8.4.

#### S13 — Autenticazione: nucleo

- **Persona:** ingegnere di sicurezza, esperto di autenticazione e password.
- **Dipende da:** S3, S12.
- **Leggere:** §7 (tutto), I5, I6, T14, T15, T16, T28; `.ref/musiclib/internal/http/auth.go`.
- **Da fare:** `internal/auth`: PHC argon2id (codifica, decodifica, verifica; parametri sovrascrivibili nei test), token (`vb_` + 32 byte, hash SHA-256), `Service` (`Login`, `CreateToken`, `Authenticate` con rinnovo e limite di `last_used_at`, `Logout`, `ChangePassword`, `ListSessions`, `Revoke`, `CleanupExpired` all'avvio e ogni ora), semaforo di login (capacità 1, ritardo di 1 s sui rifiuti, hash fittizio per utente inesistente), bootstrap dell'admin (§7.4, variabili validate qui), middleware di autenticazione (cookie e bearer, il bearer vince) che mette nel contesto `Principal{UserID, Role, SessionID}`, sottocomando `vibrance user create|reset-password|list` (`--password-stdin`), query `sqlc` per utenti e sessioni.
- **Da non fare:** endpoint (S14).
- **Attenzione:** T14, T15, T16, T28.
- **Test:** vettori PHC noti, round trip e parametri di produzione; un token non compare in chiaro nel database; scadenza e rinnovo (clock iniettato); `last_used_at` aggiornato al più ogni 10 minuti; cambio password revoca le altre sessioni; utente disattivato → 401 subito; utente inesistente e password errata sono indistinguibili (stesso codice e stesso corpo; entrambi passano dallo stesso percorso e attendono 1 s); venti login concorrenti sono serializzati; bootstrap: senza utenti e senza password valida → uscita 2, con utenti le variabili si ignorano; CLI: password solo da standard input, `reset-password` revoca le sessioni e funziona a server acceso; `FuzzPHC`; **mutazioni:** salvare il token in chiaro, saltare la verifica del ruolo, non revocare al cambio password.
- **Accettazione:** il revisore cerca password, token e cookie in log, errori e risposte (I5).

#### S14 — Endpoint di autenticazione, account e amministrazione utenti

- **Persona:** ingegnere backend API con attenzione alla sicurezza.
- **Dipende da:** S13.
- **Leggere:** §7.3–7.5, §8.3 (auth, me, admin/users, server), §12.4, I6.
- **Da fare:** handler di `login`, `createToken`, `logout`, `getMe`, `changePassword`, `listSessions`, `revokeSession`, `listUsers`, `createUser`, `getUser`, `updateUser`, `deleteUser`, `resetUserPassword`, `getServerInfo`; regole `last_admin` e `cannot_modify_self`; cookie con i flag corretti; **matrice di autorizzazione** (§12.4) come test generato dalla specifica; helper `assertConforms` riusato dai passi successivi.
- **Da non fare:** altri endpoint.
- **Test:** ogni endpoint (successo e ogni errore del §8.3), risposte conformi alla specifica; flag del cookie con origine http e https (`Secure` solo con https); ultimo admin (retrocedere, disattivare, eliminare; due admin che si eliminano a vicenda in concorrenza: ne resta uno); cascata dell'eliminazione di un utente; sessione revocata inservibile; la matrice fallisce se si aggiunge un'operazione alla specifica senza una riga; **mutazioni:** togliere il controllo `last_admin`, togliere il controllo del ruolo admin.
- **Accettazione:** il revisore controlla che la matrice copra ogni operazione implementata finora.

#### S15 — Catalogo: artisti, album, tracce

- **Persona:** ingegnere backend API e database, esperto di paginazione a chiave.
- **Dipende da:** S7, S14.
- **Leggere:** §8.2, §8.3 (catalogo), §8.5, T9.
- **Da fare:** `internal/catalog` (servizi di lettura) e query `sqlc` (liste per i quattro ordinamenti × due direzioni, dettaglio dell'album, artista con album, traccia con `favorite`); codifica e decodifica rigorosa dei cursori in `httpx`; handler `listArtists`, `getArtist`, `listAlbums`, `getAlbum`, `getTrack`; costruzione di `Cover` (URL con hash); `AlbumDetail` con tracce ordinate per `(disc, number)`.
- **Da non fare:** ricerca, preferiti, playlist, media.
- **Attenzione:** T9, T25.
- **Test:** **proprietà**: per ogni ordinamento e direzione, la concatenazione delle pagine con limiti casuali (1, 2, 7, 50, 200) è **identica** all'elenco completo ordinato (dati casuali con titoli duplicati, anni nulli, Unicode), senza righe duplicate o saltate; cursore manomesso o di un altro ordinamento → `400 invalid_cursor`; filtro per artista; album non disponibili esclusi; traccia non disponibile → 200 con `available: false`; ordinamento Unicode e numerico («Track 2» prima di «Track 10», accenti); un test legge `EXPLAIN QUERY PLAN` di ogni query di lista e verifica che usi un indice; `FuzzCursor`; risposte conformi alla specifica.
- **Accettazione:** nessuna query usa `OFFSET`.

#### S16 — Endpoint media: audio, cover, testi

- **Persona:** ingegnere HTTP, esperto di cache, Range e streaming.
- **Dipende da:** S9, S10, S15.
- **Leggere:** §9.1–9.3, I1, I2, T11, T12, T13, T20.
- **Da fare:** handler `getTrackAudio`, `getAlbumCover`, `getTrackLyrics` come nel §9; guardia `(size, mtime)` con `Scanner.Trigger("stale")` e `503 library_changing`; disattivazione della scadenza di scrittura; intestazioni; `HEAD`.
- **Da non fare:** transcodifica; qualunque lettura da un percorso che non sia quello del database.
- **Attenzione:** T11, T12, T13, T20.
- **Test:** `Range` (inizio, fine, suffisso, non soddisfacibile → 416), `If-Range` con `ETag` giusto e sbagliato, `If-None-Match` → 304, `HEAD`, `Content-Type` per i quattro codec; **file sostituito** (cambio di file e `mtime` senza nuova scansione) → `503 library_changing` con `Retry-After`, scan avviato, e successo dopo lo scan; file eliminato; cartella album rinominata; streaming di un file da 50 MB con un client lento: nessuna scadenza di scrittura e nessuna goroutine che resta; cancellazione del client a metà; id non UUID o inesistente; `profile` diverso da `original` → 400; cover: `size` 256/640/`original`, `v` giusto → `immutable`, `v` sbagliato → `no-cache`, 304, album senza cover → 404, cover sostituita → 503 e scan; testi sincronizzati, non sincronizzati, assenti, sha diverso → 503; `-race`.
- **Accettazione:** il revisore verifica a mano con `curl -r` e `If-Range` un brano reale della libreria di prova.

#### S17 — Ricerca

- **Persona:** ingegnere di ricerca full-text su SQLite.
- **Dipende da:** S7, S15.
- **Leggere:** §10, T18, T19.
- **Da fare:** `internal/search`: costruzione della query (§10.2), esecuzione per tipo con ranking e idratazione; handler `search`; verifica che la manutenzione FTS5 già fatta da S7 e S8 copra comparsa, modifica, scomparsa e ritorno.
- **Da non fare:** tolleranza agli errori di battitura o altri motori.
- **Attenzione:** T18, T19.
- **Test:** accenti («beyonce» trova «Beyoncé»); maiuscole; prefissi mentre si digita («mil dav»); più token in AND; punteggiatura; query di soli simboli → risultati vuoti; 100 caratteri ok e 101 → 400; **`FuzzSearchQuery`** (mai errore SQL, mai panic, mai sintassi `MATCH` interpretata: `NEAR(`, `"`, `*`, `-`, `a OR b`, `title:x`); ordinamento per rilevanza deterministico; limite per tipo; tracce non disponibili assenti e di nuovo presenti al ritorno; CJK: un test fissa il comportamento reale (prefisso dall'inizio).
- **Accettazione:** il revisore prova dieci query ostili a sua scelta.

#### S18 — Preferiti

- **Persona:** ingegnere backend API.
- **Dipende da:** S15.
- **Leggere:** §8.3 (preferiti), §8.5, I6.
- **Da fare:** servizio e query (aggiunta e rimozione idempotenti, lista con cursore `(created_at, track_id)`), handler `listFavoriteTracks`, `addFavoriteTrack`, `removeFavoriteTrack`; `favorite` coerente in **tutte** le risposte che contengono una `Track`.
- **Test:** idempotenza; traccia inesistente → 404; utenti isolati; eliminare un utente elimina i suoi preferiti; le tracce non disponibili restano nella lista con `available: false`; dopo una modifica di una traccia (variante della libreria di prova + scan) il preferito resta sulla stessa traccia; ordine per più recente; paginazione; matrice di autorizzazione aggiornata.
- **Accettazione:** nessuna risposta con una `Track` ha `favorite` calcolato in modo diverso dalle altre.

#### S19 — Playlist

- **Persona:** ingegnere backend API e database.
- **Dipende da:** S15, S18.
- **Leggere:** §8.3, §8.6, I6, T5.
- **Da fare:** servizio e query; tutti gli endpoint playlist; parser di `If-Match` (accetta `W/`); limiti del §5.2; rinumerazione densa nella stessa transazione; `item_count` e `duration_ms` calcolati.
- **Da non fare:** condivisione, playlist intelligenti.
- **Attenzione:** T5.
- **Test:** CRUD; aggiunta in coda senza `If-Match`; inserimento con `position` senza `If-Match` → 428; `If-Match` vecchio → 412 **senza modifiche**; duplicati; ordine dopo insert, move e remove (tabella di casi **e** test di proprietà: dopo una sequenza casuale di operazioni le posizioni sono `0..n-1` e coincidono con un modello in memoria); due client con lo stesso ETag: uno vince, l'altro 412; tutto o niente con un `track_id` sconosciuto; 10.000 elementi ok e 10.001 → `too_many_items`; 500 playlist ok e 501 → `too_many_playlists`; proprietà (404 per gli altri, admin compreso); le tracce non disponibili restano con `available: false` e non contano in `duration_ms`; eliminare un utente elimina le sue playlist; matrice aggiornata; **mutazioni:** togliere il controllo di proprietà, togliere il controllo di `If-Match`.
- **Accettazione:** il revisore esegue a mano una sequenza di dieci operazioni e verifica posizioni e revisioni.

#### S20 — Stato della libreria, documentazione servita, completamento dell'API

- **Persona:** ingegnere backend.
- **Dipende da:** S8, S11, S14, S19.
- **Leggere:** §6.5, §8.7, §8.8, T23.
- **Da fare:** handler `getLibraryStatus` e `scanLibrary`; `GET /api/openapi.yaml` e `GET /api/docs` (un solo file JavaScript vendorizzato con versione e SHA-256 in `web/docs/VENDOR.md`; CSP propria; nessuna CDN); reindirizzamento di `/`; **rimozione di ogni `501` rimasto**; revisione finale della specifica (descrizioni, esempi); `docs/api.md` breve con esempi `curl` (login, token, album, audio con `Range`, playlist con `If-Match`).
- **Test:** stato durante e dopo un ciclo, e con `.maintenance`; `scan` coalescente; `/api/docs` e `openapi.yaml` senza autenticazione e con la CSP giusta; il documento servito è identico al file incorporato; **un test chiama ogni operazione della specifica con dati d'esempio e valida la risposta**; nessuna operazione risponde 501.
- **Accettazione:** il revisore apre `/api/docs` in un browser e prova un'operazione dalla pagina.
- **Fine fase B:** riepilogo all'utente (§0.11).

---

### Fase C — Operatività e contratto

#### S21 — Backup, ripristino, doctor

- **Persona:** ingegnere SRE e database.
- **Dipende da:** S3, S13.
- **Leggere:** §11.4; `.ref/musiclib/cmd/musiclibd/{backup.go, restore.go, doctor.go}` e `docs/operations.md` (sezioni «Backups, restore and moving» e «Maintenance»).
- **Da fare:** sottocomandi `backup`, `restore`, `doctor` come nel §11.4, con i codici d'uscita 0/1/2 e messaggi con `code` e `advice`.
- **Test:** backup a server acceso con scritture concorrenti (la copia è coerente: `integrity_check` e FTS ok); nome già esistente → 2; destinazione fuori da `/backup` → 2; manifesto e SHA-256; restore su stato vuoto → il server parte con gli **stessi utenti, playlist, preferiti e ID**; restore su stato esistente → 2; manifesto manomesso → 2; schema più nuovo → 2; `VACUUM INTO` preserva l'FTS (T6); `doctor` su database sano → 0, e su database con FTS fuori sincrono, conteggi errati, nessun admin abilitato → 1 con i rilievi giusti; un SIGKILL durante il backup non lascia una cartella con il nome definitivo.
- **Accettazione:** il revisore fa un backup e un restore completi su dati di prova e confronta le API prima e dopo.

#### S22 — Immagine e stack Compose

- **Persona:** ingegnere DevOps, esperto di Docker, Compose e Caddy.
- **Dipende da:** S2, S20, S21.
- **Leggere:** §3.6, §11.6–11.8, D15, T21, T22, T24; `.ref/musiclib/{compose.yaml, .env.example, Dockerfile}` e `docs/operations.md` («Install», «Access from other devices», «HTTPS and a domain name»).
- **Da fare:** `compose.yaml` (MusicLib invariato + `vibrance` + volumi), `env.example` (MusicLib + sezione Vibrance), `scripts/check-compose-sync.sh` (§11.7), `Dockerfile` `runtime` definitivo (`CMD serve`), `compose.caddy.yaml` e `Caddyfile.example` (pin per digest; plugin DNS con `dockerfile_inline`), `docs/operations.md` **in inglese, nello stile di quella di MusicLib**: requisiti, installazione con il blocco `curl` e `openssl` che genera le tre password, verifica, comandi quotidiani, configurazione (tabella delle variabili), accesso da altri dispositivi e Caddy con due nomi, backup e ripristino di **entrambi** i prodotti, aggiornamento (solo con le release di Vibrance: lo stack fissa la versione di MusicLib), adozione di un'installazione MusicLib esistente, risoluzione dei problemi con la tabella dei `code`; `scripts/stack-smoke.sh`.
- **Da non fare:** toccare il progetto `musiclib` dell'utente (§0.8).
- **Attenzione:** T21, T22, T24.
- **Test** (`scripts/stack-smoke.sh`, progetto `vibrance-contract`, volumi nuovi): installazione da zero **nei due ordini di avvio** (vibrance prima di app e viceversa): MusicLib parte senza `volume_permission`; Vibrance vede `library/` dopo il primo import; `docker compose stop app` non cambia la readiness né il servizio di Vibrance (I14); `docker compose config` valido; il controllo di sincronizzazione è verde e **diventa rosso** se si altera a mano una riga di un servizio di MusicLib; con le porte rimosse (`!reset`) e Caddy con `tls internal` su nomi `.localhost`.
- **Accettazione:** il revisore esegue `scripts/stack-smoke.sh` e legge `docs/operations.md` come se fosse l'utente.

#### S23 — Contratto end-to-end con MusicLib reale

- **Persona:** ingegnere QA end-to-end.
- **Dipende da:** S19, S22.
- **Leggere:** §4, §5.4, §12.3; `.ref/musiclib/docs/operations.md` («The API»).
- **Da fare:** `scripts/contract.sh` (progetto `vibrance-contract`, volumi nuovi, password casuali, pulizia finale **solo** di quel progetto) e test Go con etichetta `contract` (`internal/contract`): client minimale delle API di MusicLib (accesso, importazione, modifiche, cestino, `rebuild`) e di Vibrance, e **tutti** gli scenari A1–A16 del §12.3; `docs/compat.md` (MusicLib 1.1.0 ↔ Vibrance 0.1.0 e come si aggiorna la coppia).
- **Da non fare:** toccare il progetto `musiclib` dell'utente.
- **Test:** la tabella del §12.3; ogni scenario parte da playlist e preferiti già creati.
- **Accettazione:** tutti gli scenari verdi **due volte di seguito**; il revisore esegue `scripts/contract.sh` lui stesso.
- **Fine fase C:** riepilogo all'utente con i `TO CONFIRM` (§0.11).

---

### Fase D — Prestazioni e rilascio

#### S24 — Prestazioni

- **Persona:** ingegnere di performance.
- **Dipende da:** S23.
- **Leggere:** §6.7, §8.5, T3, T9, T30.
- **Da fare:** generatore di dataset sintetico (`internal/perfgen`, etichetta `perf`) che scrive nel database **20.000 album, 200.000 tracce, 2.000 artisti, 20 utenti, 500 playlist con 200 elementi ciascuna, 50.000 preferiti**, con nomi Unicode realistici; benchmark e test-soglia (`scripts/perf.sh`, fuori dal gate veloce); misura e correggi **solo** dove si sfora (indici, `cache_size`, `mmap_size`, forma delle query).
- **Budget** (p95, macchina di sviluppo, database caldo, un client): lista album di 50 righe ≤ 30 ms per ogni ordinamento, anche alla pagina 1.000; dettaglio album ≤ 20 ms; ricerca sui tre tipi ≤ 50 ms; preferiti (pagina) ≤ 30 ms; playlist da 10.000 elementi: pagina ≤ 30 ms, `add` e `move` ≤ 100 ms; avvio con un database da 200.000 tracce ≤ 3 s; ciclo di scansione **senza modifiche** su 10.000 cartelle album simulate (ricevute vere, tracce finte) ≤ 10 s su SSD; memoria a riposo ≤ 150 MB; nessuna lettura bloccata da una scrittura lunga dello scanner.
- **Accettazione:** tabella delle misure prima e dopo nel rapporto; ogni budget rispettato oppure una voce `TO CONFIRM` motivata.

#### S25 — Rilascio 0.1.0

- **Persona:** release engineer e revisore di sicurezza.
- **Dipende da:** S24.
- **Leggere:** D2, §11.6–11.7; `.ref/musiclib/{.github/workflows/release.yml, THIRD_PARTY_NOTICES.md, licenses/}` e `docs/docker.md` («Releasing»).
- **Da fare:** `.github/workflows/release.yml` adattato (guardia sul tag, gate, pubblicazione su `ghcr.io/tommasonovelli/vibrance:X.Y.Z`, release con `compose.yaml` ed `env.example`; il contratto lo esegue l'utente prima di taggare); `THIRD_PARTY_NOTICES.md` e `licenses/` (ffmpeg è nell'immagine: testi, avvisi e sorgenti corrispondenti come fa MusicLib; moduli Go compilati; il file JavaScript della documentazione); `CHANGELOG.md` (Keep a Changelog); `README.md` per l'utente (installazione passo passo nello stile di MusicLib); `SECURITY.md` con segnaposto per il contatto; `CONTRIBUTING.md` breve; **passata di sicurezza** (la skill `security-review` se disponibile, altrimenti una checklist su I1–I16, matrice di autorizzazione, cookie, log senza segreti, `govulncheck`); **controllo della toolchain**: leggi su https://go.dev/doc/devel/release quale Go è supportato e se MusicLib ha cambiato pin, e **proponi all'utente** di portare Vibrance a Go 1.27.x (un solo punto: `go.mod` e `ARG GO_IMAGE`; poi gate e contratto); segnala la disponibilità del nome «Vibrance» (pacchetto GHCR, repository) come verifica che spetta all'utente.
- **Da non fare:** **nessun `git push`, nessun tag, nessuna pubblicazione** (§0.8).
- **Accettazione:** gate, contratto e prestazioni verdi sull'albero finale; `docker build` del runtime; controllo di sincronizzazione del compose verde; `govulncheck` senza vulnerabilità raggiungibili; checklist di sicurezza compilata nel rapporto.
- **Rapporto finale dell'orchestratore all'utente** (§0.11): cosa è pronto; come provarlo (comandi); debiti aperti; `TO CONFIRM`; i passi che restano all'utente: creare il repository GitHub, pubblicare (`git push`, tag `v0.1.0`), rendere pubblico il pacchetto GHCR, **eseguire il gate e il contratto su Linux nativo** (il design lo richiede per il rilascio, come MusicLib), decidere la versione di Go e il nome.

---
## Appendice A — Prompt dell'ingegnere

L'orchestratore sostituisce i segnaposto `{…}` e passa il testo come prompt dell'agente (`general-purpose`). Non lo riassume e non aggiunge istruzioni sul *come*.

````text
Sei {PERSONA}. Lavori sul progetto Vibrance (la cartella di lavoro è la radice del repository).

ASSEGNAZIONE: il passo {ID} — {TITOLO} del §14 di DESIGN.md. Round {N} di 3.
{SOLO DAL ROUND 2:}
Il revisore ha chiesto queste correzioni bloccanti. Correggi l'albero di lavoro ESISTENTE (non ricominciare da capo) e risolvile tutte:
{RILIEVI BLOCCANTI, COPIATI ALLA LETTERA}

COSA LEGGERE, nell'ordine e per intero:
1. CLAUDE.md (se esiste).
2. DESIGN.md: §2 (principi, decisioni, invarianti), §13 (le trappole che il passo cita), e ogni sezione che il passo elenca sotto «Leggere». Il testo del passo {ID} nel §14 è il tuo contratto.
3. PROGRESS.md e NOTES.md.
4. Il codice già presente come modello di stile, e i file di .ref/musiclib che il passo cita (sola lettura: non modificarli mai).

REGOLE (non negoziabili):
- Il design è normativo e le sue decisioni sono finali. Implementa ciò che il passo chiede, per intero, e niente che non chieda (I16): nessuna funzione, opzione, dipendenza o astrazione in più.
- Quando il design tace: scegli la lettura più semplice e conservativa e registrala in NOTES.md come DECIDED. Quando è ambiguo in un modo che cambia il comportamento visibile: scegli l'opzione più conservativa, registrala come TO CONFIRM e prosegui.
- Se trovi una CONTRADDIZIONE o un'IPOTESI FALSA (qualcosa che il design dà per vero e non lo è, o un requisito impossibile): FERMATI. Non aggirarla. Scrivi nel rapporto una riga che inizia con "BLOCCO:" con le prove (comandi e output) e termina.
- Tutto gira in Docker: non installare nulla sull'host. Usa scripts/check.sh (il gate, sull'intero modulo) e scripts/dev.sh. Su Windows, Git Bash.
- Il repository è in inglese: codice, commenti, errori, log, test, documentazione. DESIGN.md resta in italiano.
- Mai toccare installazioni reali: usa solo i progetti Compose vibrance-dev, vibrance-spike e vibrance-contract; mai `docker compose down -v` né `docker volume rm` su altro.
- Non committare. Non modificare DESIGN.md né PROGRESS.md. Non modificare .ref/.
- Stile e invarianti: §2.3 e §2.5. Test veri dove il §12 lo chiede (SQLite vero, ffmpeg vero, processi veri); niente mock di SQL, filesystem o processi; percorsi di errore e concorrenza inclusi. Per le invarianti importanti fai i controlli per mutazione (rompi il codice e verifica che un test fallisca) e dillo nel rapporto.

DEFINITION OF DONE: le regole comuni del §14.1, più i punti «Accettazione» del passo. In particolare: scripts/check.sh passa; i test nuovi concorrenti con -race -count=20; i bersagli di fuzz nuovi per almeno 60 secondi; specifica e codice generato aggiornati; NOTES.md aggiornato.

RAPPORTO FINALE (in italiano, conciso), con questi titoli:
1. Esito: COMPLETATO oppure BLOCCO.
2. Cosa ho fatto: file e componenti principali, collegamento alle sezioni del design.
3. Verifiche: i comandi esatti eseguiti e il loro esito (incolla le ultime righe del gate).
4. Mutazioni provate: quali e quale test è fallito.
5. Note aggiunte a NOTES.md: id e titolo (DECIDED / TO CONFIRM).
6. Deviazioni dal passo, rischi, cose lasciate aperte.
````

---

## Appendice B — Prompt del revisore

Stessa persona dell'ingegnere: ora è il suo pari che ne controlla il lavoro. Agente **nuovo**, `general-purpose`.

````text
Sei {PERSONA}, nel ruolo di REVISORE pari. Lavori sul progetto Vibrance (la cartella di lavoro è la radice del repository). Non hai scritto questo codice e non gli devi lealtà. NON modificare il codice. NON committare.

COSA REVISIONI: le modifiche non committate nell'albero di lavoro (`git status`, `git diff`, e i file nuovi non tracciati: leggili per intero). Il lavoro risponde al passo {ID} — {TITOLO} del §14 di DESIGN.md, round {N}. {DAL ROUND 2: l'ingegnere doveva risolvere questi rilievi bloccanti: {ELENCO}. Verifica per prima cosa che lo siano.}

COSA LEGGERE: CLAUDE.md; DESIGN.md §2 (invarianti I1–I16), §13 (trappole citate dal passo), il testo del passo {ID} e le sezioni che cita; PROGRESS.md e NOTES.md. Il rapporto dell'ingegnere, se lo hai, è solo un indice di dove guardare: non è una prova.

COSA CONTROLLARE, in ordine di importanza:
1. Correttezza rispetto al design: ogni requisito del passo e delle sezioni citate è realizzato, nessuno è contraddetto, e non c'è nulla che il design non chieda (I16: funzioni, dipendenze, opzioni in più). Le deviazioni devono stare in NOTES.md.
2. Bug veri: errori ignorati (Close, Rollback, Commit, Rows.Err, rename), corse, cancellazione del context, perdite di risorse, ambito delle transazioni (nessun I/O dentro una transazione di scrittura, I11), confinamento (tutta la lettura della libreria passa da os.Root, I1/I2), processi esterni (context, timeout, fd, I8).
3. Sicurezza: segreti in log o risposte (I5), controlli di Host/Origin/header (I4), autorizzazione e matrice (I6), cookie, SQL costruito da input (I7).
4. Test: esistono, coprono il contratto, i percorsi di errore e la concorrenza, usano la cosa vera dove il §12 lo chiede, e FALLIREBBERO se l'invariante si rompesse. Prova tu stesso almeno una mutazione sulle invarianti del passo (rompi il codice in una copia di lavoro temporanea, vedi fallire un test, ripristina l'albero esattamente com'era).
5. Regole del progetto: inglese; pin esatti e digest, mai `latest` (I12); codice generato aggiornato e committato; dipendenze solo dal §2.4 (le altre come TO CONFIRM); stile del §2.5; specifica OpenAPI coerente con il §8; documentazione aggiornata.

COME: esegui TU il gate (`scripts/check.sh`, intero modulo) e riporta l'esito esatto; non fidarti del rapporto dell'ingegnere. Per i passi che il testo indica, esegui anche le verifiche manuali elencate sotto «Accettazione». Sii rapido e mirato: è una revisione prima del commit, non una riscrittura. I nit (stile, nomi, piccole semplificazioni) si elencano ma NON bloccano.

VERDETTO: concludi con ESATTAMENTE una di queste righe (l'ultima riga del tuo messaggio):
VERDICT: APPROVED
VERDICT: CHANGES REQUIRED
Dopo CHANGES REQUIRED, PRIMA della riga del verdetto, elenca i rilievi bloccanti numerati: file:riga, cosa non va, perché (sezione del design o scenario di guasto concreto) e cosa deve ottenere la correzione. Elenca a parte i nit. Proponi infine una riga di oggetto di commit in inglese, all'imperativo (per esempio «Add internal/library: confined receipt reader and file classification»).
````

---

## Appendice C — Modelli di `PROGRESS.md` e `NOTES.md`

### C.1 `PROGRESS.md` (lo gestisce l'orchestratore)

```markdown
# Vibrance — avanzamento

Design: DESIGN.md (versione 0.1). Orchestratore: aggiorna questo file a ogni passo.

| Passo | Titolo | Stato | Round | Commit | Note |
|---|---|---|---|---|---|
| S0 | Bootstrap del repository e toolchain | todo | | | |
| S1 | Spike: le ipotesi del contratto con MusicLib reale | todo | | | |
| … (una riga per ogni passo da S0 a S25) | | | | | |

Stati: `todo`, `in-progress`, `done`, `blocked`.

## Debiti (nit non bloccanti del revisore)
- (passo) descrizione

## TO CONFIRM aperti (da riportare all'utente a fine fase)
- N-0xx (passo) sintesi

## Errata al design
Vedi la sezione «Errata» in fondo a DESIGN.md.
```

### C.2 `NOTES.md` (lo aggiornano gli ingegneri)

Un registro breve di decisioni, deviazioni, rischi e domande. Una voce per argomento, 2–5 righe, mai un diario.

```markdown
## N-001 · Titolo breve — DECIDED | TO CONFIRM | RESOLVED
Passo: S7. Contesto: cosa non diceva o contraddiceva il design. Scelta: cosa si è fatto.
Motivo: perché è la lettura più semplice o conservativa. Effetto visibile: nessuno | quale.
```

Le voci `TO CONFIRM` si riportano all'utente (§0.11) e, una volta decise, diventano `RESOLVED`.

---

## Appendice D — Ricerca e idee future

### D.1 Cosa abbiamo imparato dagli altri

- **Navidrome** (Go, SQLite, FTS5, `goose`, TagLib): dalla 0.55 usa **ID persistenti** calcolati dai metadati (non dai percorsi) per non perdere playlist e preferiti; nota che «modificare i tag e spostare i file nella stessa scansione» non si riconcilia. Da qui D6 e le fasi F1–F3 del §5.4. Le sue cache sono *in-process* (disco per miniature e transcodifica, memoria con TTL): precedente per D3. Il *folder hash* per le scansioni rapide è l'analogo del nostro `receipt_hash`.
- **Jellyfin:** gli ID derivano dal percorso, e spostare i file fa perdere «visto», preferiti e playlist (segnalazioni aperte). Nelle discussioni sul passaggio a PostgreSQL e Valkey un maintainer osserva che il collo di bottiglia reale erano le query generate (un prodotto cartesiano di EF Core), non SQLite. Ha **BlurHash** per segnaposto delle immagini e, dalla 10.9, la normalizzazione del volume con LUFS.
- **Plex/Plexamp:** database SQLite con strumenti di riparazione dedicati (corruzioni frequenti: da qui WAL, `synchronous=FULL`, backup verificati, disco locale); analisi del volume e «sonic analysis» come prerequisito di livellamento, dissolvenze e forme d'onda.
- **Polaris:** API documentata con OpenAPI e servita con l'app; negoziazione di versione dell'API con header; miniature «tiny» 40×40; dati di forma d'onda; tag multivalore; dichiara di aver scelto un protocollo proprio invece di Subsonic: la stessa filosofia di questo progetto.
- **Gonic:** SQLite, scansione incrementale di ~50.000 tracce in pochi secondi contro ~10 minuti della prima: l'ordine di grandezza da inseguire per il ciclo senza modifiche.
- **Funkwhale:** Django + PostgreSQL + **Redis + Celery**: Redis serve per la coda dei lavori e la cache della sua architettura Python; per un server Go con SQLite in-process non esiste quel problema (D3).
- **OpenSubsonic:** le estensioni (testi sincronizzati con più lingue, offset di transcodifica, rapporto di riproduzione, coda con indice, somiglianza sonora) sono un elenco di ciò che i client moderni chiedono: utile per la roadmap, non da imitare.

### D.2 Redis?

**No** (D3). Motivi: SQLite in-process risponde con chiamate di funzione (la documentazione di SQLite misura oltre 200 query per pagina in meno di 25 ms); Redis aggiunge un salto di rete, un secondo archivio e il problema dell'invalidazione; ciò che di solito si mette in cache qui non ne ha bisogno (miniature: cache su disco; sessioni: una lettura per chiave primaria; liste: indici e paginazione a chiave). Se S24 mostrasse un punto caldo, si aggiungerà una cache *in-process* (come fa Navidrome), mai un servizio.

### D.3 Idee rimandate (nessuna nella v0.1)

| Idea | Note |
|---|---|
| **Transcodifica** (ALAC→FLAC senza perdita, AAC/Opus a bitrate fisso) | Prima aggiunta naturale: `profile` è già riservato (D10). Profili predefiniti, cache per `(sha256, profilo)`, semaforo sui processi. |
| **ThumbHash** (o BlurHash) per le cover | Campo `cover.placeholder` aggiuntivo e nullable; si calcola insieme alle miniature. ThumbHash codifica anche le proporzioni in ~25 byte. |
| **Colore dominante** della cover | Il client web potrebbe tingere la pagina dell'album come fa l'interfaccia di MusicLib. |
| **ReplayGain / LUFS** calcolati da noi | v0.1 legge solo i tag che MusicLib conserva; un'analisi EBU R128 (`ebur128`) può riempire i campi mancanti. Riferimento: ReplayGain 2.0 (−18 LUFS). |
| **Forma d'onda** (picchi precalcolati) | Come Plexamp e Polaris; serve ai client per la barra di avanzamento. |
| **Gapless** vero | Richiede informazioni di inizio e fine campioni (Xing/LAME, elst) esposte dall'API. |
| **Tolleranza ai refusi** nella ricerca | Distanza di edit sui nomi di artisti e album in memoria, come «Forse cercavi…»; oppure tokenizer `trigram` per sottostringhe e CJK. |
| **Typesense** | Dietro l'interfaccia di `internal/search`; un container in più e una copia dei dati da tenere allineata. |
| **Preferiti di album e artisti**, cronologia di ascolto, scrobbling | Dati personali nuovi: decidere privacy e retention prima. |
| **Playlist condivise** | `/playlists` (non `/me/playlists`) lascia già spazio a un campo di visibilità. |
| **Adattatore OpenSubsonic** | Il dominio resta pulito proprio per renderlo possibile senza condizionare l'API. |
| **`fsnotify`** | Solo come suggerimento per anticipare un ciclo (il ciclo a livello resta la verità). |
| **Firma `(inode, mtime)` delle cartelle album** | Per saltare la lettura delle ricevute nei cicli senza modifiche, solo se S24 la giustifica. |
| **Interfaccia web** | Fase successiva; il router ha già il punto di innesto (D20); la regola è che usi solo endpoint pubblici, così la v0.1 prova che l'API basta anche al client mobile. |
| **Sommario di versione dell'API** via header (`Accept-Version`) | Se mai servisse oltre `/api/v1`. |

### D.4 Fonti

- Navidrome, ID persistenti: https://www.navidrome.org/docs/usage/configuration/persistent-ids/ — release 0.55: https://github.com/navidrome/navidrome/releases/tag/v0.55.0 — panoramica dell'architettura: https://appselfhost.com/repo-insight-navidrome-webassembly-plugins-sqlite-storage-and-subsonic-api-in-one-lean-go-binary/
- Jellyfin, watched perso spostando i file: https://github.com/jellyfin/jellyfin/issues/18238 — SQLite, PostgreSQL e Valkey: https://github.com/orgs/jellyfin/discussions/17211 — BlurHash: https://github.com/jellyfin/jellyfin/pull/2676 — normalizzazione del volume: https://github.com/jellyfin/jellyfin-web/pull/4318
- Plex, riparazione del database: https://support.plex.tv/articles/repair-a-corrupted-database/ — Plexamp: https://www.plex.tv/plexamp/ — Plexamp v3: https://medium.com/plexlabs/plexamp-v3-9af3b10063b4
- Polaris: https://github.com/agersant/polaris — changelog: https://github.com/agersant/polaris/blob/master/CHANGELOG.md
- Gonic: https://git.sr.ht/~ser/gonic
- Funkwhale, architettura: https://docs.funkwhale.audio/developer/architecture.html
- OpenSubsonic, estensioni: https://opensubsonic.netlify.app/docs/extensions/
- ReplayGain 2.0: https://wiki.hydrogenaudio.org/index.php?title=ReplayGain_2.0_specification
- ThumbHash: https://evanw.github.io/thumbhash/
- *Choose Boring Technology*: https://mcfunley.com/choose-boring-technology
- *Level Triggering and Reconciliation in Kubernetes*: https://hackernoon.com/level-triggering-and-reconciliation-in-kubernetes-1f17fe30333d
- *Crash-Only Software*: https://www.usenix.org/legacy/events/hotos03/tech/full_papers/candea/candea_html/index.html
- SQLite, molte query piccole: https://www.sqlite.org/np1queryprob.html — quando usarlo: https://www.sqlite.org/whentouse.html
- Politica di rilascio di Go: https://go.dev/doc/devel/release

---

## Errata

*(L'orchestratore aggiunge qui, con data, passo e testo, le chiarificazioni decise secondo il §0.10. Non si modifica il testo sopra.)*

- **2026-10-01 · S4 · firma di `Discover`.** Il passo S4 scrive `Discover(ctx, root)`; la firma realizzata e approvata è `Discover(ctx, root, registered)`, dove `registered` è la mappa `album_id → rel_path` delle cartelle già presenti nell'indice. Serve al secondo spareggio del §6.2 («a parità, quella già registrata nell'indice»): `Discover` non accede al database, la mappa la fornisce il chiamante (lo scanner di S8). Nessun effetto su API, invarianti, scope o decisioni (NOTES.md N-032).

- **2026-10-02 · §0 · due regole date dall'utente all'orchestratore.**
  1. *Il design è una linea guida ferrea ma non immutabile.* Quando un ingegnere o un revisore mostra che applicare il design alla lettera rende il codice meno robusto, l'orchestratore può correggere il design senza fermarsi: aggiunge qui una voce datata, la fa realizzare da un ingegnere e controllare da un revisore, e la riporta all'utente nel riepilogo. Questo allarga il §0.8 e il §0.10 solo per robustezza e correttezza di ciò che il design già vuole; per funzioni nuove, cambi di scope (§1.3) e azioni esterne (push, tag, installazioni reali) vale ancora «fermati e chiedi».
  2. *Modello degli agenti.* Ingegneri e revisori si lanciano con il tipo di agente `vibrance-worker` (file `.claude/agents/vibrance-worker.md`, escluso da git: modello Opus 5.5, effort `medium`), non più `general-purpose` (§0.4, §0.5). I prompt delle Appendici A e B restano la fonte di verità e non cambiano. Se il tipo non è disponibile nella sessione, chiedere all'utente di riavviarla o di impostare l'effort della sessione a `medium`.

- **2026-10-02 · S6 · §5.4, il planner (sostituisce le regole d'ordine di F1 e F2 e fissa ciò che il testo lasciava aperto).** Motivo: con le regole originali due tracce con lo stesso audio potevano scambiarsi l'ID dopo la cancellazione di una e la rinumerazione dell'altra, e una riga con `fp_version` vecchia perdeva l'ID anche quando l'impronta era identica (NOTES.md N-046, N-047, N-048; parere del revisore di S6).
  1. **Firme.** `NeedFingerprint(olds, news) []int` e `Reconcile(olds, news, fpVersion) (Plan, error)`, dove `fpVersion` è la versione corrente di ffmpeg. Il piano non contiene ID nuovi: li assegna chi lo applica (l'indicizzatore di S7).
  2. **F1.** Se più righe o più file hanno lo stesso `file_sha256`: gli `old` disponibili prima dei non disponibili, poi per `rel_path` crescente; i `new` per `rel_path` crescente. `(disc, no)` **non** entra più nell'ordine di F1: così `NeedFingerprint` dipende solo dai dati della ricevuta e dell'indice e si può chiamare prima di ogni `ffprobe` (§6.3 passo 4).
  3. **F2, uguaglianza.** Un `new` si abbina a un `old` con la stessa impronta **qualunque sia la `fp_version` dell'`old`**: due impronte uguali sono lo stesso audio, chiunque le abbia calcolate. La riga abbinata prende la `fp_version` corrente e conserva la sua `occurrence`.
  4. **F2, ordine.** Dentro lo stesso gruppo di impronta si abbinano **prima gli `old` disponibili, poi quelli non disponibili**. In ciascuno dei due giri: prima le coppie con `(disc, no)` uguali (a parità, `occurrence` crescente), poi il resto per ordine crescente di `(disc, no, rel_path)` dei `new` e di `occurrence` degli `old`.
  5. **F3.** Resta l'ultima risorsa, solo per gli `old` con `fp_version` diversa dalla corrente che F2 non ha abbinato. Ordine: `(disc, no, rel_path)` dei `new`; a parità di posto fra gli `old`, il disponibile prima. Se l'impronta della riga cambia, la riga prende come `occurrence` la più piccola libera per la nuova impronta, con la regola di F4, e mai una che un'altra riga dell'album aveva già.
  6. **Limiti noti, accettati.** (a) Due gemelle entrambe disponibili e un solo file con quell'audio alla scansione dopo (una cancellata e l'altra rinumerata nello stesso intervallo): non si può sapere quale delle due sia rimasta. (b) F3 può abbinare una traccia nuova a una riga vecchia con stesso disco, numero, codec e durata al millisecondo. (c) F3 confronta la durata esatta: se una versione futura di `ffprobe` arrotondasse diversamente, F3 non abbinerebbe. Nessuna tolleranza: aumenterebbe i falsi positivi.
  7. **Test da aggiungere.** Dopo A14, la traccia rimasta conserva il suo ID sia quando va a un numero libero sia quando prende il numero della gemella cancellata; una riga non disponibile con `fp_version` vecchia che torna spostata con la stessa impronta conserva l'ID (F2); gli ordini dei punti 2, 4 e 5 sono fissati da test che falliscono se l'ordine cambia; le proprietà del passo S6 (un `old` mai abbinato a due `new`, nessun `old` perso, `occurrence` unica, idempotenza, indipendenza dall'ordine degli argomenti) valgono con le regole nuove.

- **2026-10-02 · S8 · §6.6, nuove impronte al cambio di versione di ffmpeg.** (1) Il lavoro parte quando esiste una riga **disponibile** con `fp_version` diversa dalla corrente, non solo quando `meta.ffmpeg_version` è diversa: una riga tornata disponibile dopo la fine del lavoro non deve restare con la versione vecchia. (2) Se la coppia `(fingerprint, occurrence)` nuova è già occupata da un'altra riga dell'album, la riga prende la più piccola `occurrence` libera, come nel punto 5 dell'errata al §5.4.

- **2026-10-02 · S8, S11, S18, S19, S23 · §5.4, §6.1, §8.6: i riferimenti seguono l'audio (decisione dell'utente dopo MusicLib 1.2.0).** Motivo: MusicLib 1.2.0 sposta tracce da un album a un altro (`POST /api/albums/<id>/move-tracks`; un album rimasto vuoto va nel cestino e non si ripristina) e dopo «Empty trash» un album può tornare solo con un `album_id` nuovo. Con l'identità valida dentro un album (§5.4) la traccia spostata nasce come riga nuova e playlist e preferiti restano sulla riga vecchia, in grigio. L'utente vuole che l'elemento resti al suo posto nella playlist e mostri i dati nuovi (titolo, artista, album, cover), accettando che sotto cambi l'identità della traccia.
  1. **Regola.** Il §5.4 e il planner non cambiano: l'identità di una traccia resta `(album_id, fingerprint, occurrence)`. Si aggiunge una sola regola, globale: **se una riga di `tracks` non disponibile ha riferimenti degli utenti ed esiste una riga disponibile con la stessa `fingerprint`, i riferimenti passano a quella riga.** Vale qualunque sia l'album e qualunque sia la `fp_version` delle due righe (due impronte uguali sono lo stesso audio, come nel punto 3 dell'errata al §5.4).
  2. **Quale riga.** Se le righe disponibili con quella impronta sono più d'una: prima quella dello **stesso album** della riga vecchia, poi quella con `seq` maggiore (la più recente: è la traccia appena spostata). La scelta è deterministica.
  3. **Quando.** Nel passo P6 di **ogni** ciclo che ha superato P0 (anche se il ciclo non ha cambiato nulla: così un arresto fra il commit di un album e P6 si ripara al ciclo dopo). Una sola transazione di scrittura, breve, senza I/O (I11). La regola guarda solo lo stato del database: è idempotente, non dipende dall'ordine in cui gli album sono stati indicizzati né da quanti cicli sono passati.
  4. **Cosa scrive.** `playlist_items`: cambia `track_id`; `id`, `position` e `added_at` restano. Le playlist toccate aumentano `revision` e aggiornano `updated_at` (§8.6: è una modifica degli elementi; chi ha la playlist aperta riceve `412` al prossimo salvataggio condizionato e la ricarica). `favorites`: la riga passa alla traccia nuova con lo stesso `created_at`; se l'utente aveva già quella traccia fra i preferiti resta la riga esistente e la vecchia si toglie. Tutto l'SQL sta in `sql/` (I7).
  5. **Cosa non cambia.** Nessuna riga di `tracks` si cancella e nessun ID cambia (I3); solo lo scanner tocca `available` (I15). La riga vecchia resta non disponibile, senza riferimenti. Se un giorno torna disponibile (stesso album ripristinato) non riprende i riferimenti passati a un'altra riga: restano dove sono, sullo stesso audio. È l'unico punto in cui lo scanner scrive nelle tabelle degli utenti.
  6. **Schema.** Indice `tracks(fingerprint)`, aggiunto dal passo S8 (I13: prima del rilascio le migrazioni si possono ancora correggere). S24 verifica il piano delle query di P6 con `EXPLAIN QUERY PLAN` e aggiunge altri indici solo dopo una misura.
  7. **API (S11, S18, S19).** La specifica dichiara: l'`id` di un elemento di playlist, la sua `position` e `added_at` sono stabili; `track` di un elemento e di un preferito può diventare un'altra traccia con lo stesso audio quando in MusicLib la traccia cambia album. L'ID vecchio resta leggibile con `GET /tracks/{id}` (`available: false`).
  8. **Perché non spostare la riga da un album all'altro** (stesso ID, `album_id` nuovo). MusicLib scrive i due album con due render distinti, senza ordine garantito (sorgenti di MusicLib 1.2.0, `internal/catalog/move.go`:, due `EnqueueRender` nella stessa transazione, coda ordinata per `queued_at, id`, più worker). Una scansione può quindi vedere la traccia **in entrambi** gli album, o in nessuno. Nel primo caso la riga nuova esiste già quando la vecchia sparisce: per conservare l'ID servirebbe cancellare una riga (contro I3) o ordinare l'indicizzazione degli album e tenerne alcuni in sospeso. La regola del punto 1 non ha questi casi: guarda lo stato finale.
  9. **Effetti voluti.** (a) Spostamento di una traccia, di tutte (album nel cestino), scambio di tracce fra due album: playlist e preferiti seguono. (b) Album cancellato con «Empty trash» e importato di nuovo: i riferimenti passano alle righe nuove. (c) Stesso audio in due album e uno viene cancellato: i riferimenti passano alla copia rimasta, invece di restare in grigio. (d) Il limite noto (a) dell'errata al §5.4 (due gemelle, ne resta una) diventa innocuo: qualunque riga il planner tenga, i riferimenti finiscono su quella disponibile.
  10. **Limiti noti, accettati.** L'ID della traccia cambia quando la traccia cambia album: un client che lo ha in memoria (coda di riproduzione) lo trova non disponibile e deve rileggere la playlist. Durante un ciclo lungo i riferimenti si aggiornano alla fine del ciclo, non al commit dell'album. Se MusicLib cambiasse i pacchetti audio nello spostamento l'impronta sarebbe diversa e il riferimento resterebbe in grigio (mai su un audio sbagliato): lo verifica il round di passaggio a MusicLib 1.2.0 con lo spike, e S23.
  11. **Test, passo S8.** Sulla copia della libreria di prova, con playlist e preferiti creati prima: traccia spostata in un altro album con titolo e artista cambiati → l'elemento ha lo stesso `id` e la stessa `position`, mostra i dati nuovi, `revision` aumentata; lo stesso con l'album di destinazione indicizzato **prima** e **dopo** quello di origine, e con un ciclo in mezzo che vede la traccia in entrambi gli album; tutte le tracce spostate (album di origine assente); scambio fra due album; album sparito e tornato con `album_id` nuovo; copia in due album e una cancellata; preferito già presente sulla traccia di destinazione (ne resta uno); più righe disponibili con la stessa impronta (regola del punto 2); nessuna riga disponibile con quell'impronta → nulla cambia; secondo ciclo → nessuna scrittura (idempotenza); arresto prima di P6 e riavvio. Un test elenca con `PRAGMA foreign_key_list` le tabelle che puntano a `tracks(id)` e fallisce se una non è coperta da P6. **Mutazioni:** togliere il passo di P6, togliere la condizione `available`, invertire la scelta del punto 2 devono far fallire almeno un test.
  12. **Test, passo S23** (MusicLib reale 1.2.0), righe nuove della tabella del §12.3: **A17** sposta una traccia in un altro album e ne cambia titolo e artista → l'elemento di playlist resta alla stessa posizione con i dati nuovi, il preferito resta; **A18** sposta tutte le tracce di un album → come A17 per ognuna, l'album di origine sparisce dalle liste; **A19** scambia due tracce fra due album → entrambe seguono; **A20** cestino, «Empty trash», nuova importazione della stessa cartella → playlist e preferiti tornano sulle tracce nuove.

- **2026-10-02 · S6e · §5.4, un limite noto in più (si aggiunge al punto 6 dell'errata al planner).** (d) Due gemelle **entrambe non disponibili** e un solo file con quell'audio che torna riscritto o spostato (per esempio: dopo A14 la traccia rimasta prende il numero della gemella cancellata, poi l'album va nel cestino e torna con quel file riscritto): F2 lo dà alla riga con `occurrence` minore, che può essere quella cancellata da più tempo. Una riga non dice quando è stata vista l'ultima volta, e il planner non può saperlo. Con byte identici non accade (F1). Dal passo S8 è innocuo per gli utenti: i riferimenti seguono l'audio (NOTES.md N-050; parere del revisore di S6e).

- **2026-10-02 · S6m (passo nuovo, dopo S6e e prima di S7) · passaggio a MusicLib 1.2.0.** Motivo: l'utente ha pubblicato MusicLib 1.2.0 (cestino che si svuota, tracce aggiunte a un album, tracce spostate in un altro album). Ovunque il testo sopra dica «MusicLib 1.1.0» (D15, D18, §0.2, §3.6, §4, §12.2, passi S0, S1, S23) si legge **1.2.0**. L'orchestratore ha già sostituito `.ref/musiclib` con il clone del tag `v1.2.0`.
  - **Persona:** ingegnere di integrazione e QA, esperto di formati audio e di ffmpeg.
  - **Leggere:** §3.6, §4 (tutto), §5.4, §12.2, i passi S0 e S1, le voci d'errata «i riferimenti seguono l'audio» e questa; `.ref/musiclib/{CHANGELOG.md, Dockerfile, compose.yaml}`, `docs/operations.md` (aggiunta e spostamento di tracce, cestino), `internal/catalog/move.go`.
  - **Da fare:**
    1. `Dockerfile`: `ffmpeg`/`ffprobe` copiati da `ghcr.io/tommasonovelli/musiclib:1.2.0@sha256:…` (digest risolto con `docker buildx imagetools inspect`; se l'immagine non è scaricabile: **BLOCCO**). Tutti gli altri pin (Go, base Debian, frontend del Dockerfile, PostgreSQL del compose dello spike) si confrontano con quelli di `.ref/musiclib` 1.2.0 e si allineano se sono cambiati (I12).
    2. Gli strumenti: se `ffmpeg -version` dell'immagine nuova è ancora `8.1.3-musiclib1`, nulla cambia nel codice. Se è diversa, è la versione nuova a essere fissata (`media.PinnedVersion`, test, documenti) e va scritto in NOTES.md.
    3. `scripts/spike.sh` e `scripts/spike/` girano contro MusicLib 1.2.0 e rigenerano `docs/spike-report.md`: H1–H9 si riverificano tutte. Si aggiungono: **H10** l'impronta di una traccia è **identica prima e dopo lo spostamento in un altro album** (`POST /api/albums/<id>/move-tracks`), anche con titolo e artista cambiati dopo lo spostamento, per FLAC, MP3, AAC e ALAC; **H11** dopo lo spostamento di **tutte** le tracce la cartella dell'album di origine sparisce da `library/`, e dopo lo spostamento di una parte la ricevuta dell'origine non elenca più il file spostato e quella di destinazione lo elenca; **H12** l'impronta delle tracce già presenti è identica dopo l'aggiunta di una traccia all'album (`POST /api/albums/<id>/tracks`). Si registra anche, come osservazione senza esito, in quale ordine MusicLib pubblica i due album dopo uno spostamento.
    4. La libreria di prova: se la `render_version` che MusicLib 1.2.0 scrive è **uguale** a quella di `testdata/FIXTURE.md`, `testdata/library-v1/` resta com'è e `FIXTURE.md` dichiara che 1.2.0 produce la stessa `render_version` (con la prova). Se è diversa, si rigenera con `scripts/make-fixture-library.sh` e si adattano i test che fissano valori della libreria, senza toglierne.
    5. Documenti e commenti che nominano la versione provata: `CLAUDE.md` e `AGENTS.md` (identici), `README.md`, `docs/compat.md` (versione obiettivo 1.2.0), `scripts/` e i commenti del codice che dicono «MusicLib 1.1.0» come versione provata; `NOTES.md` (una nota per ciò che il design non dice).
  - **Da non fare:** nessun codice di prodotto oltre ai pin; la regola «i riferimenti seguono l'audio» è del passo S8; nessun progetto Compose diverso da `vibrance-spike` e `vibrance-dev`.
  - **Accettazione:** `scripts/check.sh` verde; `scripts/lint-shell.sh` pulito; `docs/spike-report.md` rigenerato con H1–H12. **BLOCCO** se H1 è irrisolvibile, o se H2, H5 o H10 sono false per qualche codec. Il revisore confronta i pin con `.ref/musiclib` e riesegue H10 sul codec più rischioso (MP3).
  - **Nota dell'orchestratore (2026-10-02, round 1 di S6m).** Il punto 5 chiede di aggiornare `CLAUDE.md` e `AGENTS.md`. La sessione dell'ingegnere non ammette modifiche a `CLAUDE.md` senza il consenso diretto dell'utente: le due righe che nominano MusicLib 1.1.0 in ciascun file restano com'erano (NOTES.md N-055) e la modifica è rimessa all'utente. Non blocca il passo.

- **2026-10-02 · S7 · un debito di S5 da chiudere nel passo S7.** `TestRunCancelKillsTheGroup` (`internal/media/runner_test.go`) ha una corsa **nel test**: lo script `family` pubblica il file dei pid con `mv`, che è un figlio dello stesso gruppo e può essere ancora vivo quando il test legge i membri del gruppo. È fallito una volta nel gate del passo S6m. L'ingegnere di S7 lo corregge, solo nel test: ripete la lettura dei membri finché coincide con i pid attesi, con una scadenza breve, e fallisce solo alla scadenza. L'asserzione resta «il gruppo contiene esattamente quei processi»; il codice di prodotto di `internal/media` non cambia.

- **2026-10-04 · S23 · §12.3, riga A16 (corretta: contraddiceva §6.1, §6.2 e I14).** A16 diventa: «`library/` momentaneamente illeggibile (rinominata dall'interno del container di MusicLib)» → atteso: stato `unavailable`, **indice intatto** (album e tracce restano come erano), audio `404 track_unavailable`, playlist e preferiti invariati; al ritorno tutto com'era, senza reindicizzazione. Motivo: il §6.2 dice che un errore sull'elenco della radice interrompe il ciclo senza toccare nulla; marcare tutto non disponibile per un guasto passeggero costerebbe una transazione per album, la riscrittura dell'FTS e una reindicizzazione completa al ritorno. Resta vero, ed è provato in S8, che una `library/` **vuota ma elencabile** rende gli album non disponibili senza perdere righe (NOTES.md N-066; parere del revisore di S8).

- **2026-10-04 · S8 · §11.2, due codici d'errore d'avvio in più.** `musiclib_folder`: la cartella `/musiclib` non esiste affatto (un montaggio mancante; una cartella vuota va bene, I14). `library_index`: il ricalcolo delle chiavi di ordinamento o l'avvio dello scanner falliscono. Entrambi fermano l'avvio con uscita diversa da zero, come gli altri codici del §11.2 (NOTES.md N-072).

- **2026-10-04 · S9 · §6.3, la cover si valida prima di esaminare i file audio.** Il passo 5 del §6.3 (apertura e validazione dell'intestazione della cover) si esegue **prima** del passo 4 (`ffprobe` e impronte). Motivo: una cover che non si apre ferma l'album, e l'album si riprova a ogni ciclo; scoperta dopo il passo 4, rilanciava tutti i processi dell'album a ogni ciclo, cioè ciò che il §6.2 vuole evitare (misura del revisore di S9: 6 processi per tentativo). L'ordine fra i due passi non ha altri effetti visibili. Che cosa fare di un album la cui cover non si apre (fuori dall'indice finché non si apre, oppure indicizzato senza cover con un avviso) è una scelta dell'utente, aperta in NOTES.md N-078.

- **2026-10-04 · S19 · §8.2 e §8.6, `item_count` di una playlist (i due paragrafi si contraddicevano).** Il §8.2 dice che `item_count` conta solo le tracce disponibili; il §8.6 lo usa come limite delle posizioni, che sono dense su **tutti** gli elementi. Con un elemento non disponibile le due regole non valgono insieme. Vale questa: **`item_count` conta tutti gli elementi della playlist, disponibili o no; `duration_ms` somma solo le tracce disponibili.** Così le posizioni valide restano `0..item_count` (inserimento) e `0..item_count-1` (spostamento), come scrive il §8.6, e un client ricava l'ultima posizione dalla sola `Playlist`. Il passo S19 corregge per prima cosa le due descrizioni in `api/openapi.yaml` (oggi seguono il §8.2), rigenera il codice e realizza gli handler su questa regola (NOTES.md N-088; parere del revisore di S11).

- **2026-10-04 · S12 · validazione OpenAPI senza `ValidateRequest` e senza `nethttp-middleware` (corregge il passo S12 e precisa il §2.4).** `openapi3filter.ValidateRequest` di `kin-openapi` 0.149.0, nel passo sui requisiti di sicurezza, legge in memoria **l'intero corpo senza limite** prima di chiamare la funzione di autenticazione, anche se questa non fa nulla (`validate_request.go`, `validateSecurityRequirement`; verificato dall'ingegnere con un test e dal revisore sul sorgente). `nethttp-middleware` chiama quella funzione e porta un secondo router. Perciò: (1) il confine HTTP valida con `ValidateParameter` e `ValidateRequestBody`, senza il passo di sicurezza del validatore; l'autenticazione è solo quella del middleware di S13; (2) l'operazione si trova con il router di net/http, non con quello di `kin-openapi`: non c'è un secondo router e la chiave `servers` non entra mai in gioco (lo scopo di T23 è raggiunto senza rimuoverla); (3) `github.com/oapi-codegen/nethttp-middleware` resta ammesso dal §2.4 ma non si usa. L'ordine dei controlli è quello del passo, con una precisazione: il binding dei parametri del codice generato gira prima del limite del corpo, quindi un parametro non valido dà 400 senza che il corpo sia letto (NOTES.md N-089, N-090, N-092).

- **2026-10-04 · S13 · §7.2, il costo di argon2id è un parametro del servizio.** Il §7.2 dice che i parametri di argon2id sono «sovrascrivibili nei test»; il costo si passa al costruttore del servizio di autenticazione invece di stare in una variabile di pacchetto, che sarebbe condivisa fra test paralleli. In produzione il server e il sottocomando `vibrance user` usano sempre il costo di produzione (64 MiB, 3 passate, 1 thread), verificato da un test sul processo vero. Il decodificatore PHC rifiuta, prima di calcolare, i costi oltre 256 MiB, 16 passate o 4 thread, perché un hash anomalo nel database terrebbe a lungo l'unico slot di calcolo (NOTES.md N-096, N-097).

- **2026-10-05 · S15 · §5.2, indice degli artisti.** Il §5.2 non elenca indici per `artists`, ma il §8.5 vuole che ogni query di lista usi un indice: senza, ogni pagina di `GET /artists` ordinerebbe tutti gli artisti. Lo schema ha anche `CREATE INDEX artists_sort_idx ON artists (sort_key, id);`, aggiunto dal passo S15 a `migrations/00001_schema.sql` (I13: prima del rilascio le migrazioni si possono ancora correggere, come per `tracks(fingerprint)` in S8; un database di sviluppo creato prima va ricreato). L'elenco del §5.2 resta quello degli indici obbligatori; S24 verifica i piani e aggiunge altri indici solo dopo una misura (NOTES.md N-111).

- **2026-10-05 · S16e (passo nuovo, dopo S16 e prima di S17) · §6.2, §9.1–9.3, §8.3: la guardia dei file deve poter guarire.** Motivo (revisore di S16, verificato sul server vero): il §9.1 dice che un disaccordo `(size, mtime)` avvia uno scan che lo ripara, ma lo scanner salta un album la cui ricevuta e il cui percorso non sono cambiati (P3, `internal/library/scanner.go`, `plan`), quindi `file_mtime_ns` non si aggiorna mai. Un file con solo l'`mtime` cambiato (copia o ripristino del volume che non conserva gli `mtime`, `touch`) risponde **503 per sempre** e ogni richiesta avvia un ciclo inutile; dopo un `cp -r` tutta la libreria diventa non riproducibile. Con MusicLib il caso non nasce (ogni render cambia `build_id`, quindi la ricevuta), ma un volume copiato è un'operazione normale. Regola nuova:
  1. **Ricontrollo di un album.** Lo scanner ha `Recheck(albumID)`: ricorda l'id in un insieme (protetto da mutex; contiene solo id di album indicizzati, quindi è limitato dal numero di album) e chiama `Trigger(ReasonFileReplaced)`. Gli endpoint media, in ogni caso in cui oggi rispondono `503 library_changing` e avviano uno scan, chiamano invece `Recheck` con l'`album_id` della riga letta dal database.
  2. **P3.** All'inizio della pianificazione il ciclo prende (e svuota) l'insieme. Un album dell'insieme che sarebbe saltato perché **aggiornato** (stessa ricevuta, stesso percorso, disponibile) diventa lavoro del ciclo. Un album ricordato come **fallito** con la stessa ricevuta resta saltato (N-064: niente processi rilanciati da un client). Un id dell'insieme che il ciclo non trova fra i candidati si scarta.
  3. **Effetto.** La reindicizzazione rifà il passo 2 del §6.3 (`Lstat`, dimensione della ricevuta) e F1 abbina ogni traccia per SHA senza lanciare processi; le righe abbinate prendono `file_size` e `file_mtime_ns` dal file attuale (§5.4, campi mutabili), la cover `cover_size` e `cover_mtime_ns`. Così un `mtime` cambiato guarisce in un ciclo, senza ffmpeg, e la richiesta dopo lo scan riceve il file. Nient'altro cambia: ID, preferiti, playlist e `added_at` restano (I3).
  4. **Limite noto.** Se i byte non coincidono davvero con la ricevuta (dimensione diversa → `file_size_mismatch`; testi con SHA diverso a parità di ricevuta) l'album resta com'era e la risposta resta `503` con scan a ogni richiesta, coalescente. MusicLib non produce questo stato (ogni modifica riscrive la ricevuta); lo si dice nel README fra i problemi della libreria.
  5. **Cover come audio (N-115).** Con `library/` assente, o con un link, una cartella o un permesso negato al posto del file della cover, `getAlbumCover` risponde `404 cover_not_found` **senza** scan, come `getTrackAudio` risponde `404 track_unavailable`: un volume assente non si ripara con uno scan, e oggi ogni richiesta di cover avvia un ciclo che finisce `unavailable`.
  6. **T13 fra `Lstat` e `Open`.** Un file sostituito fra il controllo e l'apertura (`errReplaced` di `internal/library`) è un file sostituito: `503 library_changing` e `Recheck`, come `ENOENT` con `library/` presente, non `404`.
  7. **§8.3, tabella.** `getTrackAudio`: anche `412` (un `If-Match` di un altro file); `getAlbumCover`: anche `206`, `412` e `416`, come la specifica di S16 già dichiara (NOTES.md N-116: ciò che `http.ServeContent` risponde).
  - **Persona:** ingegnere Go di sistemi, esperto di riconciliazione e di HTTP.
  - **Leggere:** §6.2, §6.3, §9.1–9.3, T13, I3, I11, I14, I15, l'errata ad A16 (2026-10-04 · S23) e questa; NOTES.md N-064, N-075, N-114…N-118.
  - **Da fare:** i punti 1, 2, 5 e 6 nel codice (`internal/library`, `internal/catalog`, `internal/api`, `internal/app`); la specifica e il README dove descrivono il 503 e il 404 dei file; NOTES.md.
  - **Da non fare:** nessun altro cambio dello scanner o della guardia; nessuno scan senza una richiesta che l'abbia visto; nessuna lettura dei file fuori da `os.Root`.
  - **Test:** sul server vero con lo scanner vero e la libreria di prova: (a) `touch` di un file audio a ricevuta invariata → 503 e `Retry-After`, un ciclo, poi 200 con lo stesso `ETag` e nessun processo lanciato (il runner conta le chiamate); lo stesso per la cover `original`; (b) un album fallito con la stessa ricevuta non si riprova per un `Recheck`; (c) `Recheck` di un album che non c'è più non rompe il ciclo; (d) cover con `library/` assente → 404 `cover_not_found` e nessun ciclo avviato; (e) file sostituito fra `Lstat` e `Open` (hook di test o file sostituito con rinomina) → 503; (f) dimensione diversa dalla ricevuta → resta 503 (limite noto); (g) `Recheck` concorrenti con `-race -count=20`. **Mutazioni:** P3 che ignora l'insieme (il test (a) fallisce); `Recheck` che riprova anche gli album falliti (il test (b) fallisce).
  - **Accettazione:** `scripts/check.sh` verde; il revisore ripete a mano il caso (a) con `touch -d` sul server vero.
  - **Precisazioni dopo la revisione (2026-10-05, revisore di S16e).** (i) Punto 3: l'abbinamento F1 è per lo SHA-256 **scritto nella ricevuta**; i byte non si rileggono. Un file modificato a mano con byte diversi e la stessa dimensione, a ricevuta invariata (per esempio un editor di tag che riscrive nel padding di un FLAC), dopo il ricontrollo si serve sotto l'`ETag` della ricevuta: la guardia `(size, mtime)` non è mai stata una garanzia sui byte, e anche la prima indicizzazione si fida della ricevuta (NOTES.md N-122, da confermare con l'utente). (ii) Punto 4: `file_size_mismatch` compare fra i problemi solo nel ciclo del ricontrollo; al ciclo seguente l'album è di nuovo «aggiornato», si salta e il problema sparisce dallo stato mentre il file resta 503 (conseguenza di N-064). È un limite noto. (iii) Punto 6: vale per audio e cover; per i testi un file assente o sostituito durante l'apertura resta `404 lyrics_not_found` senza ricontrollo, come nel §9.3 (NOTES.md N-120).

- **2026-10-05 · S17 · §10.2 punti 1–2, che cos'è una parola della query.** Il §10.2 divide `q` «su qualunque carattere che non sia lettera o cifra Unicode». Il tokenizer `unicode61` dell'indice tiene però dentro un token anche i numeri non decimali (`₂ ² ½ Ⅷ`) e gli accenti combinanti: una parola della query più stretta del token dell'indice perde risultati, perché il pezzo dopo il taglio non è prefisso di alcun token. Verificato dal revisore di S17 su SQLite vero: `H₂O` diventa `"H"* "O"*` e non trova l'album «H₂O»; una `q` in forma decomposta (NFD, come la inviano alcuni client) `E`+U+0301+`cho` diventa `"E"* "cho"*` e non trova «Écho Café». Regola nuova: (1) `q` si divide su ogni carattere che **non** è una lettera (categorie Unicode L\*), un numero (N\*, non solo le cifre decimali) o un segno combinante senza spaziatura (Mn); (2) una parola che non contiene alcuna lettera né alcun numero (solo segni) si scarta; (3) il resto del §10.2 non cambia: al più 8 parole di al più 64 caratteri (i segni contano), `"parola"*` in AND. T18 resta vero: virgolette, `*`, `-`, `^`, `:`, parentesi sono punteggiatura o simboli, mai lettere, numeri o segni combinanti, quindi non possono comparire in una parola. Dentro le virgolette è FTS5 stesso a dividere la parola con `unicode61`, come ha fatto per l'indice: una parola più larga del token è sicura (diventa una frase), una più stretta no. Il limite di 100 caratteri conta `q` com'è inviata (una `q` NFD conta anche gli accenti). Nessuna normalizzazione NFC: non coprirebbe i numeri né le sequenze senza forma precomposta (NOTES.md N-124, N-125; rilievo bloccante del round 1 di S17).

- **2026-10-05 · S17 (round 3) · §10.2 punti 1–2 e T18: la query divide solo dove `unicode61` divide di certo (sostituisce i punti 1 e 2 della voce precedente «che cos'è una parola della query»).** La voce precedente dava per vero che `unicode61` tenga in un token solo lettere, numeri e accenti combinanti. Non è così (revisore del round 2, su SQLite vero): le sue categorie predefinite sono `L* N* Co`, e ogni code point che le sue tabelle Unicode, più vecchie di quelle di Go, non conoscono è un carattere di token (le emoji recenti, `₺ ₽ ₿`, punteggiatura recente). Il titolo esatto «A🤝B», «🫶Love» o «Disco🪩Ball» non si trovava. Nessun elenco di categorie scritto in Go può seguire le tabelle di SQLite. Regola definitiva:
  1. **Dove si divide.** `q` si divide **solo** su: i caratteri ASCII che non sono lettere né cifre (`U+0000–U+007F` tranne `0–9`, `A–Z`, `a–z`), gli spazi Unicode (categorie Z\*), i caratteri di controllo (Cc) e i byte che non sono UTF-8 valido. Ogni altro carattere resta nella parola.
  2. **Chi decide i token.** Ogni parola diventa `"parola"*`: dentro le virgolette è FTS5 a dividerla con `unicode61`, esattamente come ha fatto per l'indice. Una parola più larga del token diventa una frase (token adiacenti, l'ultimo come prefisso); una parola che `unicode61` riduce a nessun token (solo punteggiatura non ASCII, solo accenti) è una frase vuota, che FTS5 ignora nell'AND implicito e che da sola non trova nulla. Vibrance non scarta parole: la regola «una parola di soli segni si scarta» della voce precedente è ritirata.
  3. **Limiti.** Al più 8 parole (contano tutte, anche quelle che FTS5 ignorerà) di al più 64 caratteri; `q` di 1–100 caratteri com'è inviata.
  4. **T18 resta vero, per un motivo più semplice.** L'unico carattere che può chiudere una stringa di FTS5 è la virgoletta `"` (U+0022), che è ASCII non alfanumerico e quindi separa sempre: non può comparire in una parola. Tutta la sintassi di `MATCH` (`*`, `-`, `^`, `:`, `+`, parentesi, graffe, `NEAR`, `OR`, `AND`, `NOT`) fuori da una stringa è fatta di ASCII; dentro una stringa non ha significato. NUL è un controllo e separa.
  5. **Prova dell'orchestratore (2026-10-05, test temporaneo su SQLite vero con le tabelle della migrazione, poi rimosso).** Per **ognuno** dei 1.112.063 code point validi `r`, il nome `a<r>b` indicizzato si trova cercando `a<r>b` con questa regola: 0 eccezioni (inserimento 15 s, ricerche 101 s, senza `-race`). Inoltre: `miles — davis`, `́ miles`, `… miles` trovano «Miles Davis» (la frase vuota è ignorata); `—` e `́` da soli non trovano nulla, senza errore; `A🤝B`, `🫶Love`, `🫶`, `Disco🪩Ball`, `H₂O`, `Sigur—Rós`, `«miles»`, `miles、davis`, `don’t` trovano; `""*` e `"" "miles"*` non danno errore.
  6. **Effetti visibili da documentare (specifica, README, NOTES.md).** Parole unite da punteggiatura non ASCII (`Sigur—Rós`, `miles…davis`) sono una frase ordinata, non un AND: `Rós—Sigur` non trova «Sigur Rós». Limiti di `unicode61` che restano (§10.3): `disco ball` non trova «Disco🪩Ball» e `🫶 love` non trova «🫶Love», perché per SQLite quelle emoji sono caratteri di token; una `q` NFD con segni che `unicode61` non toglie (kana con dakuten combinante, greco politonico) non trova il nome precomposto: la normalizzazione NFC di query **e** indice è un'idea per l'Appendice D, da proporre all'utente.
  - **Test (round 3):** `TestParse` e un test di `Find` fissano i casi del punto 5 (comprese la frase vuota in AND e da sola, e i limiti del punto 6); un test **esaustivo** sull'indice vero per ogni code point su cui `Parse` divide (sono poche centinaia): `a<r>b` si trova cercando `a<r>b`; lo stesso su un campione degli altri (almeno: uso privato, emoji recenti e vecchie, valute, punteggiatura non ASCII, segni Mn/Mc/Me, un code point non assegnato). L'oracolo di `FuzzSearchQuery` segue il punto 1 e si riesegue per 60 s.

- **2026-10-05 · S19 · T5, forma della rinumerazione.** T5 indica `UPDATE … FROM` con `row_number()`. sqlc 1.31.1 non legge quell'istruzione per SQLite e I7 vuole tutto l'SQL in `sql/`. Vale il risultato, non la forma: la rinumerazione densa è un'unica istruzione nella stessa transazione, ordinata per `(position, id)`; è ammesso l'upsert degli elementi su se stessi (`INSERT … SELECT …, row_number() OVER (ORDER BY position, id) - 1 … ON CONFLICT (id) DO UPDATE SET position = excluded.position`). Resta vietato `UNIQUE(playlist_id, position)` (NOTES.md N-131; verificato dal revisore di S19: SQLite legge tutta la `SELECT` prima di scrivere sulla stessa tabella, nessun trigger, nessuna scrittura quando le posizioni sono già dense).

- **2026-10-05 · S20 · la pagina di documentazione ha una terza rotta e una cartella (precisa §3.3, §8.3 e §8.8).** Il «solo file JavaScript vendorizzato» del §8.8 è servito da una rotta propria, `GET /api/docs/scalar.js` (GET e HEAD, senza autenticazione, dietro i controlli di `Host` e `Origin` come `/`, CSP chiusa dell'API, `Cache-Control: no-cache` con `ETag`): si aggiunge alle rotte fuori da `/api/v1` del §8.3 e alla lista a parte della matrice (§12.4). La struttura del §3.3 ha in più `web/` (pacchetto `web`: `docs/index.html`, `docs/scalar.js` e `docs/VENDOR.md`, incorporati nel binario). La pagina è Scalar API Reference 1.72.4 (MIT); la CSP della pagina è `default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'` (NOTES.md N-136, N-137; file e SHA-256 verificati dal revisore contro il tarball ufficiale del registro npm).

- **2026-10-05 · S23 · §12.3 e passo S23, A15 parte senza riferimenti.** Il passo dice «ogni scenario parte da playlist e preferiti già creati». A15 (arresti di Vibrance durante la **prima** scansione) è l'unica eccezione: prima della scansione iniziale non esistono tracce a cui riferirsi. La suite crea playlist e preferiti su ogni traccia subito dopo A15, e tutti gli altri scenari (A1–A14, A16–A20) partono da quelli. In A11 il «durante» del rebuild si osserva fermando con SIGSTOP il vero `musiclibd rebuild` appena compare `.maintenance`, perché il marcatore dura poche decine di millisecondi (NOTES.md N-156, N-157; parere del revisore di S23).

- **2026-10-05 · S24 · §5.2, indice per la durata delle playlist.** La durata di una playlist si calcola a ogni lettura sommando `duration_ms` delle tracce disponibili: con 10.000 elementi sono 10.000 letture di righe di `tracks`, e ogni modifica della playlist rilegge la playlist. Lo schema ha anche `CREATE INDEX tracks_duration_idx ON tracks (id, available, duration_ms);`, aggiunto dal passo S24 a `migrations/00001_schema.sql` (I13: prima del rilascio le migrazioni si possono ancora correggere, come per `tracks(fingerprint)` in S8 e `artists(sort_key, id)` in S15; un database di sviluppo creato prima non ha l'indice e va ricreato). Misura sul dataset di S24 (p95, rifatta dal revisore): aggiunta di un elemento in testa a una playlist da 10.000, 103 ms senza l'indice e 84 ms con; `GET /playlists` con 500 playlist da 200, 476 ms e 288 ms. Con il rilascio 0.1.0 la migrazione `00001` è chiusa: ogni altro indice è una migrazione nuova (NOTES.md N-163).

- **2026-10-05 · I7, approvata dall'utente · le istruzioni che sqlc scarta.** I7 dice che tutto l'SQL vive in `sql/*.sql` e ammette la sola eccezione di FTS5 in `internal/search`. sqlc 1.31.1 scarta i `PRAGMA` e non legge `VACUUM INTO`: quelle istruzioni stanno come **costanti** in `internal/store` (`journal_mode`, `optimize`, `wal_checkpoint(TRUNCATE)`, `integrity_check`, `foreign_key_check`, `VACUUM INTO ?` con il percorso come parametro legato). I7 si legge così: «Tutto l'SQL vive in `sql/*.sql` (sqlc). Le sole eccezioni sono FTS5 in `internal/search` e le istruzioni `PRAGMA` e `VACUUM INTO` costanti di `internal/store`, che sqlc scarta. Nessuna stringa SQL è costruita dall'input dell'utente.» L'utente ha approvato la modifica dell'invariante il 2026-10-05 (§0.8; NOTES.md N-023; suggerimento del revisore di S3, uso esteso in S21).

- **2026-10-06 · R1–R4 (passi nuovi, dopo S25 e prima del tag `v0.1.0`) · le decisioni dell'utente.** L'utente ha chiuso le decisioni aperte (PROGRESS.md, «Decisioni dell'utente») e ha chiesto il parere di un consulente indipendente, che ha letto il codice e fatto misure. Dove il parere ha corretto la forma di una proposta, vale la forma qui sotto. Nessuna di queste modifiche tocca lo schema: le migrazioni restano quelle di S24 (I13). Ogni passo aggiorna specifica, codice generato, `CHANGELOG.md` (sezione 0.1.0), README e guide, e NOTES.md; i quattro passi sono indipendenti e si eseguono in ordine.

  **R1 — Testi (§9.3; NOTES.md N-082).** Persona: ingegnere di parsing e formati di testo. Leggere: §9.3, il passo S10 e le trappole che cita, `internal/lyrics`.
  1. *Marca a tre campi.* Il §9.3 elenca solo `[hh:mm:ss.xx]` (ore **e** frazione). Una marca con tre campi **senza punto**, `[mm:ss:xx]`, è minuti:secondi:frazione: il terzo campo segue la regola della frazione (da 1 a 3 cifre). Con il punto resta `hh:mm:ss.xx`. Vale anche per i tag di parola `<…>`. Limite noto, da scrivere nella specifica: un vero `[hh:mm:ss]` senza frazione si legge come minuti:secondi:frazione.
  2. *Righe vuote nei testi non sincronizzati.* Si tengono come righe con `text: ""` (lo schema lo ammette già). Regole: una riga che era solo un tag di intestazione si scarta, non diventa una riga vuota; più righe vuote consecutive diventano una; quelle in testa e in coda si tolgono; `\r\n` è **un** fine-riga (oggi è letto come due con una riga vuota in mezzo); il limite di 10.000 righe conta anche le vuote. Nei testi sincronizzati nulla cambia.
  3. *Tag di intestazione.* Lista chiusa di tredici chiavi che non sono mai testo: `ar`, `ti`, `al`, `by`, `length`, `offset` (di oggi) più `au`, `lr`, `re`, `ve`, `tool`, `la`, `id`. Ogni altra riga `[lettere:…]` resta testo (per esempio `[Chorus: …]`).
  - Test: una tabella per ciascuno dei tre punti (compresi un file Windows con `\r\n`, righe vuote in testa, in coda e ripetute, un file di sole intestazioni, i tre campi con e senza punto, i tag di parola); l'oracolo del fuzz dei testi (la seconda scrittura della grammatica) aggiornato e rieseguito per 60 s; i test di tempo lineare restano verdi. Mutazioni: tre campi letti di nuovo come ore; `\r\n` letto come due fine-riga.
  - Accettazione: il revisore prova tre file `.lrc` suoi (uno `mm:ss:xx`, uno di solo testo con strofe e fine-riga Windows, uno con le intestazioni nuove).

  **R2 — Contratto dell'API (§7.3, §8.1, §8.3, §8.6; N-107, N-138, debito di S11/S19).** Persona: ingegnere backend API con attenzione alla sicurezza. Leggere: §7.3, §8.1, §8.3, §8.6, §8.8, I4, I10, T23.
  1. *`X-Vibrance-Request` nella specifica.* Un parametro `components.parameters` (in `header`, obbligatorio, `schema: {type: string, enum: ["1"]}`) richiamato con `$ref` da **tutte e sole** le operazioni non-GET. Il comportamento del server non cambia: il confine risponde `403 request_header_required` a un header mancante o diverso da `1` prima di ogni validazione (mai `400`). Niente `securitySchemes`: non è una credenziale. Si tolgono da specifica, README, `docs/api.md` e `web/docs/VENDOR.md` le istruzioni «aggiungi l'header a mano».
  2. *Header `ETag` sulle modifiche delle playlist.* `updatePlaylist`, `addPlaylistItems`, `removePlaylistItem`, `movePlaylistItem` rispondono anche con l'header `ETag`, uguale al campo `etag` del corpo, come già fanno `createPlaylist`, `getPlaylist` e `listPlaylistItems` e come promette il §8.1.
  3. *Cookie di sessione.* L'utente vuole restare dentro finché usa l'applicazione. La sessione sul server è già scorrevole (30 giorni, rinnovata all'uso, §7.3); è il browser che butta il cookie 30 giorni dopo l'accesso. Il cookie prende un `Max-Age` proprio di **400 giorni** (il tetto che i browser applicano ai cookie), distinto dalla durata della sessione, che resta 30 giorni scorrevoli e resta l'unica autorità: un cookie che sopravvive a una sessione scaduta è un token morto. **Non** si rimanda `Set-Cookie` al rinnovo (toccherebbe ogni risposta GET, I4, e ogni operazione nella specifica). `logout` e i token bearer non cambiano. Si correggono le descrizioni di `cookieAuth` e di `login`, che oggi dicono il falso.
  - Test: tutte e sole le operazioni non-GET dichiarano il parametro (dalla specifica); header assente o con valore `2` → 403, non 400; per ogni risposta che porta una playlist l'header `ETag` è uguale a `etag` del corpo; `login` → `Max-Age=34560000` con origine http e https; una sessione usata dopo il giorno 15 è ancora valida al giorno 31 con lo stesso cookie, e una non usata per 30 giorni non lo è; matrice di autorizzazione e conformità verdi. Mutazioni: un'operazione non-GET senza il parametro; un handler di modifica senza l'header `ETag`.
  - Accettazione: il revisore apre `/api/docs` in un browser sul server vero e prova una richiesta non-GET **senza aggiungere nulla a mano** (se la pagina non precompila il valore, lo dice nel rapporto: resta vero il guadagno per i client generati); esegue `scripts/contract.sh`.

  **R3 — Ricerca (§10.2, §11.4; N-126, N-144).** Persona: ingegnere di ricerca full-text su SQLite e SRE. Leggere: §10, §11.4, I7, T6, T18, l'errata «S17 (round 3)».
  1. *Query in NFC.* Corregge il punto 6 dell'errata «S17 (round 3)»: l'indice è **già** in NFC, perché ogni nome passa da `names.Normalize` prima di essere scritto; i test di allora scrivevano nomi decomposti direttamente nel database. Basta normalizzare la query: `Parse` applica NFC a `q` prima di dividerla. Il limite di 100 caratteri conta `q` com'è inviata; il taglio a 64 caratteri per parola avviene dopo NFC. Niente NFKC (romperebbe `H₂O`; resta un'idea per l'Appendice D). Nessuna ricostruzione dell'indice, nessuna migrazione.
  2. *`vibrance rebuild-search`.* Sottocomando nuovo: ricostruisce le tre tabelle FTS dalle tabelle madri in **una** transazione di scrittura (`DROP`, `CREATE` identici alla migrazione, `INSERT … SELECT` degli stessi dati che scrive lo scanner). `doctor` continua a non riparare nulla (§11.4); i suoi rilievi sull'indice di ricerca consigliano ora questo comando invece del ripristino di un backup. Codici d'uscita e rifiuti come `doctor` (database assente, schema più vecchio o più nuovo, root). La guida dice di fermare il server prima (su librerie grandi la transazione può superare il `busy_timeout`), anche se il comando è corretto a server acceso. Nessuna ricostruzione automatica all'avvio.
  - Test: una query NFD non latina (kana con dakuten combinante, greco politonico) trova il nome; `Parse(NFD(x))` è uguale a `Parse(NFC(x))` per testo UTF-8 valido (proprietà nel fuzz, 60 s, con l'oracolo che normalizza allo stesso modo); gli helper dei test indicizzano i nomi come li scrive lo scanner; lo sweep dei separatori resta verde. Per il comando: righe stantie, in più e mancanti e indice FTS danneggiato → dopo il comando `doctor` esce 0 e la ricerca risponde; lo schema delle tre tabelle in `sqlite_master` è identico a quello di un database appena migrato (protegge dalla deriva del DDL duplicato); idempotenza; a server acceso con letture e scritture concorrenti; i rifiuti. Mutazioni: NFC tolto da `Parse`; una delle tre tabelle non ricostruita.
  - Accettazione: il revisore rovina a mano l'indice di un database di prova, vede `doctor` uscire 1 e la ricerca fallire, esegue il comando e vede `doctor` uscire 0 e la ricerca rispondere; esegue `scripts/perf.sh` (budget della ricerca).

  **R4 — Operatività, cover e verifica finale (§9.2, §11.6, T20; N-147, N-165).** Persona: ingegnere DevOps e di sistemi. Leggere: §9.2, §11.6–11.7, D15, T20, T21, il passo S25.
  1. *`stop_grace_period: 15s`* nel servizio `vibrance` di `compose.yaml` (dentro il blocco Vibrance: il controllo di sincronizzazione con MusicLib non cambia). Dieci secondi di tolleranza delle richieste, poi chiusura, `PRAGMA optimize` e checkpoint: con i 10 s di default di Docker il SIGKILL arrivava nell'istante della chiusura.
  2. *Cover enormi.* Il semaforo delle miniature è già pesato (capacità 2). Una cover con più di **16.000.000 di pixel** prende peso 2 (si decodifica da sola); le altre peso 1. Il peso si decide leggendo solo l'intestazione (`image.DecodeConfig`) prima di acquisire il semaforo; se l'intestazione non si legge, peso 1 (la generazione fallisce come oggi e si serve l'originale). Il limite di 40 megapixel e 20 MiB resta. Così il picco nel caso peggiore scende da circa 830 MB (due cover da 40 megapixel insieme) a circa 260–440 MB (misure di R4: una cover da 40 megapixel da sola occupa 257–439 MB secondo forma e profondità in bit, due da 16 megapixel insieme 285–409 MB; il caso peggiore è una cover quadrata da 40 megapixel a 16 bit). La stessa regola ha una guardia: se, dopo l'attesa, i byte verificati chiedono più slot di quelli presi (il file è cambiato fra la lettura dell'intestazione e quella dei byte), la richiesta risponde `503 library_changing` e l'album si ricontrolla, come per ogni file sostituito (NOTES.md N-180).
  3. *Note di rilascio.* `CHANGELOG.md` e `SECURITY.md` dicono che la 0.1.0 è costruita con Go 1.25.14, serie non più supportata a monte, per restare sugli stessi pin di MusicLib (D2; decisione dell'utente: Go si aggiorna in futuro).
  - Test: con il gancio che esiste già, una cover sopra soglia non è mai in decodifica insieme a un'altra, due sotto soglia sì; `memory_perf_test.go` misura due cover da 16 megapixel insieme e una da 40 da sola; `scripts/check-compose-sync.sh` e `scripts/stack-smoke.sh` verdi. Mutazione: peso sempre 1.
  - Accettazione (è l'ultimo passo prima del tag): il revisore esegue sull'albero finale gate, `scripts/contract.sh`, `scripts/stack-smoke.sh`, `scripts/perf.sh`, `scripts/vulncheck.sh`, `scripts/check-compose-sync.sh` e `scripts/lint-shell.sh`, e verifica che `migrations/` sia identica a quella del commit di S24.

  **Decisioni che non cambiano il codice.** *Cover che non si apre (N-078):* resta com'è (`file_missing`, riprovata a ogni ciclo). La proposta «album indicizzato senza cover con avviso» non guarisce: un album indicizzato diventa «aggiornato» e lo scanner lo salta finché la ricevuta non cambia, quindi la cover non tornerebbe nemmeno dopo aver corretto il guasto. La variante con riprova a ogni ciclo è rimandata a dopo la 0.1.0. *Bitrate dei FLAC (N-040):* resta `null` (ogni file di MusicLib ha la cover incorporata: un bitrate calcolato dalla dimensione sarebbe falsato; il client ha `bit_depth`, `sample_rate`, `size` e `duration_ms`). *Miniature (N-076):* proporzionate. *Preferiti su tracce non disponibili (N-128):* restano ammessi. *Go (N-172):* resta 1.25.14.
