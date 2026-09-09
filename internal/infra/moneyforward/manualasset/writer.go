package manualasset

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Endpoints the account page's own forms submit to.
const (
	createPath = "/bs/portfolio/new"

	// editPath takes an update, tunnelled as a POST with _method=put.
	editPath = "/bs/portfolio/edit"

	// entryPath is the base a delete addresses, by row hash.
	entryPath = "/bs/portfolio/"
)

// Field names on the form.
const (
	fieldToken        = "authenticity_token"
	fieldID           = "user_asset_det[id]"
	fieldSubAccount   = "user_asset_det[sub_account_id_hash]"
	fieldSubclass     = "user_asset_det[asset_subclass_id]"
	fieldName         = "user_asset_det[name]"
	fieldValue        = "user_asset_det[value]"
	fieldEntriedAt    = "user_asset_det[entried_at]"
	fieldEntriedPrice = "user_asset_det[entried_price]"

	// fieldCommit is the submit button's own value. Rails forms routinely
	// branch on it, and omitting it is a silent no-op: the server answers 200
	// with the form re-rendered, which reads as success to anything only
	// checking the status code.
	fieldCommit      = "commit"
	fieldMethod      = "_method"
	commitCreateText = "この内容で登録する"
)

// Writer is permission to write to one account, as of one rendering of its page.
//
// Obtained from [Account.Writer], never constructed directly: the tokens it
// carries are only valid for the page they were read from, and pairing them
// with a different account is a forged request as far as Rails is concerned.
type Writer struct {
	// Account is what this writer may write to.
	Account Account

	// Token is the create form's own CSRF token.
	Token string

	// MetaToken is the page-level CSRF token from the csrf-token meta tag.
	//
	// Deletes need this one rather than a form's. They are rendered as Rails
	// method-links, which carry no form of their own and are validated against
	// the page token — so a form token here is rejected exactly like a forged
	// request.
	MetaToken string

	// SubAssetID is the sub-account new entries join. MoneyForward's own
	// field name, scraped from the create form.
	//
	// Not the same value as [Account.AssetID], despite both being 43-character
	// hashes on the same page. This one is never supplied by a user.
	SubAssetID      string
	SubAccountLabel string
}

// Response is what the server said to a write.
//
// Kept rather than reduced to an error, because this site answers a rejection
// with 200 and the page re-rendered: the status code says nothing, and the
// reason is only in the body.
type Response struct {
	StatusCode int
	FinalURL   string
	Body       []byte
}

// RejectionReason returns what the page is saying, or "" if it says nothing.
//
// Only ever used to explain a failure that reading the account back already
// established, never to decide whether one happened. The page carries error
// blocks that have nothing to do with the request — an unrelated transfer form
// contributes one on every render — and success is announced through the same
// kind of block, so this signal is far too noisy to judge by. It is still worth
// reading once something is known to have gone wrong: it is where
// "名称は20文字以内でお願いします" comes from.
//
// Every block, not the first. Which one is relevant cannot be told apart here,
// and taking the first made the answer depend on where the site happens to
// render each — so the useful message was quoted only if it came before the
// permanent one. This is for a person to read; give them all of it.
func (r Response) RejectionReason() string {
	var reasons []string
	seen := map[string]bool{}
	for _, m := range errorPattern.FindAllStringSubmatch(string(r.Body), -1) {
		text := strings.TrimSpace(tagPattern.ReplaceAllString(m[1], ""))
		text = strings.Join(strings.Fields(text), " ")
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		reasons = append(reasons, text)
	}
	return strings.Join(reasons, " / ")
}

// Create adds one entry to the portfolio.
//
// entried_at is left empty: the purchase date cannot be known without walking
// the transaction history, and it does not affect any figure shown.
// entried_price is the acquisition cost, which does — see [Entry].
func (w Writer) Create(ctx context.Context, e Entry) (Response, error) {
	return w.post(ctx, w.Token, origin+createPath, url.Values{
		fieldToken:        {w.Token},
		fieldID:           {e.ID},
		fieldSubAccount:   {w.SubAssetID},
		fieldSubclass:     {strconv.Itoa(int(e.Subclass))},
		fieldName:         {e.Name},
		fieldValue:        {strconv.FormatInt(e.Yen, 10)},
		fieldEntriedAt:    {""},
		fieldEntriedPrice: {e.entriedPrice()},
		fieldCommit:       {commitCreateText},
	}, "create "+e.Name)
}

// Update changes an existing row, addressing it by numeric ID.
func (w Writer) Update(ctx context.Context, e Entry) (Response, error) {
	if e.ID == "" {
		return Response{}, fmt.Errorf("update %q: no numeric id", e.Name)
	}
	// The row's own edit-form token where there is one; the create form's is
	// only a fallback for a row assembled by hand.
	token := e.Token
	if token == "" {
		token = w.Token
	}
	return w.post(ctx, token, origin+editPath, url.Values{
		fieldToken:        {token},
		fieldMethod:       {"put"},
		fieldID:           {e.ID},
		fieldSubAccount:   {w.SubAssetID},
		fieldSubclass:     {strconv.Itoa(int(e.Subclass))},
		fieldName:         {e.Name},
		fieldValue:        {strconv.FormatInt(e.Yen, 10)},
		fieldEntriedAt:    {""},
		fieldEntriedPrice: {e.entriedPrice()},
		fieldCommit:       {commitCreateText},
	}, "update "+e.Name)
}

// Delete removes a row, addressing it by hash.
func (w Writer) Delete(ctx context.Context, e Entry) (Response, error) {
	if e.Hash == "" {
		return Response{}, fmt.Errorf("delete %q: no row hash", e.Name)
	}
	token := w.MetaToken
	if token == "" {
		token = w.Token
	}
	target := origin + entryPath + e.Hash + "?sub_account_id_hash=" + url.QueryEscape(w.SubAssetID)
	return w.post(ctx, token, target, url.Values{
		fieldToken:  {token},
		fieldMethod: {"delete"},
	}, "delete "+e.Name)
}

// post submits a form and reports transport-level failures only.
//
// Whether the write was *accepted* is not decided here: this endpoint answers a
// rejection with 200 and the page re-rendered, so the only reliable verdict is
// reading the account back afterwards. The one exception is a bounce to the
// sign-in host, which is unambiguous and worth naming rather than letting it
// surface later as "the entry is not there".
func (w Writer) post(ctx context.Context, token, endpoint string, values url.Values, what string) (Response, error) {
	var out Response

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", w.Account.URL())
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := w.Account.HTTP.Do(req)
	if err != nil {
		return out, fmt.Errorf("%s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()

	out.StatusCode = resp.StatusCode
	if resp.Request != nil && resp.Request.URL != nil {
		out.FinalURL = resp.Request.URL.String()
	}
	out.Body, _ = io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("%s returned %d", what, resp.StatusCode)
	}
	if resp.Request != nil && strings.Contains(resp.Request.URL.Host, "id.moneyforward.com") {
		return out, fmt.Errorf("%s was bounced to sign-in; the CSRF token or session was rejected", what)
	}
	return out, nil
}
