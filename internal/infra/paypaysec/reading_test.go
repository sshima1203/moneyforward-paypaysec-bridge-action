package paypaysec

import (
	"strings"
	"testing"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/assetname"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/paypaysec/pagescan"
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/paypaysec/selector"
)

// newReading builds a Reading the way [Read] does — from the text a page scan
// reported, before parse turns any of it into numbers.
//
// An empty string means the element was not on the page at all, which is a
// different thing from it being present and blank; only the second is a
// placeholder.
func newReading(target selector.Target, total, acquisition, gain string, holdings ...holdingText) Reading {
	var figures pagescan.Figures
	if total != "" {
		figures.TotalPresent, figures.TotalRaw = true, total
	}
	if acquisition != "" {
		figures.AcquisitionPresent, figures.AcquisitionRaw = true, acquisition
	}
	if gain != "" {
		figures.GainPresent, figures.GainRaw = true, gain
	}
	for _, h := range holdings {
		figures.Holdings = append(figures.Holdings, pagescan.Row{Name: h.name, InvestText: h.invest})
	}
	return readingOf(target, figures)
}

// holdingText is one row as the page listed it, for the cases below.
type holdingText struct{ name, invest string }

// withCosts fills in what fillAcquisition would have established, in order.
func withCosts(r *Reading, costs ...int64) {
	for i, cost := range costs {
		r.Holdings[i].AcquisitionYen, r.Holdings[i].HasAcquisition = cost, true
	}
}

var usa = selector.Target{Key: "usa", Name: "米国株", URL: "https://example.test/usa", Bucket: selector.BucketApp}

func TestReadingParse(t *testing.T) {
	r := newReading(usa, "78万9012円", "60万0000円", "+18万9012円",
		holdingText{"テスト電機", "45万6789円"},
		holdingText{"テスト商事", "33万2223円"},
	)
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}

	if r.TotalYen != 789012 || !r.HasTotal {
		t.Errorf("TotalYen = %d (known=%v)", r.TotalYen, r.HasTotal)
	}
	if r.AcquisitionYen != 600000 || r.GainYen != 189012 {
		t.Errorf("acquisition/gain = %d / %d", r.AcquisitionYen, r.GainYen)
	}
	if r.HoldingsSumYen != 789012 || r.HoldingsParsed != 2 {
		t.Errorf("holdings summed to %d over %d rows", r.HoldingsSumYen, r.HoldingsParsed)
	}
	if r.HoldingCount() != 2 {
		t.Errorf("HoldingCount() = %d", r.HoldingCount())
	}
}

func TestReadingParseAggregatesTotalsOnlyRoboSavings(t *testing.T) {
	robo := selector.Target{Key: "robo", Name: "ロボ貯蓄", URL: "https://example.test/robo", Bucket: selector.BucketApp}
	r := newReading(robo, "789012円", "600000円", "+189012円")
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	if r.HoldingCount() != 1 || r.Holdings[0].Name != "ロボ貯蓄" {
		t.Fatalf("holdings = %#v, want one Robo Savings aggregate", r.Holdings)
	}
	if r.Holdings[0].Yen != 789012 || !r.Holdings[0].HasYen {
		t.Errorf("aggregate value = %d (known=%v)", r.Holdings[0].Yen, r.Holdings[0].HasYen)
	}
	if r.HoldingsSumYen != 789012 || r.HoldingsParsed != 1 {
		t.Errorf("holdings summed to %d over %d rows", r.HoldingsSumYen, r.HoldingsParsed)
	}
}

func TestReadingParseDoesNotAggregateMissingOrdinaryHoldings(t *testing.T) {
	r := newReading(usa, "789012円", "", "")
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	if r.HoldingCount() != 0 {
		t.Fatalf("HoldingCount() = %d, want 0", r.HoldingCount())
	}
	if _, err := r.Amount(); err == nil {
		t.Fatal("Amount() accepted missing ordinary holdings")
	}
}

func TestReadingParseNormalizesUnusedMiniAppPlaceholder(t *testing.T) {
	miniapp := selector.Target{Key: "miniapp", Name: "ミニアプリ", URL: "https://example.test/miniapp", Bucket: selector.BucketMiniApp}
	r := newReading(miniapp, "—", "—", "—")
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	yen, err := r.Amount()
	if err != nil {
		t.Fatalf("Amount() error = %v", err)
	}
	if yen != 0 {
		t.Errorf("Amount() = %d, want 0", yen)
	}
}

