// Package syncassets performs one synchronisation: read each source's holdings,
// then bring the account it is recorded in into line with them.
//
// Separate from the command layer because the phases and the order they run in
// are the actual work, and nothing about them depends on flags, a terminal or an
// environment. What varies — where the browser comes from, how a one-time code
// arrives, what gets logged — arrives as dependencies.
package syncassets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/asset"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/portfolio"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/port"
)

// Bridge is one source and the account its holdings are recorded in.
//
// A pair rather than many sources into one account, and that decision buys
// more than tidiness. Reconciliation is per-account: what to delete is decided
// by comparing what an account holds against what was read. With one account
// per source, every guard already in [portfolio] — the coverage check, the
// refusal to empty a category — closes over exactly one source's holdings, and
// a source that could not be read simply leaves its account alone. Sharing one
// account would have meant teaching all of them which rows belong to whom.
type Bridge struct {
	Source port.Source
	Ledger port.Ledger
}

// Sync is one configured run.
//
// The dependencies are [port] interfaces, so this package names no site, no
// browser and no mailbox.
type Sync struct {
	Bridges []Bridge

	Reporter port.Reporter

	// AllowEmptyingCategories permits deleting every entry in a category.
	//
	// Off by default, because every mis-read has taken that shape. On when a
	// person says so for one run, because selling out of a category is real and a
	// stop nothing can lift is the mistake this replaced.
	AllowEmptyingCategories bool

	// AllowEmpty permits reconciling against no holdings at all, which deletes
	// everything the account holds.
	//
	// Off by default and never set by the scheduled job: a scrape returning
	// nothing is a bug, and acting on it empties the account. Only a caller that
	// meant it — `mfpp debug mf sync --empty` — turns it on.
	AllowEmpty bool
}

// Result is what a run did, one entry per bridge, in the order they were
// configured.
type Result struct {
	Bridges []BridgeResult
}

// BridgeResult is one source's outcome.
//
// Err is kept beside the plan rather than replacing it. These are writes to a
// financial record, and "what did it manage before it stopped?" is the first
// question worth answering.
type BridgeResult struct {
	Source string
	Assets []asset.Asset
	Plan   portfolio.Plan
	Err    error
}

// Run reads every source and brings each one's account into line with it.
//
// Reading happens for all of them before anything is written. That is the order
// the one-time codes want: every service here mails a code in response to a
// sign-in, and a run that interleaved sign-ins with writes would spread them
// over a longer window for no reason. It also means a failure to read is known
// before the first row is touched.
//
// A source that cannot be read does not stop the others. Its account is left
// exactly as it was — nothing was read, so nothing can be reconciled against it
// — and the run fails at the end naming every source that did not complete. The
// alternative, stopping at the first failure, means an insurance contract whose
// site is down keeps a brokerage account from being updated for the rest of the
// week.
func (s Sync) Run(ctx context.Context) (Result, error) {
	result := Result{Bridges: make([]BridgeResult, len(s.Bridges))}
	for i, bridge := range s.Bridges {
		result.Bridges[i].Source = bridge.Source.ID()
	}

	s.phase("read holdings")
	held := make([]asset.Holdings, len(s.Bridges))
	for i, bridge := range s.Bridges {
		holdings, err := s.read(ctx, bridge)
		if err != nil {
			s.fail(&result.Bridges[i], err)
			continue
		}
		held[i] = holdings
		result.Bridges[i].Assets = holdings.Assets
		if s.Reporter != nil {
			s.Reporter.ReadResult(bridge.Source.ID(), holdings.Assets)
		}
	}

	s.phase("record holdings")
	for i, bridge := range s.Bridges {
		if result.Bridges[i].Err != nil {
			continue
		}
		plan, err := s.record(ctx, bridge, held[i])
		result.Bridges[i].Plan = plan
		if err != nil {
			s.fail(&result.Bridges[i], err)
		}
	}
	return result, result.err()
}

// read signs in to one source and takes its holdings.
func (s Sync) read(ctx context.Context, bridge Bridge) (asset.Holdings, error) {
	if err := bridge.Source.SignIn(ctx); err != nil {
		return asset.Holdings{}, err
	}
	held, err := bridge.Source.Holdings(ctx)
	if err != nil {
		return asset.Holdings{}, err
	}
	// An empty read aborts before the account is touched. Reconciliation deletes
	// what is no longer held, so a scrape that silently returned nothing would
	// empty the account — a failure that looks like a successful run with no
	// holdings.
	if len(held.Assets) == 0 && !s.AllowEmpty {
		return asset.Holdings{}, errors.New("it reported no holdings at all; refusing " +
			"to reconcile, which would empty the account it records into")
	}
	return held, nil
}

// record signs in to one account and brings it into line.
func (s Sync) record(ctx context.Context, bridge Bridge, held asset.Holdings) (portfolio.Plan, error) {
	if err := bridge.Ledger.SignIn(ctx); err != nil {
		return portfolio.Plan{}, err
	}
	return s.reconcile(ctx, bridge, held)
}

