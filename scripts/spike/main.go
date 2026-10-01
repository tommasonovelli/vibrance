// Command spike is the verification tool of step S1 (DESIGN.md §14): it
// checks the hypotheses of the contract with MusicLib (DESIGN.md §4, §5.4)
// against the real MusicLib 1.1.0 and produces the fixture library
// (DESIGN.md §12.2). It is test tooling, not product code: nothing in
// Vibrance imports it.
//
// It runs only inside the `tools` container of scripts/spike/compose.yaml,
// driven by scripts/make-fixture-library.sh and scripts/spike.sh, with:
//
//	/musiclib   MusicLib's data volume, read-only (library/, the markers)
//	/import     MusicLib's import folder, which `inputs` fills
//	/work       state shared between the subcommands and the host script
//	/src        the repository (bind mount): the fixture and the report
//
// and MUSICLIB_URL, MUSICLIB_PASSWORD in the environment. Each subcommand
// writes its findings as a Markdown fragment and a verdict under /work;
// `report` assembles them into docs/spike-report.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Fixed paths inside the tools container (scripts/spike/compose.yaml).
const (
	musiclibRoot = "/musiclib"
	libraryRoot  = musiclibRoot + "/library"
	importRoot   = "/import"
	workRoot     = "/work"
	srcRoot      = "/src"
)

var commands = map[string]func(ctx context.Context) error{
	"inputs":            runInputs,
	"import":            runImport,
	"fixture":           runFixture,
	"analyze":           runAnalyze,
	"edits":             runEdits,
	"tags":              runTags,
	"online":            runOnline,
	"snapshot":          runSnapshot,
	"watch-maintenance": runWatchMaintenance,
	"after-rebuild":     runAfterRebuild,
	"rebuild-check":     runRebuildCheck,
	"report":            runReport,
}

func main() {
	if len(os.Args) != 2 || commands[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "usage: spike inputs|import|fixture|analyze|edits|tags|online|snapshot|watch-maintenance|after-rebuild|rebuild-check|report")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := commands[os.Args[1]](ctx)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spike %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}