// TestReadingParseRejectsAnUnnamedHolding catches a row the page listed but did
// not label. It has a figure and nothing to record it under, which downstream
// becomes the asset "[米国株] ".
func TestReadingParseRejectsAnUnnamedHolding(t *testing.T) {
	r := newReading(usa, "100円", "", "", holdingText{invest: "100円"})
	if err := r.parse(); err == nil {
		t.Fatal("parse() accepted a holding with no name")
	}
}

// TestReadingParseTreatsPlaceholdersAsAbsent keeps an empty holding out of the
// sum without failing the read: "—" is a legitimate state.
func TestReadingParseTreatsPlaceholdersAsAbsent(t *testing.T) {
	r := newReading(usa, "100円", "", "",
		holdingText{"持っている", "100円"},
		holdingText{"空っぽ", "—"},
	)
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	if r.HoldingsParsed != 1 || r.HoldingsSumYen != 100 {
		t.Errorf("parsed %d holdings summing to %d, want 1 and 100", r.HoldingsParsed, r.HoldingsSumYen)
	}
	if r.Holdings[1].HasYen {
		t.Error("the placeholder holding reports a known value")
	}
}

func TestReadingParseRejectsUnreadableAmounts(t *testing.T) {
	r := newReading(usa, "100円", "", "", holdingText{"変な値", "12株"})
	if err := r.parse(); err == nil {
		t.Fatal("parse() accepted an unreadable holding value")
	}
}

func TestReadingAmount(t *testing.T) {
	r := newReading(usa, "78万9012円", "60万0000円", "+18万9012円",
		holdingText{"テスト電機", "45万6789円"},
		holdingText{"テスト商事", "33万2223円"},
	)
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	// As fillAcquisition leaves it: Amount is only ever called on a reading that
	// has been through that step, and 投資元本 is checked against these.
	withCosts(&r, 400000, 200000)

	yen, err := r.Amount()
	if err != nil {
		t.Fatalf("Amount() error = %v", err)
	}
	if yen != 789012 {
		t.Errorf("Amount() = %d, want 789012", yen)
	}
}

// TestReadingAmountRefusesOnDisagreement is the whole point of newReading a page
// three ways. Each of these is a number that would look entirely plausible in a
// financial record.
func TestReadingAmountRefusesOnDisagreement(t *testing.T) {
	tests := []struct {
		name        string
		newReading  Reading
		wantMessage string
	}{
		{
			name: "投資元本 + 含み益 does not reach 評価額合計",
			newReading: newReading(usa, "78万9012円", "60万0000円", "+1万0000円",
				holdingText{"テスト電機", "78万9012円"}),
			wantMessage: "投資元本",
		},
		{
			name: "the holdings do not add up to the total",
			newReading: newReading(usa, "78万9012円", "", "",
				holdingText{"テスト電機", "45万6789円"}),
			wantMessage: "holdings sum to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.newReading.parse(); err != nil {
				t.Fatalf("parse() error = %v", err)
			}
			got, err := tt.newReading.Amount()
			if err == nil {
				t.Fatalf("Amount() = %d, want a refusal", got)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("Amount() error = %v, want it to mention %q", err, tt.wantMessage)
			}
		})
	}
}

// TestReadingAmountChecksAcquisitionSum guards the figure gathered one detail
// page at a time. A single page read wrongly would otherwise land in
// MoneyForward as a wrong 評価損益 with nothing to catch it.
func TestReadingAmountChecksAcquisitionSum(t *testing.T) {
	r := newReading(usa, "78万9012円", "60万0000円", "+18万9012円",
		holdingText{"テスト電機", "45万6789円"},
		holdingText{"テスト商事", "33万2223円"},
	)
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	// Costs that do not add up to 投資元本.
	r.Holdings[0].AcquisitionYen, r.Holdings[0].HasAcquisition = 400000, true
	r.Holdings[1].AcquisitionYen, r.Holdings[1].HasAcquisition = 150000, true

	if _, err := r.Amount(); err == nil {
		t.Fatal("Amount() accepted holding costs that do not sum to 投資元本")
	}

	// And accepts them when they do.
	r.Holdings[1].AcquisitionYen = 200000
	if _, err := r.Amount(); err != nil {
		t.Errorf("Amount() = %v with costs that do sum correctly", err)
	}
}

