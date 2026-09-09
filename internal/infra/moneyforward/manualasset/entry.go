// Package manualasset reads and writes the 資産 rows of a MoneyForward manual
// account.
//
// Separate from the parent package, which drives a browser to sign in. The two
// share nothing but the site: this one speaks plain HTTP with the resulting
// session, and its form field names, CSRF patterns and endpoint paths have no
// business being reachable from the sign-in code.
//
// Everything here was CONFIRMED against the live account page on 2026-08-01.
// The account page renders fine in a browser but keeps a headless renderer busy
// long enough that querying its DOM times out, and none of that work is needed
// to submit what are plain form posts.
package manualasset

import (
	"fmt"
	"strconv"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/asset"
)

// AssetSubclass identifies what kind of instrument an entry holds. The values
// come from the create form's own select.
type AssetSubclass int

const (
	SubclassDomesticStock AssetSubclass = 14 // 国内株
	SubclassUSStock       AssetSubclass = 15 // 米国株
	SubclassOtherStock    AssetSubclass = 17 // その他株式
	SubclassMutualFund    AssetSubclass = 12 // 投資信託

	// SubclassSavingsInsurance is 積立型保険.
	//
	// CONFIRMED 2026-08-29 from the create form's own select, which offers
	// sixty-odd classes; see `mfpp debug mf subclasses`.
	//
	// The alternative considered was 外債 (9), which describes what the contract
	// is invested in rather than what is owned. Either files a correct figure;
	// they differ only in how the portfolio reads, and this one matches the
	// thing the statement is about.
	SubclassSavingsInsurance AssetSubclass = 32
)

// SubclassFor is how MoneyForward files an instrument of this kind.
//
// The translation lives here, next to the identifiers it produces, because they
// are this site's numbering and no one else's business. PayPay 証券's target
// table used to carry them directly.
func SubclassFor(kind asset.Kind) (AssetSubclass, error) {
	switch kind {
	case asset.DomesticStock:
		return SubclassDomesticStock, nil
	case asset.USStock:
		return SubclassUSStock, nil
	case asset.OtherStock:
		return SubclassOtherStock, nil
	case asset.MutualFund:
		return SubclassMutualFund, nil
	case asset.SavingsInsurance:
		return SubclassSavingsInsurance, nil
	}
	// Not a default on the switch: an unrecognised kind must not quietly become
	// whatever the zero value files as.
	return 0, fmt.Errorf("no MoneyForward 資産クラス for %s", kind)
}

// KindOf is SubclassFor backwards: what a recorded subclass means.
//
// Needed to read the account back in the same terms it is written in. An
// unrecognised value becomes the zero Kind, which nothing will accept for a
// write — reading is not the place to refuse.
func KindOf(subclass AssetSubclass) asset.Kind {
	switch subclass {
	case SubclassDomesticStock:
		return asset.DomesticStock
	case SubclassUSStock:
		return asset.USStock
	case SubclassOtherStock:
		return asset.OtherStock
	case SubclassMutualFund:
		return asset.MutualFund
	case SubclassSavingsInsurance:
		return asset.SavingsInsurance
	}
	return asset.KindUnknown
}

// MaxEntryNameLength is the limit MoneyForward enforces on an entry's name,
// established by exceeding it: 名称は20文字以内でお願いします.
//
// The site announces that as a 200 with the page re-rendered, so a name over
// the limit is dropped with no error anywhere — which is how two of five
// holdings went missing from a run that reported success.
const MaxEntryNameLength = 20

// Entry is one row in the manual portfolio.
//
// Existing rows carry two identifiers, and they are not interchangeable: an
// update addresses the numeric one, a delete the hashed one. Keeping both is
// simpler than deriving either.
type Entry struct {
	// ID is the numeric identifier an update needs. Empty for a row not yet
	// created.
	ID string

	// Hash is the identifier a delete needs. Empty for a row not yet created.
	Hash string

	// Token is the CSRF token from this row's own edit form. Tokens here are
	// per-form, not per-session: reusing the create form's token on an update
	// gets the request treated as forged, which Rails answers by nullifying the
	// session and redirecting to sign-in — indistinguishable from an expired
	// login.
	Token string

	// SubAccountID identifies the manual account this row belongs to. It is
	// needed when rows are read from the all-assets portfolio page.
	SubAccountID string

	Name string

	// Yen is the current valuation.
	Yen int64

	// AcquisitionYen is what the holding cost, and whether it is known.
	//
	// MoneyForward computes 評価損益 from this, and a blank one is not treated
	// as unknown: it takes the cost to equal the current value and reports a
	// profit of exactly zero. Leaving it out is therefore not neutral.
	AcquisitionYen int64
	HasAcquisition bool

	Subclass AssetSubclass
}

// Amounts is every yen figure this entry could put in a message.
//
// On the type that holds them rather than assembled by a caller, so that
// "which of these fields are money" stays one answer. The scheduled job
// registers them with the Actions log masker.
func (e Entry) Amounts() []int64 {
	return []int64{e.Yen, e.AcquisitionYen}
}

// entriedPrice renders the acquisition cost for the form, empty when unknown.
func (e Entry) entriedPrice() string {
	if !e.HasAcquisition {
		return ""
	}
	return strconv.FormatInt(e.AcquisitionYen, 10)
}
