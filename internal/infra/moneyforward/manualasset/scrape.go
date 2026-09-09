package manualasset

import (
	"errors"
	"fmt"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The account page is read with regexps rather than parsed as a document. The
// markup that matters here is a handful of hidden inputs inside forms whose ids
// are stable, and each pattern below encodes something learned by getting it
// wrong against the live page — which a general-purpose selector would not
// carry with it.
var (
	// createFormPattern isolates the create form before anything is read out of
	// it. The page carries six forms, and the first authenticity_token belongs
	// to none of them in particular — using it gets the POST treated as a forged
	// request, which Rails answers by nullifying the session and redirecting to
	// sign-in. That looks exactly like an expired login.
	createFormPattern = regexp.MustCompile(`(?s)<form[^>]*id="new_user_asset_det"[^>]*>(.*?)</form>`)

	tokenPattern      = regexp.MustCompile(`name="authenticity_token"[^>]*value="([^"]+)"`)
	metaTokenPattern  = regexp.MustCompile(`<meta[^>]*name="csrf-token"[^>]*content="([^"]+)"`)
	subAccountPattern = regexp.MustCompile(
		`name="user_asset_det\[sub_account_id_hash\]"[\s\S]*?<option[^>]*value="([^"]+)"[^>]*>\s*([^<]*)</option>`)

	// Each existing row carries its own edit form, inside a modal. Those forms
	// are a better source than the rendered table: they hold both identifiers,
	// the exact stored value, and the subclass, with no formatting to undo.
	entryFormPattern = regexp.MustCompile(
		`(?s)<form[^>]*id="new_user_asset_det_([^"]+)"[^>]*>(.*?)</form>`)
	entryFieldPattern = regexp.MustCompile(
		`<input[^>]*value="([^"]*)"[^>]*name="user_asset_det\[([a-z_]+)\]"`)
	entryFieldAltPattern = regexp.MustCompile(
		`<input[^>]*name="user_asset_det\[([a-z_]+)\]"[^>]*value="([^"]*)"`)

	// subclassSelectPattern isolates the 資産クラス select inside the create
	// form, and optionPattern reads the options out of it.
	//
	// Two steps rather than one, because the form carries several selects — a
	// sub-account chooser among them — and a pattern loose enough to find
	// options anywhere would mix their values into one list. The identifiers
	// this yields are what an entry is filed under, so a value from the wrong
	// select would file a holding as something else entirely.
	subclassSelectPattern = regexp.MustCompile(
		`(?s)<select[^>]*name="user_asset_det\[asset_subclass_id\]"[^>]*>(.*?)</select>`)
	optionPattern = regexp.MustCompile(`(?s)<option[^>]*value="(\d+)"[^>]*>(.*?)</option>`)

	// errorPattern finds the message the page shows when it rejects a write.
	//
	// Scoped to error classes specifically: success is announced through the
	// same kind of block ("資産を削除しました"), and treating that as a rejection
	// turns a completed delete into a reported failure.
	errorPattern = regexp.MustCompile(`(?s)<div[^>]*class="[^"]*(?:alert-error|alert-danger|error)[^"]*"[^>]*>(.*?)</div>`)
	tagPattern   = regexp.MustCompile(`<[^>]+>`)
)

// accountPage is the HTML of one manual account page, and the only thing that
// knows how to get anything out of it.
//
// A named type rather than passing strings to a set of parse helpers: at
// package scope those helpers were reachable from the write path, where a
// "parse the account page" function applied to a POST response would find
// nothing and say so unhelpfully.
type accountPage string

// writerFor extracts what a write to account needs.
func (p accountPage) writerFor(account Account) (Writer, error) {
	w := Writer{Account: account}

	if mm := metaTokenPattern.FindStringSubmatch(string(p)); mm != nil {
		w.MetaToken = mm[1]
	}

	createForm := createFormPattern.FindStringSubmatch(string(p))
	form := string(p)
	if createForm != nil {
		form = createForm[1]
		m := tokenPattern.FindStringSubmatch(form)
		if m == nil {
			return w, fmt.Errorf("no authenticity_token in the create form on %s", account.URL())
		}
		w.Token = m[1]
	} else {
		// The current page inserts the modal form in the browser. Rails emits
		// the same masked token in the csrf-token meta tag, so it is valid for
		// the create POST even when the form wrapper is absent from the raw GET.
		if w.MetaToken == "" {
			return w, fmt.Errorf("no create form or page CSRF token on %s — the session is probably not authenticated", account.URL())
		}
		w.Token = w.MetaToken
	}

	if sm := subAccountPattern.FindStringSubmatch(form); sm != nil {
		w.SubAssetID = sm[1]
		w.SubAccountLabel = html.UnescapeString(strings.TrimSpace(sm[2]))
	}
	if w.SubAssetID == "" && createForm == nil {
		w.SubAssetID = strings.TrimSpace(os.Getenv("MONEYFORWARD_PAYPAYSEC_SUBACCOUNT_ID"))
		w.SubAccountLabel = "PayPay証券"
	}
	if w.SubAssetID == "" {
		return w, fmt.Errorf("no sub-account option on %s", account.URL())
	}
	return w, nil
}

// entries reads every row the page records.
func (p accountPage) entries() ([]Entry, error) {
	var entries []Entry
	for _, m := range entryFormPattern.FindAllStringSubmatch(string(p), -1) {
		entry, err := parseEntryForm(m[1], m[2])
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// parseEntryForm reads one row's edit form.
//
// Attribute values come back HTML-escaped, and are unescaped here. A 銘柄 whose
// name contains & — AT&T, S&P500 — is stored as sent and rendered as &amp;, so
// a name read back raw never equals the one that was written. The verification
// step matches by name, so every run reported the write as failed, having
// already applied it, and created the row again on the next run: one duplicate
// of a real position per run, with the balance double-counted and the delete
// steps that would have cleaned up never reached.
func parseEntryForm(hash, body string) (Entry, error) {
	fields := map[string]string{}
	for _, f := range entryFieldPattern.FindAllStringSubmatch(body, -1) {
		fields[f[2]] = html.UnescapeString(f[1])
	}
	// The same inputs with the attributes the other way round. Both orders
	// appear on the page depending on which of them Rails rendered.
	for _, f := range entryFieldAltPattern.FindAllStringSubmatch(body, -1) {
		if _, seen := fields[f[1]]; !seen {
			fields[f[1]] = html.UnescapeString(f[2])
		}
	}

	yen, err := strconv.ParseInt(strings.TrimSpace(fields["value"]), 10, 64)
	if err != nil {
		return Entry{}, fmt.Errorf("row %s: unreadable value %q", hash, fields["value"])
	}
	subclass, _ := strconv.Atoi(fields["asset_subclass_id"])

	token := ""
	if tm := tokenPattern.FindStringSubmatch(body); tm != nil {
		token = tm[1]
	}

	// A blank acquisition means "not recorded", which is not the same as zero —
	// see [Entry].
	acquisition, hasAcquisition := int64(0), false
	if raw := strings.TrimSpace(fields["entried_price"]); raw != "" {
		if v, aerr := strconv.ParseInt(raw, 10, 64); aerr == nil {
			acquisition, hasAcquisition = v, true
		}
	}

	return Entry{
		ID:             fields["id"],
		Hash:           hash,
		Token:          token,
		Name:           fields["name"],
		Yen:            yen,
		AcquisitionYen: acquisition,
		HasAcquisition: hasAcquisition,
		Subclass:       AssetSubclass(subclass),
	}, nil
}

// SubclassOption is one entry in the create form's 資産クラス select.
//
// Read from the page rather than written down from memory. The identifiers in
// [SubclassFor] were established this way, and the only honest way to add
// another is to look again — a guessed one files a holding under the wrong
// 資産クラス with no error anywhere, which is a number in the right place and
// the wrong category.
type SubclassOption struct {
	ID    AssetSubclass
	Label string
}

// subclasses reads the 資産クラス options the create form offers.
func (p accountPage) subclasses() ([]SubclassOption, error) {
	createForm := createFormPattern.FindStringSubmatch(string(p))
	if createForm == nil {
		return nil, errors.New("the create form is not on the page; the session is " +
			"probably not authenticated")
	}
	sel := subclassSelectPattern.FindStringSubmatch(createForm[1])
	if sel == nil {
		return nil, errors.New("the create form has no " + fieldSubclass + " select")
	}

	var out []SubclassOption
	for _, m := range optionPattern.FindAllStringSubmatch(sel[1], -1) {
		id, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("option value %q is not a number", m[1])
		}
		label := strings.TrimSpace(html.UnescapeString(tagPattern.ReplaceAllString(m[2], "")))
		out = append(out, SubclassOption{ID: AssetSubclass(id), Label: label})
	}
	if len(out) == 0 {
		return nil, errors.New("the 資産クラス select is present but offers no options")
	}
	return out, nil
}
