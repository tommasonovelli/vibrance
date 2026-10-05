package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vibrance/internal/buildinfo"
	"vibrance/internal/store"
)

// The codes of the operational commands backup and restore (DESIGN.md
// §11.4). A refusal (exit 2) has done nothing; a failure (exit 1) was
// attempted and failed, and its advice says what is left.
const (
	CodeDatabaseMissing       = "database_missing"
	CodeBackupOutside         = "backup_outside_backup"
	CodeBackupExists          = "backup_exists"
	CodeBackupDestination     = "backup_destination"
	CodeBackupFailed          = "backup_failed"
	CodeBackupVerify          = "backup_verify"
	CodeRestoreOutside        = "restore_outside_backup"
	CodeRestoreDatabase       = "restore_database_exists"
	CodeRestoreManifest       = "restore_manifest_invalid"
	CodeRestoreHash           = "restore_hash"
	CodeRestoreSchemaTooNew   = "restore_schema_too_new"
	CodeRestoreDestination    = "restore_destination"
	CodeRestoreFailed         = "restore_failed"
	backupDatabase            = "vibrance.db"
	backupManifest            = "manifest.json"
	backupTemporaryPrefix     = ".vibrance-backup-"
	restoreTemporaryPrefix    = ".vibrance-restore-"
	temporarySuffix           = ".tmp"
	maxManifestBytes          = 64 << 10
	adviceBackupLeftover      = "only a folder with the name given to backup is a backup: remove the .vibrance-backup-*.tmp folder next to it, fix the cause and retry"
	adviceUseBackupFolder     = "use a new folder directly or indirectly under the backup folder, written as a plain absolute path such as /backup/2026-10-05-2130"
	adviceSchemaOld           = "start the server once with this version, which migrates the database, then retry"
	adviceSchemaNew           = "use the version of Vibrance that wrote the database, or a newer one"
	adviceRestoreUseAnother   = "nothing was written: use another, complete backup"
	adviceRestoreNotWritten   = "nothing was restored: fix the cause and retry; a .vibrance-restore-*.tmp file left in the state folder can be removed"
	adviceStateFolderNotEmpty = "restore writes only a new database: stop the server and restore into a new, empty state volume"
)

// Manifest is the inventory of a backup, its manifest.json (DESIGN.md
// §11.4). It holds no secret: the counts and the hash of the database file
// only (I5).
type Manifest struct {
	AppVersion    string           `json:"app_version"`
	SchemaVersion int64            `json:"schema_version"`
	CreatedAt     string           `json:"created_at"`
	Database      ManifestDatabase `json:"database"`
	Counts        ManifestCounts   `json:"counts"`
}

// ManifestDatabase is the database file of a backup.
type ManifestDatabase struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ManifestCounts are the rows of the copy, for the operator to compare
// with what the restored server shows.
type ManifestCounts struct {
	Users         int64 `json:"users"`
	Playlists     int64 `json:"playlists"`
	PlaylistItems int64 `json:"playlist_items"`
	Favorites     int64 `json:"favorites"`
	Artists       int64 `json:"artists"`
	Albums        int64 `json:"albums"`
	Tracks        int64 `json:"tracks"`
}

func refusal(code, msg, advice string, err error) error {
	return &Error{Code: code, Msg: msg, Advice: advice, Refusal: true, Err: err}
}

func failure(code, msg, advice string, err error) error {
	return &Error{Code: code, Msg: msg, Advice: advice, Err: err}
}

// checkUnder refuses a path that is not a clean absolute path strictly
// under dir, the backup folder. Anywhere else is the filesystem of the
// one-off container, lost when it exits although the command succeeded.
// It is lexical, and runs before anything is opened.
func checkUnder(path, dir string) bool {
	return filepath.Clean(path) == path && strings.HasPrefix(path, dir+"/")
}

