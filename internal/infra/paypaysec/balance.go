package paypaysec

import (
	"context"
	"errors"
	"fmt"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/asset"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/assetname"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/money"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/valuation"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/paypaysec/investapi"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/paypaysec/pagescan"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/paypaysec/selector"
)

// Holding is one 銘柄 and its valuation.
//
// The text fields are what the page said, kept alongside the numbers so a
// recorded figure can be traced back to the words it came from. The parsed ones
// are filled by [Reading.parse] and [Reading.fillAcquisition].
type Holding struct {
	// Name is the 銘柄 as the site labels it, e.g. "テスト電機".
	Name string

	// Ref is the site's own link for the holding, "/trade/brand/35/0" — present
	// on the 株 template, empty on 投資信託. It decides which of the two ways of
	// establishing an acquisition cost applies.
	Ref string

	InvestText string

	// GainText is the profit as the list showed it. Never parsed when Ref is
	// set: the 株 template abbreviates it ("+3.7万"), and a rounded figure has no
	// business near the one being recorded.
	GainText string

	Yen    int64
	HasYen bool

	// AcquisitionYen is what this holding cost, and whether that is known.
	// MoneyForward needs it: without one it assumes cost equals value and
	// reports a profit of zero for everything.
	AcquisitionYen int64
	HasAcquisition bool

	// DetailYen is the valuation this holding's own page stated, when it has
	// one. Kept rather than compared and discarded: it is a figure read off a
	// page, so it has to be maskable like the rest, and the disagreement it
	// reports is easier to read with both numbers recorded.
	DetailYen    int64
	HasDetailYen bool
}

// Reading is everything the page offered for one target, plus what Go made of
// it. Nothing is reconciled here; see [Reading.Amount] for that.
type Reading struct {
	// Target is the entry this reading came from.
	Target selector.Target

	// Figures is the page's own words, exactly as [pagescan.Load] reported them.
	Figures pagescan.Figures

	// Holdings is every 銘柄 row, with the numbers parsed out of it.
	Holdings []Holding

	// Parsed from Figures. The Has* flags separate "the page showed a
	// placeholder" from "the amount is zero" — both are legitimate, and only one
	// of them means there is nothing to add up.
	TotalYen       int64
	HasTotal       bool
	AcquisitionYen int64
	HasAcquisition bool
	GainYen        int64
	HasGain        bool
	HoldingsSumYen int64
	HoldingsParsed int
}

// HoldingCount is how many 銘柄 rows the page listed.
func (r Reading) HoldingCount() int { return len(r.Holdings) }

// readingOf turns one page's text into a reading, without yet parsing it.
func readingOf(t selector.Target, figures pagescan.Figures) Reading {
	r := Reading{
		Target:   t,
		Figures:  figures,
		Holdings: make([]Holding, 0, len(figures.Holdings)),
	}
	for _, row := range figures.Holdings {
		r.Holdings = append(r.Holdings, Holding{
			Name:       row.Name,
			Ref:        row.Ref,
			InvestText: row.InvestText,
			GainText:   row.GainText,
		})
	}
	return r
}

// parse turns the raw strings into yen.
//
// A placeholder is tolerated: an empty holding is a normal state. Anything else
// that fails to parse is an error, because an amount that cannot be read is not
// an amount to guess at.
func (r *Reading) parse() error {
	f := r.Figures
	var err error
	if f.TotalPresent {
		if r.TotalYen, r.HasTotal, err = parseAmountCell(f.TotalRaw); err != nil {
			return fmt.Errorf("%s: 評価額合計: %w", r.Target.Key, err)
		}
	}
	if f.AcquisitionPresent {
		if r.AcquisitionYen, r.HasAcquisition, err = parseAmountCell(f.AcquisitionRaw); err != nil {
			return fmt.Errorf("%s: 投資元本: %w", r.Target.Key, err)
		}
	}
	if f.GainPresent {
		if r.GainYen, r.HasGain, err = parseAmountCell(f.GainRaw); err != nil {
			return fmt.Errorf("%s: 含み益: %w", r.Target.Key, err)
		}
	}

	r.HoldingsSumYen, r.HoldingsParsed = 0, 0
	for i := range r.Holdings {
		h := &r.Holdings[i]
		if h.Name == "" {
			return fmt.Errorf("%s: holding %d has no name; it could not be recorded "+
				"under one", r.Target.Key, i+1)
		}
		yen, ok, perr := parseAmountCell(h.InvestText)
		if perr != nil {
			return fmt.Errorf("%s: holding %q: %w", r.Target.Key, h.Name, perr)
		}
		h.Yen, h.HasYen = yen, ok
		if ok {
			r.HoldingsSumYen += yen
			r.HoldingsParsed++
		}
	}

	// Some PayPay Securities accounts render Robo Savings as totals only: the
	// category has a non-zero valuation, acquisition and gain, but no individual
	// holding rows. Preserve that real balance as one aggregate position. Keep
	// this exception limited to Robo Savings so a missing list in any ordinary
	// stock or fund category remains a hard failure.
	if r.Target.Key == "robo" && len(r.Holdings) == 0 && r.HasTotal && r.TotalYen != 0 {
		r.Holdings = append(r.Holdings, Holding{
			Name:     r.Target.Name,
			GainText: r.Figures.GainRaw,
			Yen:      r.TotalYen,
			HasYen:   true,
		})
		r.HoldingsSumYen = r.TotalYen
		r.HoldingsParsed = 1
	}
	return nil
}

