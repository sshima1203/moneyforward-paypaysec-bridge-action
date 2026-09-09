package manualasset

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/chrome/cookiestore"
)

// origin is the app's own host, for building absolute URLs.
const origin = "https://moneyforward.com"

// Account is one MoneyForward manual account — the container the individual
// 資産 entries live in.
//
// A type rather than threading (ctx, client, assetID) through every read
// and write: those two values have to agree for any of it to mean anything, and
// separately they are just an HTTP client and a 43-character string.
type Account struct {
	// HTTP carries the signed-in session.
	HTTP *http.Client

	// AssetID identifies the manual asset, and is the value in its page URL —
	// the one thing a user has to copy out of MoneyForward by hand.
	//
	// Named for what the UI calls it (手入力資産) rather than for the URL path it
	// sits under (/accounts/). The page also carries
	// [Writer.SubAssetID], a different 43-character hash that this
	// program scrapes rather than being given, and two values one word apart is
	// how the wrong one gets pasted in.
	AssetID string

	// OnRead, if set, is handed every set of entries as they are read.
	//
	// It exists so the scheduled job can register the figures with the Actions
	// log masker. These are balances the account already held, which this
	// program never chose and so cannot have masked in advance — and a
	// verification failure names one: "the recorded value is %d, not %d". That
	// message fires precisely when the recorded figure is not the one we sent,
	// which is to say when it is a number nothing else has seen.
	OnRead func([]Entry)
}

// FromBrowser builds an account addressed over plain HTTP, borrowing the
// cookies of a browser that has already signed in.
func FromBrowser(ctx context.Context, assetID string) (Account, error) {
	client, err := cookiestore.HTTPClientFor(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("borrow session: %w", err)
	}
	return Account{HTTP: client, AssetID: assetID}, nil
}

// URL is the account's page.
func (a Account) URL() string {
	return origin + "/accounts/show_manual/" + a.AssetID
}

// Entries returns the rows currently recorded in the account.
//
// The single door onto the account's contents — Sync, its verification step and
// its token refresh all come through here — which is what makes it the place to
// hang [Account.OnRead].
func (a Account) Entries(ctx context.Context) ([]Entry, error) {
	page, err := a.load(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := page.entries()
	if err != nil {
		return nil, err
	}
	if a.OnRead != nil {
		a.OnRead(entries)
	}
	return entries, nil
}

// Writer reads the account page for what a write needs.
//
// Called immediately before each write rather than cached: the CSRF token is
// per-session *and* per-rendering, so one held across a sequence of writes
// starts being rejected partway through.
func (a Account) Writer(ctx context.Context) (Writer, error) {
	page, err := a.load(ctx)
	if err != nil {
		return Writer{}, err
	}
	return page.writerFor(a)
}

// load GETs the account page.
func (a Account) load(ctx context.Context) (accountPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("load %s: %w", a.URL(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", a.URL(), err)
	}
	return accountPage(body), nil
}

// Subclasses reports the 資産クラス options the create form offers.
//
// A read, for finding out what an instrument kind has to be mapped to. The
// numbering is MoneyForward's and appears nowhere in their documentation, so
// the form is the only statement of it there is.
func (a Account) Subclasses(ctx context.Context) ([]SubclassOption, error) {
	page, err := a.load(ctx)
	if err != nil {
		return nil, err
	}
	return page.subclasses()
}

// EntryNamed finds one current row by name.
//
// Exported because a write needs the identifiers and the per-form token the
// account gave that row, and only the account can say what they are.
func (a Account) EntryNamed(ctx context.Context, name string) (Entry, bool) {
	entries, err := a.Entries(ctx)
	if err != nil {
		return Entry{}, false
	}
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}