// Backup writes a new backup of the database in stateDir to the folder
// dest, which must be under backupDir and must not exist (DESIGN.md §11.4).
// The server may be running: the copy is one committed state of the
// database (VACUUM INTO). The folder is written under a temporary name in
// the same parent, verified, and renamed to dest only when it is complete:
// dest exists only as a whole backup. The returned error is an *Error.
func Backup(ctx context.Context, stateDir, backupDir, dest string, now func() time.Time) (Manifest, error) {
	if !checkUnder(dest, backupDir) {
		return Manifest{}, refusal(CodeBackupOutside, fmt.Sprintf(
			"backup --to %q: the destination must be a new folder under %s; nothing was written", dest, backupDir),
			adviceUseBackupFolder, nil)
	}
	source := filepath.Join(stateDir, databaseFile)
	if err := checkCurrentDatabase(ctx, source); err != nil {
		return Manifest{}, err
	}
	parent := filepath.Dir(dest)
	if _, err := os.Lstat(dest); err == nil {
		return Manifest{}, refusal(CodeBackupExists, fmt.Sprintf("backup --to %q: it exists already; backups are never overwritten", dest),
			"choose another name", nil)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, refusal(CodeBackupDestination, fmt.Sprintf("backup --to %q: cannot look at the destination", dest),
			adviceUseBackupFolder, err)
	}
	// 0700: the copy holds the password and session hashes of the server.
	tmp, err := os.MkdirTemp(parent, backupTemporaryPrefix+"*"+temporarySuffix)
	if err != nil {
		return Manifest{}, refusal(CodeBackupDestination, fmt.Sprintf(
			"backup --to %q: cannot create a folder in %s; nothing was written", dest, parent),
			"the parent folder must exist and be writable by the user of the server (VIBRANCE_UID and VIBRANCE_GID)", err)
	}
	m, err := writeBackup(ctx, source, tmp, now)
	if err != nil {
		return Manifest{}, err
	}
	if err := syncDir(parent); err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot sync the backup folder", adviceBackupLeftover, err)
	}
	// The name was free a moment ago. A folder that appeared meanwhile is
	// not replaced if it holds anything: rename refuses a folder that is
	// not empty.
	if _, err := os.Lstat(dest); err == nil {
		return Manifest{}, failure(CodeBackupExists, fmt.Sprintf("backup --to %q: it appeared while the backup ran", dest),
			adviceBackupLeftover, nil)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot give the backup its name", adviceBackupLeftover, err)
	}
	if err := syncDir(parent); err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot sync the backup folder; the backup "+dest+" may not survive a power cut",
			"run the backup again under another name", err)
	}
	return m, nil
}

// checkCurrentDatabase refuses, before anything is written, a database
// that is missing or that this binary does not read as it is: the copy is
// checked with the queries of this binary.
func checkCurrentDatabase(ctx context.Context, path string) error {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return refusal(CodeDatabaseMissing, "there is no database at "+path,
			"the server creates it at its first start; check the state volume", nil)
	}
	r, err := store.OpenReader(ctx, path, false)
	if err != nil {
		return storeRefusal(err)
	}
	return errors.Join(storeRefusal(r.CheckCurrent()), r.Close())
}

// storeRefusal is a refusal with the code of the store and its advice.
func storeRefusal(err error) error {
	if err == nil {
		return nil
	}
	advice := "check that the file is the database of Vibrance and that the user of the server can read and write it"
	switch store.Code(err) {
	case store.CodeSchemaOld:
		advice = adviceSchemaOld
	case store.CodeSchemaTooNew:
		advice = adviceSchemaNew
	}
	return refusal(store.Code(err), "opening the database", advice, err)
}

// writeBackup fills the temporary folder tmp: the copy, verified, and its
// manifest, all synced.
func writeBackup(ctx context.Context, source, tmp string, now func() time.Time) (Manifest, error) {
	copyPath := filepath.Join(tmp, backupDatabase)
	if err := store.CopyInto(ctx, source, copyPath); err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot copy the database", adviceBackupLeftover, err)
	}
	if err := syncFile(copyPath, 0o600); err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot sync the copy", adviceBackupLeftover, err)
	}
	sum, size, err := hashFile(copyPath)
	if err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot read the copy", adviceBackupLeftover, err)
	}
	m, err := verifyCopy(ctx, copyPath)
	if err != nil {
		return Manifest{}, err
	}
	// The copy is read again from the disk: the manifest names the bytes
	// that are there, which the check did not change.
	again, _, err := hashFile(copyPath)
	if err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot read the copy", adviceBackupLeftover, err)
	}
	if again != sum {
		return Manifest{}, failure(CodeBackupVerify, "the copy changed while it was verified", adviceBackupLeftover, nil)
	}
	m.AppVersion = buildinfo.Version
	m.CreatedAt = now().UTC().Format(timestampLayout)
	m.Database = ManifestDatabase{File: backupDatabase, Size: size, SHA256: sum}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot write the manifest", adviceBackupLeftover, err)
	}
	if err := writeSynced(filepath.Join(tmp, backupManifest), append(b, '\n'), 0o600); err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot write the manifest", adviceBackupLeftover, err)
	}
	if err := syncDir(tmp); err != nil {
		return Manifest{}, failure(CodeBackupFailed, "cannot sync the backup", adviceBackupLeftover, err)
	}
	return m, nil
}

// timestampLayout is the form of every date Vibrance writes (T25).
const timestampLayout = "2006-01-02T15:04:05.000Z"