// parseAmountCell reports the amount and whether there was one at all.
func parseAmountCell(raw string) (int64, bool, error) {
	yen, err := money.ParseYen(raw)
	if errors.Is(err, money.ErrNoValue) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return yen, true, nil
}

// figures projects the reading onto the domain's view of it: amounts, with the
// page they came from left behind.
func (r Reading) figures() valuation.Figures {
	f := valuation.Figures{
		// The page's own words, so a refusal can be read against the screen.
		Labels: valuation.Labels{
			Total:       "評価額合計",
			Acquisition: "投資元本",
			Gain:        "含み益",
		},
		Total:       amountOf(r.Figures.TotalPresent, r.HasTotal, r.TotalYen),
		Acquisition: amountOf(r.Figures.AcquisitionPresent, r.HasAcquisition, r.AcquisitionYen),
		Gain:        amountOf(r.Figures.GainPresent, r.HasGain, r.GainYen),
	}
	for _, h := range r.Holdings {
		f.Positions = append(f.Positions, valuation.Position{
			Name:  h.Name,
			Value: amountOf(true, h.HasYen, h.Yen),
			Cost:  amountOf(true, h.HasAcquisition, h.AcquisitionYen),
		})
	}
	return f
}

// amountOf maps the page's three states onto the domain's.
func amountOf(present, known bool, yen int64) valuation.Amount {
	switch {
	case !present:
		return valuation.Absent()
	case !known:
		return valuation.Placeholder()
	default:
		return valuation.Yen(yen)
	}
}

// Amount is the page total, once [valuation.Figures] is satisfied the routes
// agree.
//
// The arithmetic is not here: it is about money, not about pages, and it is the
// same judgement whatever reported the figures. What this adds is the context a
// refusal needs to be acted on — which target, and which element to go and look
// at.
func (r Reading) Amount() (int64, error) {
	yen, err := r.figures().Reconciled()
	switch {
	case errors.Is(err, valuation.ErrNotReported):
		return 0, fmt.Errorf("%s: %s not found on %s — the session is probably not authenticated",
			r.Target.Key, selector.ValueTotal, r.Target.URL)
	case err != nil:
		return 0, fmt.Errorf("%s (%s on %s): %w", r.Target.Key, selector.ValueTotal, r.Target.URL, err)
	}
	return yen, nil
}

// Amounts is every yen figure this reading could name in a message.
//
// The reconciliation's own list comes from the package that words those
// messages, so a figure added to a refusal there is covered here without anyone
// remembering to. The rest are figures only this side has.
func (r Reading) Amounts() []int64 {
	amounts := r.figures().Amounts()
	amounts = append(amounts, r.HoldingsSumYen)
	for _, h := range r.Holdings {
		amounts = append(amounts, h.DetailYen)
	}
	return amounts
}

// Texts is every figure as the page spelled it.
//
// A figure that fails to parse reaches the log inside the parse error still in
// the page's own words, which is the balance however it is written.
func (r Reading) Texts() []string {
	texts := []string{r.Figures.TotalRaw, r.Figures.AcquisitionRaw, r.Figures.GainRaw}
	for _, h := range r.Holdings {
		texts = append(texts, h.InvestText, h.GainText)
	}
	return texts
}

// Balances is the outcome of a full read.
//
// App and MiniApp are disjoint sums over [selector.Targets], not two views of one
// figure: every target belongs to exactly one bucket.
type Balances struct {
	App     int64
	MiniApp int64

	// Readings is the per-target breakdown, in [selector.Targets] order. A target missing
	// from that list understates the total silently, so the breakdown is what
	// makes such a gap visible.
	Readings []Reading
}

