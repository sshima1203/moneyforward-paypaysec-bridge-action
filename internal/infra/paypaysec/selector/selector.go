// Package selector holds every PayPay 証券 DOM selector, URL and target, plus
// the page scripts that read them.
//
// Separate from the code that drives the site because these are findings, not
// logic: each was confirmed against the live pages on a particular day, and each
// is what breaks when the front end changes. Keeping them in one package makes
// that blast radius a directory rather than a grep.
package selector

import (
	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/asset"
	"regexp"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/assetname"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/otp"
)

// All PayPay 証券 DOM selectors, URLs and extraction snippets live here so a
// front-end change is a one-file fix.
//
// Two tiers, deliberately distinguished:
//
//   - CONFIRMED consts — observed on the live site (2026-05-23).
//   - *Candidates maps — UNCONFIRMED. The OTP page only appears on a device the
//     site does not recognise, and no run has ever reached it. Rather than
//     commit to one guess and hang on WaitVisible, the login probes every
//     candidate at once and reports which matched.

// OTPMail is everything the shared OTP sources need for this service.
// CONFIRMED 2026-08-01 against a real message.
var OTPMail = otp.MailSpec{
	Label:   "PayPay 証券",
	Query:   otpMailQuery,
	Pattern: otpCodePattern,
	Digits:  OTPDigits,
}

// otpMailQuery names where this service's mail comes from.
//
// The same address also sends 【PayPay証券】ログイン通知 moments later, inside the
// same freshness window — but that message carries no labelled code, so
// otpCodePattern refuses it. A subject filter would do the same job and add a
// localized string to the path, which is what broke the MoneyForward side.
//
// in:anywhere is there because transactional mail can land in Spam, and a code
// that arrived but was filtered is indistinguishable from one that never came.
const otpMailQuery = `from:no-reply@cs.paypay-sec.co.jp in:anywhere`

// otpCodePattern pulls the code out of the mail body. CONFIRMED 2026-08-01
// against a real message, whose relevant line reads:
//
//	認証コード:AB-123456
//
// Anchored to the label rather than hunting for any six digits: the same body
// carries a phone number and a registration number, and a generic pattern only
// works until the day one of those changes shape. The two-letter prefix is
// matched but not captured — it is the page's anti-phishing marker, and the form
// takes digits only.
//
// 認証コード is a Japanese string, and MoneyForward's equivalent turned out to
// depend on where the login came from. This service sends Japanese to a login
// from a US runner, so the same trap is not sprung here — but if that changes,
// this is where it breaks, and there is no confirmed English message to write a
// second branch against. Guessing one would put an unverified string next to
// the CONFIRMED ones.
var otpCodePattern = regexp.MustCompile(`認証コード[:：]\s*(?:[A-Za-z]{1,4}-)?(\d{6})`)

const (
	// Origin is the site's own host, for resolving the relative refs a 銘柄 row
	// links to.
	Origin = "https://www.paypay-sec.co.jp"

	LoginURL = Origin + "/login/" // CONFIRMED

	// Login form. CONFIRMED 2026-05-23.
	MemberIDInput = "#MEMBER_ID"
	PasswordInput = "#PASSWORD"
	LoginSubmit   = "#login_submit" // an <a> styled as a button (javascript:void(0))

	// Post-login marker: the header link to the ミニアプリ view, present once the
	// trade dashboard renders. CONFIRMED 2026-05-23.
	PostLoginAnchor = `a[href*="country=pp"]`

	// URLMiniApp is the ミニアプリ trade category. CONFIRMED.
	URLMiniApp = "https://www.paypay-sec.co.jp/trade?country=pp"

	// URLInvestmentTrust is the 投資信託 page. Unlike the /trade views it holds
	// two totals — PayPay 証券アプリ and ミニアプリ — at one URL, which is why the
	// two targets on it are read over HTTP rather than off the page. Kept as the
	// target's URL because it is where a human goes to check the figures by hand.
	// See [Targets].
	URLInvestmentTrust = "https://www.paypay-sec.co.jp/investment_trust/"
)

// Bucket says which subtotal a target contributes to. The two are disjoint.
type Bucket string

const (
	// BucketApp is the PayPay 証券アプリ side.
	BucketApp Bucket = "app"
	// BucketMiniApp is the ミニアプリ side.
	BucketMiniApp Bucket = "miniapp"
)