// verifyCopy checks the copy with PRAGMA integrity_check, which covers the
// full-text indexes too, and returns its schema version and its counts.
// The copy is opened frozen: reading it changes nothing.
func verifyCopy(ctx context.Context, path string) (m Manifest, err error) {
	r, err := store.OpenReader(ctx, path, true)
	if err != nil {
		return Manifest{}, failure(CodeBackupVerify, "cannot open the copy", adviceBackupLeftover, err)
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			err = errors.Join(err, failure(CodeBackupFailed, "cannot close the copy", adviceBackupLeftover, cerr))
		}
	}()
	var damage []string
	var counts store.CountRowsRow
	err = r.Read(ctx, func(q *store.Queries) error {
		var err error
		if damage, err = q.IntegrityCheck(ctx); err != nil {
			return err
		}
		counts, err = q.CountRows(ctx)
		return err
	})
	if err != nil {
		return Manifest{}, failure(CodeBackupVerify, "cannot verify the copy", adviceBackupLeftover, err)
	}
	if len(damage) > 0 {
		return Manifest{}, failure(CodeBackupVerify, "the copy is damaged: "+strings.Join(damage, "; "),
			"run vibrance doctor on the server's database; "+adviceBackupLeftover, nil)
	}
	return Manifest{SchemaVersion: r.SchemaVersion(), Counts: ManifestCounts{
		Users: counts.Users, Playlists: counts.Playlists, PlaylistItems: counts.PlaylistItems, Favorites: counts.Favorites,
		Artists: counts.Artists, Albums: counts.Albums, Tracks: counts.Tracks,
	}}, nil
}

// Restore puts the backup in the folder from, which must be under
// backupDir, as the database of stateDir, which must have none (DESIGN.md
// §11.4). It verifies the manifest and the SHA-256 of the copy and refuses
// a schema newer than the binary before it writes anything. The database
// appears with its name only once it is complete and synced; it is never
// overwritten. The returned error is an *Error.
func Restore(ctx context.Context, stateDir, backupDir, from string) (Manifest, error) {
	if !checkUnder(from, backupDir) {
		return Manifest{}, refusal(CodeRestoreOutside, fmt.Sprintf(
			"restore --from %q: the backup must be a folder under %s; nothing was written", from, backupDir),
			"give the folder of the backup as a plain absolute path such as /backup/2026-10-05-2130", nil)
	}
	target := filepath.Join(stateDir, databaseFile)
	if err := checkNoDatabase(target); err != nil {
		return Manifest{}, err
	}
	m, err := readManifest(filepath.Join(from, backupManifest))
	if err != nil {
		return Manifest{}, err
	}
	source := filepath.Join(from, backupDatabase)
	sum, size, err := hashFile(source)
	if err != nil {
		return Manifest{}, refusal(CodeRestoreHash, "cannot read the database of the backup", adviceRestoreUseAnother, err)
	}
	if sum != m.Database.SHA256 || size != m.Database.Size {
		return Manifest{}, refusal(CodeRestoreHash, "the database of the backup is not the one its manifest describes", adviceRestoreUseAnother, nil)
	}
	if err := checkBackupSchema(ctx, source, m.SchemaVersion); err != nil {
		return Manifest{}, err
	}
	if err := copyDatabase(source, target, m.Database.SHA256); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// checkNoDatabase refuses a state folder that has a database, or what is
// left of one: a write-ahead log of another database would be applied to
// the restored one.
func checkNoDatabase(target string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_, err := os.Lstat(target + suffix)
		if err == nil {
			return refusal(CodeRestoreDatabase, fmt.Sprintf("%s exists: restore never overwrites a database", target+suffix),
				adviceStateFolderNotEmpty, nil)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return refusal(CodeRestoreDestination, "cannot look at the state folder", adviceStateFolderNotEmpty, err)
		}
	}
	return nil
}