// Assets flattens the readings into the per-銘柄 records to write.
//
// This is the unit that leaves this package, and it is a domain type: nothing
// downstream should have to know it was scraped. The totals on [Balances] are a
// cross-check and a debugging convenience; nothing writes them anywhere.
//
// A duplicate name is an error rather than a last-write-wins: two holdings
// mapping to one asset would leave whichever ran last, and the result looks
// exactly like a correct balance. Names come from the site at runtime, so no
// static check can rule this out — it has to be caught here.
//
// Holdings whose valuation was a placeholder are skipped: they have no figure
// to record, and writing a zero would assert something the page never said.
func (b Balances) Assets() ([]asset.Asset, error) {
	assets := make([]asset.Asset, 0, len(b.Readings))
	var names assetname.Set

	for _, reading := range b.Readings {
		for _, holding := range reading.Holdings {
			if !holding.HasYen {
				continue
			}
			a := asset.Asset{
				Name:           reading.Target.AssetName(holding.Name),
				Yen:            holding.Yen,
				AcquisitionYen: holding.AcquisitionYen,
				HasAcquisition: holding.HasAcquisition,
				Kind:           reading.Target.Kind,
				Source:         reading.Target.Key,
			}
			if !a.Kind.Valid() {
				return nil, fmt.Errorf("%s: no instrument kind, so %q cannot be filed",
					reading.Target.Key, a.Name)
			}
			if err := names.Add(a.Name, holding.Name+" on "+reading.Target.Key); err != nil {
				return nil, err
			}
			assets = append(assets, a)
		}
	}
	return assets, nil
}

// Total is the account total: both buckets summed.
func (b Balances) Total() int64 {
	return b.App + b.MiniApp
}

// GetBalances reads every entry in [selector.Targets].
//
// It requires an authenticated session; on a logged-out page the label is
// absent and the read fails rather than silently reporting zero.
func (c *Client) GetBalances(ctx context.Context) (Balances, error) {
	var out Balances
	out.Readings = make([]Reading, 0, len(selector.Targets))

	for _, t := range selector.Targets {
		reading, err := Read(ctx, t)
		// Before the error check: Read returns what it managed to gather even
		// when it failed, and a failure is exactly when those figures are about
		// to appear in a message. See [Client.OnRead].
		if c.OnRead != nil {
			c.OnRead(reading)
		}
		// A bucket the account does not have is left out of the read entirely,
		// rather than recorded as empty. Recorded as empty it would be a category
		// this run covered and found nothing in — which is a licence to delete
		// everything under it. Left out, it is a category the run never saw, and
		// [portfolio.Plan.CheckCoverage] refuses to delete from those.
		if errors.Is(err, investapi.ErrNoMiniApp) {
			if c.OnSkip != nil {
				c.OnSkip(t, err)
			}
			continue
		}
		if err != nil {
			return Balances{}, stepErr(StepReadBalance, err)
		}
		out.Readings = append(out.Readings, reading)
		yen, err := reading.Amount()
		if err != nil {
			return Balances{}, stepErr(StepReadBalance, err)
		}
		switch t.Bucket {
		case selector.BucketMiniApp:
			out.MiniApp += yen
		default:
			out.App += yen
		}
	}
	return out, nil
}

// Read turns one target into figures, from wherever that target is read.
//
// The branch lives here, not in [Client.GetBalances], so that every caller gets
// the same answer for the same target. When GetBalances held it, the debug
// command drove the other path and disagreed with the scheduled job about what
// 投資信託 even is — and a debug tool that reads differently from the job is a
// debug tool that cannot reproduce the job's mistakes. The same split, over
// acquisition costs, is what the comment below is about.
//
// It is exported so the debug command can exercise a single target without
// logging in again or reading the other seven.
//
// Deliberately silent: the amount is returned, never logged. Callers decide
// whether the figure may appear in their output, because in CI it must be
// masked before it reaches the workflow log.
func Read(ctx context.Context, t selector.Target) (Reading, error) {
	if t.ViaAPI {
		return readInvestmentTrust(ctx, t)
	}

	figures, err := pagescan.Load(ctx, t)
	if err != nil {
		return Reading{Target: t}, err
	}

	reading := readingOf(t, figures)
	if err := reading.parse(); err != nil {
		return reading, err
	}

	// Filled here rather than by the caller: every path that reads a target
	// needs it, and one that forgot would produce assets whose profit shows as
	// zero — which is what happened when this lived in GetBalances alone and the
	// debug command drove Read directly.
	if err := reading.fillAcquisition(ctx); err != nil {
		return reading, stepErr(StepReadBalance, err)
	}
	return reading, nil
}

// Categories names every target this read covered, in Targets order.
//
// [selector.Target.Category], not Name: the category is what goes in front of a
// holding's name, and the name is what a recorded entry is matched back
// through. Name is the human label and differs for any target with a ShortName.
func (b Balances) Categories() []string {
	out := make([]string, 0, len(b.Readings))
	for _, r := range b.Readings {
		out = append(out, r.Target.Category())
	}
	return out
}
