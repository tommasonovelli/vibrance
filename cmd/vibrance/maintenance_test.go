package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"vibrance/internal/app"
	"vibrance/internal/store"
)

// The commands backup, restore and doctor (DESIGN.md §11.4, step S21):
// their arguments, their output and their exit codes. What they do to the
// database is proved in internal/app.

// runMaintenance runs `vibrance <command> args...` in this process as uid
// euid, on the state folder state and the backup folder backups, and
// returns the exit code, standard output and the log.
func runMaintenance(t *testing.T, euid int, state, backups, command string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, logs bytes.Buffer
	log := newLogger(&logs)
	var code int
	switch command {
	case "backup":
		code = backup(args, euid, &stdout, state, backups, log)
	case "restore":
		code = restore(args, euid, &stdout, state, backups, log)
	case "doctor":
		code = doctor(args, euid, &stdout, state, log)
	default:
		t.Fatalf("no command %s", command)
	}
	return code, stdout.String(), logs.String()
}

// wantAdvice checks that logs is one ERROR line with the code and an
// advice.
func wantAdvice(t *testing.T, logs, code string) {
	t.Helper()
	wantLog(t, []byte(logs), code)
	var entry struct {
		Advice string `json:"advice"`
	}
	if err := json.Unmarshal([]byte(logs), &entry); err != nil || entry.Advice == "" {
		t.Fatalf("the log line has no advice: %s", logs)
	}
}

// newDatabase makes the database of a new server in a new state folder,
// with an admin when admin is true.
func newDatabase(t *testing.T, admin bool) string {
	t.Helper()
	state := t.TempDir()
	if admin {
		if code, _, logs := runUser(t, state, firstPassword, "create", "--username", "anna", "--role", "admin", "--password-stdin"); code != exitOK {
			t.Fatalf("user create: %d %s", code, logs)
		}
		return state
	}
	s, err := store.Open(t.Context(), filepath.Join(state, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return state
}

// The three commands from the first backup to a restored server: the
// lines they print and their exit codes 0, 1 and 2.
func TestMaintenanceCommands(t *testing.T) {
	state, backups := newDatabase(t, true), t.TempDir()
	dest := backups + "/2026-10-05-2130"

	if code, out, logs := runMaintenance(t, nonRoot, state, backups, "doctor"); code != exitOK || out != "Doctor complete: no damage found.\n" || logs != "" {
		t.Fatalf("doctor: exit %d, stdout %q, log %s", code, out, logs)
	}
	if code, out, logs := runMaintenance(t, nonRoot, state, backups, "backup", "--to", dest); code != exitOK || out != "Backup completed: "+dest+"\n" || logs != "" {
		t.Fatalf("backup: exit %d, stdout %q, log %s", code, out, logs)
	}
	code, out, logs := runMaintenance(t, nonRoot, state, backups, "backup", "--to="+dest)
	if code != exitUsage || out != "" {
		t.Fatalf("a second backup with the same name: exit %d, stdout %q", code, out)
	}
	wantAdvice(t, logs, app.CodeBackupExists)
	code, _, logs = runMaintenance(t, nonRoot, state, backups, "backup", "--to", "/tmp/elsewhere")
	if code != exitUsage {
		t.Fatalf("a backup outside the backup folder: exit %d", code)
	}
	wantAdvice(t, logs, app.CodeBackupOutside)

	code, _, logs = runMaintenance(t, nonRoot, state, backups, "restore", "--from", dest)
	if code != exitUsage {
		t.Fatalf("a restore onto a database: exit %d", code)
	}
	wantAdvice(t, logs, app.CodeRestoreDatabase)
	restored := t.TempDir()
	if code, out, logs := runMaintenance(t, nonRoot, restored, backups, "restore", "--from", dest); code != exitOK || out != "Restore completed: "+dest+". Start the server.\n" || logs != "" {
		t.Fatalf("restore: exit %d, stdout %q, log %s", code, out, logs)
	}
	// The restored database is the one of the server: anna signs in.
	if _, err := accounts(t, restored).CreateToken(t.Context(), "anna", firstPassword, "tests", ""); err != nil {
		t.Fatalf("anna cannot sign in on the restored database: %v", err)
	}
	if code, out, _ := runMaintenance(t, nonRoot, restored, backups, "doctor"); code != exitOK || out != "Doctor complete: no damage found.\n" {
		t.Fatalf("doctor of the restored database: exit %d, stdout %q", code, out)
	}
}

// The doctor prints one line per finding and exits 1.
func TestDoctorFindings(t *testing.T) {
	state := newDatabase(t, false)
	code, out, logs := runMaintenance(t, nonRoot, state, t.TempDir(), "doctor")
	want := "doctor_no_admin users: no admin is enabled. Advice: "
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if code != exitFailure || len(lines) != 2 || !strings.HasPrefix(lines[0], want) ||
		lines[1] != "Doctor complete: 1 problems found. Nothing was changed." || logs != "" {
		t.Fatalf("doctor: exit %d, stdout %q, log %s", code, out, logs)
	}
	// Without a database there is nothing to inspect, and none is created.
	empty := t.TempDir()
	code, _, logs = runMaintenance(t, nonRoot, empty, t.TempDir(), "doctor")
	if code != exitUsage {
		t.Fatalf("doctor without a database: exit %d", code)
	}
	wantAdvice(t, logs, app.CodeDatabaseMissing)
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Fatalf("the doctor created %v, %v", entries, err)
	}
}

// Arguments that are not the ones of §11.4, and root, are refused with 2
// before anything is opened.
func TestMaintenanceRefusals(t *testing.T) {
	state, backups := newDatabase(t, true), t.TempDir()
	for _, tc := range []struct {
		command string
		args    []string
		euid    int
		code    string
	}{
		{"backup", nil, nonRoot, "usage"},
		{"backup", []string{"--to"}, nonRoot, "usage"},
		{"backup", []string{"--to", ""}, nonRoot, "usage"},
		{"backup", []string{"--from", backups + "/x"}, nonRoot, "usage"},
		{"backup", []string{"--to", backups + "/x", "more"}, nonRoot, "usage"},
		{"restore", nil, nonRoot, "usage"},
		{"restore", []string{"--to", backups + "/x"}, nonRoot, "usage"},
		{"doctor", []string{"--deep"}, nonRoot, "usage"},
		{"backup", []string{"--to", backups + "/x"}, 0, app.CodeRunAsRoot},
		{"restore", []string{"--from", backups + "/x"}, 0, app.CodeRunAsRoot},
		{"doctor", nil, 0, app.CodeRunAsRoot},
	} {
		code, out, logs := runMaintenance(t, tc.euid, state, backups, tc.command, tc.args...)
		if code != exitUsage || out != "" {
			t.Fatalf("%s %v as %d: exit %d, stdout %q", tc.command, tc.args, tc.euid, code, out)
		}
		wantLog(t, []byte(logs), tc.code)
	}
	if entries, err := os.ReadDir(backups); err != nil || len(entries) != 0 {
		t.Fatalf("the refusals wrote %v, %v", entries, err)
	}
}

// runCommand runs the binary with args, without environment, and returns
// its exit code and standard output.
func runCommand(t *testing.T, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, vibranceBinary(t), args...)
	cmd.Env = []string{}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String()
	case errors.As(err, &exitErr) && exitErr.Exited():
		return exitErr.ExitCode(), stdout.String()
	default:
		t.Fatalf("vibrance %v: %v", args, err)
		return 0, ""
	}
}