// readManifest reads and checks manifest.json strictly: an unknown key, a
// missing or malformed value, is a backup that cannot be trusted.
func readManifest(path string) (Manifest, error) {
	invalid := func(err error) error {
		return refusal(CodeRestoreManifest, "the manifest of the backup "+path+" is missing or not valid", adviceRestoreUseAnother, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, invalid(err)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err := errors.Join(err, f.Close()); err != nil {
		return Manifest{}, invalid(err)
	}
	if len(b) > maxManifestBytes {
		return Manifest{}, invalid(errors.New("too large"))
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, invalid(err)
	}
	if dec.More() {
		return Manifest{}, invalid(errors.New("more than one document"))
	}
	sum, err := hex.DecodeString(m.Database.SHA256)
	switch {
	case m.Database.File != backupDatabase:
		return Manifest{}, invalid(fmt.Errorf("the database file is %q, not %q", m.Database.File, backupDatabase))
	case err != nil || len(sum) != sha256.Size || m.Database.SHA256 != strings.ToLower(m.Database.SHA256):
		return Manifest{}, invalid(errors.New("the SHA-256 is not 64 lowercase hexadecimal digits"))
	case m.Database.Size <= 0:
		return Manifest{}, invalid(errors.New("the size is not positive"))
	case m.SchemaVersion <= 0:
		return Manifest{}, invalid(errors.New("the schema version is not positive"))
	case m.AppVersion == "":
		return Manifest{}, invalid(errors.New("the version of Vibrance is missing"))
	}
	if _, err := time.Parse(timestampLayout, m.CreatedAt); err != nil {
		return Manifest{}, invalid(fmt.Errorf("the date: %w", err))
	}
	return m, nil
}

// checkBackupSchema refuses a copy that a newer binary made, and one whose
// schema is not the one its manifest names. An older one is restored: the
// server migrates it when it starts.
func checkBackupSchema(ctx context.Context, path string, want int64) error {
	r, err := store.OpenReader(ctx, path, true)
	if err != nil {
		if store.Code(err) == store.CodeSchemaTooNew {
			return refusal(CodeRestoreSchemaTooNew, "the backup was made by a newer version of Vibrance", adviceSchemaNew, err)
		}
		return refusal(CodeRestoreManifest, "the database of the backup cannot be opened", adviceRestoreUseAnother, err)
	}
	got := r.SchemaVersion()
	if err := r.Close(); err != nil {
		return refusal(CodeRestoreManifest, "the database of the backup cannot be read", adviceRestoreUseAnother, err)
	}
	if got != want {
		return refusal(CodeRestoreManifest, fmt.Sprintf("the database of the backup has schema version %d, its manifest says %d", got, want),
			adviceRestoreUseAnother, nil)
	}
	return nil
}

// copyDatabase copies source to target through a temporary file of the
// state folder, checking the bytes it wrote against sum, and links it to
// target, which fails if target exists: a server started meanwhile keeps
// its database.
func copyDatabase(source, target, sum string) (err error) {
	dir := filepath.Dir(target)
	tmp := filepath.Join(dir, restoreTemporaryPrefix+rand.Text()+temporarySuffix)
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return refusal(CodeRestoreDestination, "cannot create a file in the state folder "+dir,
			"the state folder must be writable by the user of the server (VIBRANCE_UID and VIBRANCE_GID)", err)
	}
	// The temporary file goes in every case: once linked, target holds it.
	defer func() {
		if rerr := os.Remove(tmp); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			err = errors.Join(err, failure(CodeRestoreFailed, "cannot remove "+tmp, adviceRestoreNotWritten, rerr))
		}
	}()
	in, err := os.Open(source)
	if err != nil {
		return errors.Join(failure(CodeRestoreFailed, "cannot read the database of the backup", adviceRestoreNotWritten, err), out.Close())
	}
	h := sha256.New()
	_, cerr := io.Copy(io.MultiWriter(out, h), in)
	if err := errors.Join(cerr, in.Close(), out.Sync(), out.Close()); err != nil {
		return failure(CodeRestoreFailed, "cannot write the database", adviceRestoreNotWritten, err)
	}
	if hex.EncodeToString(h.Sum(nil)) != sum {
		return failure(CodeRestoreHash, "the backup changed while it was restored", adviceRestoreNotWritten, nil)
	}
	if err := os.Link(tmp, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return failure(CodeRestoreDatabase, target+" appeared while the backup was restored: it was not touched", adviceStateFolderNotEmpty, err)
		}
		return failure(CodeRestoreFailed, "cannot give the database its name", adviceRestoreNotWritten, err)
	}
	if err := syncDir(dir); err != nil {
		return failure(CodeRestoreFailed, "cannot sync the state folder",
			"the database is in place but may not survive a power cut: stop nothing, run vibrance doctor, and restore again into a new volume if it fails", err)
	}
	return nil
}

// hashFile returns the SHA-256, in lowercase hexadecimal, and the size of
// a file.
func hashFile(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	h := sha256.New()
	if size, err = io.Copy(h, f); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// syncFile sets the mode of a file and writes it to the disk.
func syncFile(path string, mode os.FileMode) (err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(f.Chmod(mode), f.Sync(), f.Close())
}

// writeSynced creates a new file with data and writes it to the disk.
func writeSynced(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	return errors.Join(werr, f.Sync(), f.Close())
}

// syncDir writes the entries of a folder to the disk: a file created or
// renamed in it survives a power cut only then.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
