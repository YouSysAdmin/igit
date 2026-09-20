package main

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/yousysadmin/igit/internal/update"
)

// updateSubcommandName is the positional word that runs the self-updater
// instead of starting the TUI.
const updateSubcommandName = "update"

// detectUpdateSubcommand turns "igit update" into a self-update run. As with
// commit and pr, the word counts as the subcommand only when it stands alone
// and was not forced positional with "--", so a ref named update can still be
// reviewed.
func detectUpdateSubcommand(opts options, args []string) options {
	if opts.Refs.Base != updateSubcommandName || opts.Refs.Against != "" {
		return opts
	}
	if slices.Contains(args, "--") {
		return opts
	}
	opts.updateSubcommand = true
	opts.Refs.Base = ""
	return opts
}

// runUpdate reports the newest release with --check, and otherwise downloads it
// and replaces the running binary. revision is the build stamp, which only
// carries a version for a release build.
func runUpdate(opts options, revision string, out io.Writer, upd update.Updater) error {
	// a development build has no released version to compare against, so it is
	// told what the latest release is and updated on request
	current, _ := update.Version(revision)
	ctx := context.Background()

	if !opts.Check {
		if err := upd.Apply(ctx, current, out); err != nil {
			return fmt.Errorf("update igit: %w", err)
		}
		return nil
	}

	res, err := upd.Check(ctx, current)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	_, _ = fmt.Fprintln(out, checkMessage(res))
	return nil
}

// checkMessage phrases a check result for the terminal.
func checkMessage(res update.Result) string {
	switch {
	case res.Current == "":
		return fmt.Sprintf("development build, latest release is v%s, run `igit update` to install it", res.Latest)
	case res.Newer:
		return fmt.Sprintf("igit v%s is out of date, v%s is available, run `igit update` to install it", res.Current, res.Latest)
	default:
		return fmt.Sprintf("igit v%s is up to date", res.Current)
	}
}
