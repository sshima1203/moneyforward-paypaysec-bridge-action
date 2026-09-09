package adapter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/asset"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/moneyforward"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/moneyforward/manualasset"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/otp"
)

// MoneyForwardSession is one sign-in to MoneyForward.
//
// Separate from the account because there are several accounts and one login.
// Every source records into its own manual account, and signing in per account
// would mail a one-time code per account — for the same person, to the same
// mailbox, seconds apart, on a service that stops sending them after a handful.
//
// Used through a pointer, and it signs in at most once however many ledgers ask.
type MoneyForwardSession struct {
	Client *moneyforward.Client

	// Browser is the chromedp context the sign-in is driven through.
	Browser context.Context

	// Codes supplies the one-time code the login needs.
	Codes otp.Source

	// OnLogin, if set, is told whether a challenge was presented.
	OnLogin func(challenged bool)

	once sync.Once
	err  error
}

// SignIn logs in, once, and reports the same outcome to every later caller.
//
// A failure is remembered rather than retried: a second attempt would mail a
// second code, and whatever stopped the first — wrong password, no code, a
// challenge nobody answered — is not something a retry fixes.
func (s *MoneyForwardSession) SignIn() error {
	s.once.Do(func() {
		result, err := s.Client.Login(s.Browser, s.Codes)
		if err != nil {
			if step := moneyforward.StepOf(err); step != "" {
				s.err = fmt.Errorf("moneyforward: login failed at %s: %w", step, err)
				return
			}
			s.err = fmt.Errorf("moneyforward: login: %w", err)
			return
		}
		if s.OnLogin != nil {
			s.OnLogin(result.OTPRequired)
		}
	})
	return s.err
}

// MoneyForwardLedger records one source's holdings in one MoneyForward manual
// account.
//
// Used through a pointer: the account is opened once at sign-in, and what the
// site last said about a write is kept for [MoneyForwardLedger.LastRejection].
type MoneyForwardLedger struct {
	// Session is the sign-in, shared with every other account written in the
	// same run.
	Session *MoneyForwardSession

	// AssetID identifies the manual account the entries live in.
	AssetID string

	// OnRead, if set, is handed the account's entries as they are read. See
	// [manualasset.Account.OnRead].
	OnRead func([]manualasset.Entry)

	// account speaks plain HTTP carrying the browser's session: the account page
	// renders fine but keeps a headless renderer busy long enough that querying
	// its DOM times out, and none of that work is needed to submit a form.
	account manualasset.Account

	// lastRejection is what the site said about the most recent write.
	lastRejection string
}

// SignIn ensures the shared login has happened, then opens this account.
func (l *MoneyForwardLedger) SignIn(context.Context) error {
	if err := l.Session.SignIn(); err != nil {
		return err
	}
	account, err := manualasset.FromBrowser(l.Session.Browser, l.AssetID)
	if err != nil {
		return fmt.Errorf("moneyforward: %w", err)
	}
	account.OnRead = l.OnRead
	l.account = account
	return nil
}

// Recorded is what the account holds now.
func (l *MoneyForwardLedger) Recorded(ctx context.Context) ([]asset.Asset, error) {
	entries, err := l.account.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("moneyforward: %w", err)
	}
	out := make([]asset.Asset, 0, len(entries))
	for _, e := range entries {
		out = append(out, asset.Asset{
			Name:           e.Name,
			Yen:            e.Yen,
			AcquisitionYen: e.AcquisitionYen,
			HasAcquisition: e.HasAcquisition,
			Kind:           manualasset.KindOf(e.Subclass),
		})
	}
	return out, nil
}

// Create adds one entry.
func (l *MoneyForwardLedger) Create(ctx context.Context, a asset.Asset) error {
	writer, entry, err := l.prepare(ctx, a)
	if err != nil {
		return err
	}
	res, err := writer.Create(ctx, entry)
	l.lastRejection = res.RejectionReason()
	return wrap(err)
}

// Update changes one, addressing it by the identifiers the account gave it.
func (l *MoneyForwardLedger) Update(ctx context.Context, a asset.Asset) error {
	writer, entry, err := l.prepare(ctx, a)
	if err != nil {
		return err
	}
	// The row's own edit-form token, not the create form's: tokens here are
	// per-form, and reusing the wrong one gets the request treated as forged.
	existing, ok := l.account.EntryNamed(ctx, a.Name)
	if !ok {
		return fmt.Errorf("moneyforward: update %q: it is not recorded", a.Name)
	}
	entry.ID, entry.Hash, entry.Token = existing.ID, existing.Hash, existing.Token

	res, err := writer.Update(ctx, entry)
	l.lastRejection = res.RejectionReason()
	return wrap(err)
}

// Delete removes one by name.
func (l *MoneyForwardLedger) Delete(ctx context.Context, name string) error {
	writer, err := l.account.Writer(ctx)
	if err != nil {
		return wrap(err)
	}
	existing, ok := l.account.EntryNamed(ctx, name)
	if !ok {
		return fmt.Errorf("moneyforward: delete %q: it is not recorded", name)
	}
	res, err := writer.Delete(ctx, existing)
	l.lastRejection = res.RejectionReason()
	return wrap(err)
}

// UseAccount hands in an account opened elsewhere, for a caller that already
// has a session — the debug commands, which persist one so the write path can be
// exercised without spending a one-time code per attempt.
func (l *MoneyForwardLedger) UseAccount(account manualasset.Account) {
	l.account = account
}

// LastRejection is what the site said about the most recent write.
//
// Consulted only once a read-back has established that something went wrong;
// see [port.Explainer].
func (l *MoneyForwardLedger) LastRejection() string { return l.lastRejection }

// MoneyForward updates a manual portfolio asynchronously. A create can return
// successfully while the account page still shows the previous rows for a few
// seconds, so confirmation must tolerate that short propagation window.
func (l *MoneyForwardLedger) ConfirmationAttempts() int { return 13 }
func (l *MoneyForwardLedger) ConfirmationDelay() time.Duration { return 5 * time.Second }

// prepare reads the account page for a token valid against this rendering, and
// renders the asset as an entry.
//
// The token is per-session and per-rendering, so it is re-read before each write
// rather than held across a sequence of them.
func (l *MoneyForwardLedger) prepare(ctx context.Context, a asset.Asset) (manualasset.Writer, manualasset.Entry, error) {
	writer, err := l.account.Writer(ctx)
	if err != nil {
		return manualasset.Writer{}, manualasset.Entry{}, wrap(err)
	}
	subclass, err := manualasset.SubclassFor(a.Kind)
	if err != nil {
		return manualasset.Writer{}, manualasset.Entry{}, fmt.Errorf("moneyforward: %q: %w", a.Name, err)
	}
	return writer, manualasset.Entry{
		Name:           a.Name,
		Yen:            a.Yen,
		AcquisitionYen: a.AcquisitionYen,
		HasAcquisition: a.HasAcquisition,
		Subclass:       subclass,
	}, nil
}

// wrap names the service in an error, or passes nil through.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("moneyforward: %w", err)
}
