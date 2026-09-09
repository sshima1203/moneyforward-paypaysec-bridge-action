// Package sync is the scheduled job's command.
//
// What is left here after the work moved to the use case is what a command is
// actually for: reading configuration, wiring dependencies, and reporting.
package sync

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/secret"
)

const (
	// jobTimeout sits below the workflow's own limit, so the job fails with
	// these diagnostics rather than being killed by the runner.
	jobTimeout = 18 * time.Minute

	// otpTimeout caps the wait for a one-time code; otpInterval is the poll
	// cadence.
	otpTimeout  = 5 * time.Minute
	otpInterval = 5 * time.Second
)

// requiredEnv are the credentials the job cannot run without, and sourceEnv
// what each optional source needs.
//
// From the domain rather than listed again here: this and `secrets setup` were
// two copies of one set, kept in step by nothing but care, and a credential in
// only one of them is a run that fails at whichever step first needs it.
//
// Deliberately not flags: a value on a command line is visible to anything that
// can read the process list.
var requiredEnv = secret.RequiredNames()

// sourceEnv describes each source, for the help text.
//
// Listed because "optional" on its own is not usable: a reader needs to know
// that a source is all of its variables or none, and which those are.
func sourceEnv() string {
	var parts []string
	for _, provider := range secret.Providers {
		names := make([]string, 0, 3)
		for _, n := range provider.Names() {
			names = append(names, string(n))
		}
		parts = append(parts, provider.ID+" ("+strings.Join(names, ", ")+")")
	}
	return strings.Join(parts, "; ")
}

// Command builds the sync subcommand.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "sync",
		Usage: "read every configured source and record it in its MoneyForward account",
		Description: "Credentials come from the environment (GitHub Secrets in CI). They are\n" +
			"deliberately not flags: a value on the command line is visible in the\n" +
			"process list.\n\n" +
			"Always required: " + fmt.Sprint(requiredEnv) + ".\n\n" +
			"Sources, each all-or-nothing and at least one of them needed:\n  " +
			sourceEnv() + ".",
		Action: func(ctx context.Context, _ *cli.Command) error {
			log.SetFlags(log.Ltime)
			return run(ctx)
		},
	}
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	// Everything this needs is assembled by wire, from the providers next door.
	// What is left here is the order of the two phases, which is the use case's
	// business, and the deadline, which is this command's.
	sync, cleanup, err := newSync(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	sync.RepairExactDuplicates = true

	_, err = sync.Run(ctx)
	return err
}