// fail records an error against one bridge and says so at the moment it
// happens.
//
// Reported here rather than only in the returned error, because the run carries
// on afterwards: a failure that is only mentioned at the end arrives after
// everything that succeeded, about something that happened minutes earlier.
func (s Sync) fail(into *BridgeResult, err error) {
	into.Err = err
	if s.Reporter != nil {
		s.Reporter.Failed(into.Source, err)
	}
}

// err collects what went wrong, naming each source.
func (r Result) err() error {
	var errs []error
	for _, b := range r.Bridges {
		if b.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", b.Source, b.Err))
		}
	}
	return errors.Join(errs...)
}

// Failed lists the sources that did not complete.
func (r Result) Failed() []string {
	var out []string
	for _, b := range r.Bridges {
		if b.Err != nil {
			out = append(out, b.Source)
		}
	}
	return out
}

// reconcile plans against what the account holds, then applies the plan.
func (s Sync) reconcile(ctx context.Context, bridge Bridge, held asset.Holdings) (portfolio.Plan, error) {
	recorded, err := bridge.Ledger.Recorded(ctx)
	if err != nil {
		return portfolio.Plan{}, err
	}
	if dup := portfolio.DuplicateName(recorded); dup != "" {
		return portfolio.Plan{}, fmt.Errorf(
			"the account holds more than one entry named %q; nothing here can tell them "+
				"apart, so remove the extra by hand before syncing", dup)
	}

	plan := portfolio.Reconcile(recorded, held.Assets)
	if s.Reporter != nil {
		s.Reporter.Planned(bridge.Source.ID(), plan)
	}
	// Skipped when the caller asked for an empty read and got one: that is a
	// request to remove everything, and there are no categories to check it
	// against. An AllowEmpty run that read something still has to pass.
	askedToEmpty := s.AllowEmpty && len(held.Assets) == 0
	if !askedToEmpty {
		if err := plan.CheckCoverage(held.Categories); err != nil {
			return plan, err
		}
		// Coverage asks whether the category was looked at. This asks whether
		// anything was seen — a page was fetched, verified, and still handed back
		// a different view's figures, which is how a category holding two 銘柄
		// came to read as empty and lose both.
		if !s.AllowEmptyingCategories {
			if err := portfolio.CheckCategoryEmptied(recorded, held.Assets); err != nil {
				return plan, err
			}
		}
	}

	wanted := make(map[string]asset.Asset, len(held.Assets))
	for _, a := range held.Assets {
		wanted[a.Name] = a
	}
	for _, step := range plan.Steps {
		if step.Action == portfolio.ActionUnchanged {
			continue
		}
		if err := s.apply(ctx, bridge, step, wanted[step.Name]); err != nil {
			return plan, err
		}
	}
	if s.Reporter != nil {
		s.Reporter.Applied(bridge.Source.ID(), plan)
	}
	return plan, nil
}

// apply performs one step and confirms it took effect.
func (s Sync) apply(ctx context.Context, bridge Bridge, step portfolio.Step, want asset.Asset) error {
	var err error
	switch step.Action {
	case portfolio.ActionCreate:
		err = bridge.Ledger.Create(ctx, want)
	case portfolio.ActionUpdate:
		err = bridge.Ledger.Update(ctx, want)
	case portfolio.ActionDelete:
		err = bridge.Ledger.Delete(ctx, step.Name)
	default:
		return fmt.Errorf("unknown action %q for %q", step.Action, step.Name)
	}
	if err != nil {
		return err
	}
	return s.confirm(ctx, bridge, step, want)
}

// confirm reads the account back rather than trusting what the write returned.
//
// An account reached over the web can report success for a write it did not
// apply — and has. What it holds afterwards is not ambiguous.
func (s Sync) confirm(ctx context.Context, bridge Bridge, step portfolio.Step, want asset.Asset) error {
	attempts, delay := 1, time.Duration(0)
	if eventual, ok := bridge.Ledger.(port.EventuallyConsistent); ok {
		if n := eventual.ConfirmationAttempts(); n > attempts {
			attempts = n
		}
		delay = eventual.ConfirmationDelay()
	}

	var verr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("verify %s %q: %w", step.Action, step.Name, ctx.Err())
			case <-timer.C:
			}
		}

		after, err := bridge.Ledger.Recorded(ctx)
		if err != nil {
			return fmt.Errorf("verify %s %q: %w", step.Action, step.Name, err)
		}

		var found *asset.Asset
		for i := range after {
			if after[i].Name == step.Name {
				found = &after[i]
				break
			}
		}

		verr = step.Confirm(want, found)
		if verr == nil {
			return nil
		}
	}
	// Only now: the service's own message cannot decide whether a write worked,
	// but once something is known to have gone wrong it is worth quoting.
	if explainer, ok := bridge.Ledger.(port.Explainer); ok {
		if reason := explainer.LastRejection(); reason != "" {
			return fmt.Errorf("%w — the service said: %s", verr, reason)
		}
	}
	return verr
}

// phase announces a stage when there is anyone listening.
func (s Sync) phase(name string) {
	if s.Reporter != nil {
		s.Reporter.Phase(name)
	}
}
