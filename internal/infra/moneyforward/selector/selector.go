// Package selector holds every MoneyForward DOM selector, URL and form field.
//
// Separate from the code that drives the site for the same reason as its PayPay
// counterpart: these are findings confirmed against live pages on a given day,
// and they are what breaks when the front end changes.
package selector

import (
	"regexp"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/otp"
)

// All MoneyForward DOM selectors and URLs live here so a front-end change is a
// one-file fix.
//
// All selectors here are CONFIRMED against the live site: the sign-in form on
// 2026-05-23, everything else by a full sign-in on 2026-08-01.

// OTPMail is everything the shared OTP sources need for this service.
// CONFIRMED 2026-08-02 against real messages in both languages.
//
// The sender is do_not_reply@, not the feedback@ address the service's other
// mail comes from — the guess would have been wrong, as PayPay's was.
// in:anywhere is there because transactional mail can land in Spam, where a code
// that arrived is indistinguishable from one that never came.
//
// No subject filter, and do not add one. The subject is localized by where the
// login came from: a GitHub runner gets "Money Forward ID Additional
// Authentication via Email", a laptop in Tokyo gets the Japanese. A filter on
// either matches every message tested by hand and none of the ones the
// scheduled job receives, which shows up as a poll running its whole window
// with the mail sitting unmatched in every result set.
//
// What is left is the sender, which is not localized, and otpCodePattern, which
// keys on a line holding nothing but the code. The service's other mail from
// this address — the new-device notice, which arrives seconds later and is
// inside the same freshness window — has no such line in either language.
var OTPMail = otp.MailSpec{
	Label:   "MoneyForward",
	Query:   `from:do_not_reply@moneyforward.com in:anywhere`,
	Pattern: otpCodePattern,
	Digits:  6,
}

// otpCodePattern pulls the code out of the mail body. CONFIRMED 2026-08-01
// against a real message, which reads:
//
//	ご自身でのログイン試行である場合は、こちらのコードを入力してログインを継続してください。
//	654321
//
//	この確認コードの有効期限は10分（2026年08月01日 14時31分59秒まで）です。
//
// Matched as a line holding nothing but six digits, rather than by anchoring to
// the sentence above it. The wording is the sort of thing that gets reworded;
// the code sitting alone on its own line is structural. It also excludes the
// timestamps, which are full of digits but never six in a row.
var otpCodePattern = regexp.MustCompile(`(?m)^[  \t]*(\d{6})[  \t]*\r?$`)

const (
	// SignInURL is the app's own entry point, not the identity service's.
	//
	// Both render the same credential form, because the app bounces through the
	// identity service to reach it — but only this one comes back with a session
	// for the app. Signing in directly at id.moneyforward.com/sign_in
	// authenticates the account and leaves you with nothing the app recognises:
	// moneyforward.com then serves its signed-out marketing pages, which is a
	// confusing way to discover you are not where you thought.
	// CONFIRMED 2026-08-01.
	SignInURL = "https://moneyforward.com/sign_in"

	// Login form. CONFIRMED 2026-05-23.
	EmailInput    = `input[name="mfid_user[email]"]`
	PasswordInput = `input[name="mfid_user[password]"]`
	SignInSubmit  = `button#submitto`

	// HomeURL is the app itself, as distinct from both the identity service and
	// the product's public site.
	//
	// Two hops are easy to get wrong here. Authenticating at id.moneyforward.com
	// leaves the browser on the ID account portal, which links nowhere into the
	// app; and moneyforward.com/ is the marketing page, which renders signed-out
	// no matter who you are. /me became the product landing page in September
	// 2026, while /cf remains the authenticated household-account app.
	HomeURL = "https://moneyforward.com/cf"

	// IDPortalMarker identifies the ID account portal — id.moneyforward.com/me,
	// where a direct sign-in lands. CONFIRMED 2026-08-01.
	IDPortalMarker = `a[href="/password/edit"]`

	// HomeAnchor is the post-login marker in the app itself.
	// CONFIRMED — the global-nav 家計簿 link.
	HomeAnchor = `a[href="/cf"]`

	// AccountSelectorButton is shown after authentication when Money Forward ID
	// asks which remembered account should enter the requested service.
	AccountSelectorButton = `form[action="/sign_in/email?select_account=true"] button`

	// Manual asset rollover (= balance update) endpoint, found by inspecting an
	// existing manual asset's show_manual page. CONFIRMED.
	RolloverEndpoint = "https://moneyforward.com/accounts/rollover"

	// CSRF token meta tag, rendered on every authenticated page. CONFIRMED.
	AuthenticityTokenMeta = `meta[name="csrf-token"]`
)

// The email OTP challenge. CONFIRMED 2026-08-01 by a live sign-in.
//
// Kept as single-entry maps rather than collapsed to constants: the login probes
// them alongside the two post-sign-in landings, and one shape for that probe is
// simpler than two. Unlike PayPay's, this challenge is an ordinary single field
// and an ordinary submit button.
var OTPInputCandidates = map[string]string{
	"email_otp": `input[name="email_otp"]`,
}

// OTPSubmitCandidates is the button confirming the OTP. If none matches, the
// login falls back to pressing Enter, which many one-time-code forms handle.
var OTPSubmitCandidates = map[string]string{
	"submitto": `button#submitto`,
}