func TestReadingAmountOnAnUnauthenticatedPage(t *testing.T) {
	r := newReading(usa, "", "", "")
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	_, err := r.Amount()
	if err == nil {
		t.Fatal("Amount() succeeded with no 評価額合計 on the page")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("Amount() error = %v, want it to name the likely cause", err)
	}
}

// TestReadingAmountRejectsAPlaceholderTotal separates "the page showed —" from
// "the balance is zero". Only one of those is a figure.
func TestReadingAmountRejectsAPlaceholderTotal(t *testing.T) {
	r := newReading(usa, "—", "", "")
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	if _, err := r.Amount(); err == nil {
		t.Fatal("Amount() accepted a placeholder as a total")
	}
}

func TestBalancesTotal(t *testing.T) {
	b := Balances{App: 789012, MiniApp: 250000}
	if got := b.Total(); got != 1039012 {
		t.Errorf("Total() = %d, want 1039012", got)
	}
}

// TestReadingAmountRejectsATotalWithNoHoldings is the second way a page can
// report a plausible wrong answer.
//
// If the 保有銘柄 list fails to render, the total is still there and there are no
// rows — and both holdings checks are guarded on having rows, so both are
// skipped. The read then succeeds with nothing to record, the other targets keep
// the run from looking empty, and the reconciliation deletes this bucket's
// entries as no longer held.
func TestReadingAmountRejectsATotalWithNoHoldings(t *testing.T) {
	r := newReading(usa, "25万1234円", "", "")
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	if _, err := r.Amount(); err == nil {
		t.Fatal("Amount() accepted a page that holds money and lists no 銘柄")
	}

	// An empty category is the legitimate version of the same shape.
	empty := newReading(usa, "0円", "", "")
	if err := empty.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	if yen, err := empty.Amount(); err != nil || yen != 0 {
		t.Errorf("Amount() on an empty category = %d, %v; want 0 and no error", yen, err)
	}
}

// TestReadingAmountRefusesAPartiallyCostedPage covers a check that used to
// disable itself.
//
// The holdings' costs must add up to 投資元本, and that was only checked when
// every holding had one. So a single misread detail page passed unnoticed among
// others that were fine — and no one downstream can catch it: MoneyForward's
// read-back confirms the cost that was sent, not the cost that was right.
func TestReadingAmountRefusesAPartiallyCostedPage(t *testing.T) {
	r := newReading(usa, "78万9012円", "60万0000円", "+18万9012円",
		holdingText{"テスト電機", "45万6789円"},
		holdingText{"テスト商事", "33万2223円"},
	)
	if err := r.parse(); err != nil {
		t.Fatalf("parse() error = %v", err)
	}
	withCosts(&r, 400000) // the second holding's detail page gave nothing

	_, err := r.Amount()
	if err == nil {
		t.Fatal("Amount() accepted a 投資元本 it could not check against the holdings")
	}
	// Naming the holding, because the next question is which page to go and look at.
	if !strings.Contains(err.Error(), "テスト商事") {
		t.Errorf("Amount() error = %v, want it to name the holding with no cost", err)
	}
}

// TestCategoriesMatchTheNamesTheyLabel ties the two ends of the coverage check
// together.
//
// A run reports which categories it covered; the ledger's entries carry the
// same category as a prefix. If the two are taken from different fields they
// agree for whichever targets happen to have one field equal to the other — and
// silently disagree for the rest, which shows up only when a deletion is
// planned and then refuses a deletion from a category that was read.
//
// So this reads the category back out of a name the target itself produced,
// rather than restating it.
func TestCategoriesMatchTheNamesTheyLabel(t *testing.T) {
	var readings []Reading
	for _, target := range selector.Targets {
		readings = append(readings, Reading{Target: target})
	}
	covered := Balances{Readings: readings}.Categories()

	if len(covered) != len(selector.Targets) {
		t.Fatalf("Categories() returned %d for %d targets", len(covered), len(selector.Targets))
	}
	for i, target := range selector.Targets {
		name := target.AssetName("テスト電機")
		want, ok := assetname.CategoryOf(name)
		if !ok {
			t.Errorf("%s: AssetName() = %q, which carries no category", target.Key, name)
			continue
		}
		if covered[i] != want {
			t.Errorf("%s: Categories() says %q but its names are written %q — "+
				"a delete from this category would be refused as unread",
				target.Key, covered[i], want)
		}
	}
}