// Target is one 評価額合計 that has to be read, and what it represents.
//
// Most targets are a URL on their own. 投資信託 is not: it presents both buckets
// at a single URL, so a target also says whether it is read from the page or from
// the endpoints behind it. Modelling that as part of the target keeps the reader
// uniform — every entry says where its figure comes from.
//
// Name and Bucket exist because the figure alone is not useful downstream: a
// balance has to be attributable to what produced it, both to be recorded under
// a meaningful name and to be checked later against the site by hand.
type Target struct {
	// Key identifies the target in logs and in the breakdown output. Stable, and
	// safe for config: unlike Name it is ASCII and will not be reworded.
	Key string

	// Name is what this category is called, in the site's own words.
	Name string

	// ShortName is the same thing, abbreviated for the asset-name prefix.
	//
	// MoneyForward caps an entry's name at 20 characters, and "投資信託（ミニアプリ）"
	// alone eats thirteen of them. Empty means Name is already short enough.
	ShortName string

	// URL is the page to load.
	URL string

	// Bucket is the subtotal this target contributes to.
	Bucket Bucket

	// ViaAPI reads this target from the endpoints the page calls instead of from
	// the page.
	//
	// Set for 投資信託, where two buckets share one URL behind a tab. That swap is
	// where every reading failure lived, and no amount of waiting closed it — see
	// the investapi package. A bucket is an endpoint there, so there is no swap to
	// observe.
	ViaAPI bool

	// Kind is what these holdings are, as an instrument — not as a PayPay view.
	// ミニアプリ is a way of buying, and what it holds here is US stock.
	//
	// This was MoneyForward's asset_subclass_id, so PayPay 証券's target table
	// carried the other site's numbering and a change there reached in here. The
	// ledger translates at its own edge now.
	Kind asset.Kind
}

// AssetName renders the MoneyForward manual-asset name for one 銘柄 held under
// this target: "[米国株] テスト電機".
//
// The rules — the length cap and what gets shortened — belong to
// [assetname.Scheme]; this only says which category a holding sits in.
func (t Target) AssetName(holding string) string {
	return t.scheme().For(holding)
}

// scheme is how this target labels its holdings.
func (t Target) scheme() assetname.Scheme {
	return assetname.Scheme{Category: t.Category()}
}

// Category is the label this target's holdings are recorded under, and the
// string a recorded name can be traced back through.
//
// Exported because two places need the same answer: the name written into the
// ledger, and the list of categories a run says it covered. They were taking it
// from different fields — this one and Name — which agree for five of the eight
// targets and not for the three with a ShortName. The disagreement is invisible
// until a run tries to delete something, and then it refuses a deletion from a
// category it did read.
func (t Target) Category() string {
	if t.ShortName != "" {
		return t.ShortName
	}
	return t.Name
}

// Targets lists every 評価額合計 contributing to the account total.
//
// A missing entry understates the balance silently rather than failing, which
// is why the reader reports a per-target breakdown: the only way to notice a
// gap is to see the parts.
var Targets = []Target{
	{Kind: asset.DomesticStock, Key: "japan", Name: "日本株", URL: "https://www.paypay-sec.co.jp/trade?country=japan", Bucket: BucketApp},
	{Kind: asset.DomesticStock, Key: "japan-etf", Name: "日本株ETF", URL: "https://www.paypay-sec.co.jp/trade?country=japan-etf", Bucket: BucketApp},
	{Kind: asset.USStock, Key: "usa", Name: "米国株", URL: "https://www.paypay-sec.co.jp/trade?country=usa", Bucket: BucketApp},
	{Kind: asset.USStock, Key: "usa-etf", Name: "米国株ETF", URL: "https://www.paypay-sec.co.jp/trade?country=usa-etf", Bucket: BucketApp},
	{Kind: asset.MutualFund, Key: "robo", Name: "ロボ貯蓄", URL: "https://www.paypay-sec.co.jp/trade?reserve_mode=1", Bucket: BucketApp},
	{Kind: asset.USStock, Key: "miniapp", Name: "ミニアプリ", ShortName: "ミニ", URL: URLMiniApp, Bucket: BucketMiniApp},

	// Same URL, different bucket — so the names have to distinguish them, or the
	// two would collide into one asset.
	{Kind: asset.MutualFund, Key: "toushin-app", Name: "投資信託（アプリ）", ShortName: "投信ア", URL: URLInvestmentTrust, Bucket: BucketApp, ViaAPI: true},
	{Kind: asset.MutualFund, Key: "toushin-miniapp", Name: "投資信託（ミニアプリ）", ShortName: "投信ミ", URL: URLInvestmentTrust, Bucket: BucketMiniApp, ViaAPI: true},
}