// resetBackups empties the backup folder of the binary.
func resetBackups(t *testing.T) string {
	t.Helper()
	vibranceBinary(t)
	if err := os.RemoveAll(binary.backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(binary.backup, 0o755); err != nil {
		t.Fatal(err)
	}
	return binary.backup
}

// §3.4: backup and doctor run while the server runs, as processes.
func TestProcessBackupAndDoctorWhileTheServerRuns(t *testing.T) {
	resetState(t)
	backups := resetBackups(t)
	p := startServe(t, freeAddr(t), 0o022)
	p.waitReady(t)
	if code, out := runCommand(t, "backup", "--to", backups+"/live"); code != exitOK || out != "Backup completed: "+backups+"/live\n" {
		t.Fatalf("backup: exit %d\n%s", code, out)
	}
	if code, out := runCommand(t, "doctor"); code != exitOK || out != "Doctor complete: no damage found.\n" {
		t.Fatalf("doctor: exit %d\n%s", code, out)
	}
	p.signal(t, syscall.SIGTERM)
	p.wantExit(t, exitOK)
}

// Step S21: a backup killed with SIGKILL at any moment never leaves a
// folder with the name of the backup. The name exists only for a whole
// backup, whose manifest describes its copy.
func TestProcessBackupKilled(t *testing.T) {
	state := resetState(t)
	backups := resetBackups(t)
	// A database of some megabytes, so that a backup lasts long enough to
	// be killed while it writes.
	s, err := store.Open(t.Context(), filepath.Join(state, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(state, "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := db.ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 3000)
		INSERT INTO meta ("key", value) SELECT 'filler ' || i, hex(randomblob(4096)) FROM n`)
	if err := errors.Join(execErr, db.Close()); err != nil {
		t.Fatal(err)
	}

	killed, leftovers := 0, 0
	for i := 0; i < 40 && leftovers < 3; i++ {
		name := filepath.Join(backups, "b"+strings.Repeat("x", i))
		cmd := exec.Command(vibranceBinary(t), "backup", "--to", name)
		cmd.Env = []string{}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(i*5) * time.Millisecond)
		if err := cmd.Process.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Fatal(err)
		}
		err := cmd.Wait()
		ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
		entries, rerr := os.ReadDir(backups)
		if rerr != nil {
			t.Fatal(rerr)
		}
		final := false
		for _, e := range entries {
			if e.Name() == filepath.Base(name) {
				final = true
			}
		}
		switch {
		case ws.Signaled():
			killed++
			if final {
				// The rename is the last step: only a whole backup has the name.
				wantWholeBackup(t, name)
			}
		case err == nil:
			wantWholeBackup(t, name)
		default:
			t.Fatalf("backup %d: %v", i, err)
		}
		leftovers = 0
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".vibrance-backup-") {
				leftovers++
			}
		}
	}
	if killed == 0 || leftovers == 0 {
		t.Fatalf("%d backups killed, %d temporary folders: no backup was killed while it wrote", killed, leftovers)
	}
}

// wantWholeBackup checks that the backup in dir is complete: its manifest
// names its copy.
func wantWholeBackup(t *testing.T, dir string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("the backup %s has no manifest: %v", dir, err)
	}
	var m app.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Stat(filepath.Join(dir, "vibrance.db")); err != nil || got.Size() != m.Database.Size {
		t.Fatalf("the backup %s has a copy of %v bytes, its manifest says %d", dir, got, m.Database.Size)
	}
}