// The OTP challenge. CONFIRMED 2026-08-01 from the live page markup.
//
// This page is not the usual "one field plus a button", and assuming it was is
// why every earlier guess missed:
//
//   - The code is six separate single-digit inputs, #code1 … #code6. Only
//     #code1 starts editable; the page's own keypress handler unlocks and
//     focuses each following field, and paste is blocked outright. The only way
//     in is to type, one key at a time, exactly as a person would.
//   - A two-letter prefix is displayed on the page (#otp-prefix, e.g. "QO-") and
//     repeated in the email. Confirming they match is an anti-phishing step, so
//     the prefix is read and surfaced rather than ignored — see [LoginResult].
//   - There are two "認証する" anchors. The visible one carries no handler; the
//     real one is #btn_sms_success, hidden until the six digits are in. Clicking
//     by visibility alone would press a button that does nothing.
const (
	// OTPFirstDigit is also the challenge-page marker: if it is present,
	// the OTP page is up.
	OTPFirstDigit = "#code1"

	// OTPPrefix holds the two-letter prefix shown on the page.
	OTPPrefix = "#otp-prefix"

	// OTPSubmit is the anchor that actually submits (go_emailauth).
	OTPSubmit = "#btn_sms_success"

	// OTPDigits is how many single-digit inputs the challenge has.
	OTPDigits = 6
)

// One 銘柄's own page, reached from its row's href. CONFIRMED 2026-08-01.
//
// Worth the extra page load: the holdings list rounds the profit on the 株
// template, so the acquisition cost cannot be derived from it accurately, and an
// approximate cost basis shows up in MoneyForward as a wrong 評価損益 —
// a plausible number, which is the worst kind.
const (
	HoldingValue       = "#SECURITIES_VALUE"
	HoldingAcquisition = "#ACQUISITION_AMOUNT_YEN"
	HoldingGain        = "#SUM_GROSS_PROFIT"
)

// The account summary. CONFIRMED 2026-08-01 from the live 投資信託 page:
//
//	<span id="SECURITIES_VALUE_TOTAL">34<span>万</span>5678<span>円</span></span>
//	<span id="TOTAL_ACQUISITION_FEE_TAX_TOTAL">30<span>万</span>0000<span>円</span></span>
//	<span id="gross_profit_total">+4<span>万</span>5678<span>円</span></span>
//
// Note what these mean: SECURITIES_VALUE_TOTAL is 評価額合計, the market value we
// want. TOTAL_ACQUISITION_FEE_TAX_TOTAL is 投資元本 — the cost basis, which is a
// different and smaller number, and recording it by mistake would understate the
// asset in a way that looks entirely plausible.
//
// 投資元本 + 含み益 = 評価額合計 by definition, which makes those two an
// arithmetic check rather than a second guess. See [Reading.Amount].
// LoadingOverlay is the spinner the 投資信託 Vue app shows while it
// fetches. It exists on the page as display:none and becomes visible during a
// load, so its visibility — not its presence — is the signal.
const LoadingOverlay = ".loading_page"

const (
	ValueTotal  = "#SECURITIES_VALUE_TOTAL"
	Acquisition = "#TOTAL_ACQUISITION_FEE_TAX_TOTAL"
	GrossProfit = "#gross_profit_total"
)

// The 保有銘柄 list. CONFIRMED 2026-08-01 on both templates the site uses:
//
//	株:       <div class="mypage_brand_icon mypage_brand35 up">
//	            <a href="/trade/brand/35/0">…<div class="brand_invest">45万6789円</div>
//	                                          <div class="brand_gain">+3.7万</div></a>
//	            <div class="brand_text">テスト電機</div></div>
//
//	投資信託: <div class="mypage_brand_icon trust_brand_icon up">
//	            <a title="テスト・グローバル・ファンドインデックス">…<div class="brand_invest"> 345,678円 </div>
//	              <p class="brand_text"> テスト・グローバル・ファンドインデックス </p></a></div>
//
// They differ in where the name sits and whether there is an href, but both
// wrap each 銘柄 in .mypage_brand_icon and both label it .brand_text — so rows
// are read whole rather than by pairing flat cell lists positionally.
//
// Note .brand_gain is abbreviated on the 株 template ("+3.7万"). It is captured
// as text and deliberately never parsed: it is not the figure being recorded,
// and a lossy rounding has no business anywhere near one that is.
// HoldingsHeading is the section heading the holdings sit under.
//
// Scoping to it is essential, not cosmetic: .mypage_brand_icon is also how the
// site marks up its tradeable-brand catalogue, so an unscoped query returns
// every brand PayPay offers — 305 of them on the 日本株 page — rather than the
// handful actually held.
const HoldingsHeading = "保有銘柄"

const (
	HoldingsHeadingTag = "h1, h2, h3, h4"
	HoldingsContainer  = ".icon_lv1"

	HoldingRow  = ".mypage_brand_icon"
	HoldingName = ".brand_text"
	BrandInvest = ".brand_invest"
	BrandGain   = ".brand_gain"
)
