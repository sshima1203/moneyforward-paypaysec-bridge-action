// Package assetname builds the names holdings are recorded under.
//
// Pure, and separate from any site, because the constraints come from two
// directions at once and neither is negotiable. MoneyForward caps a name at
// twenty characters and rejects a longer one silently — with a 200 and a
// re-rendered page, so an over-long name is not an error to handle but a holding
// that never appears. And the names have to stay distinct, because they are the
// only key linking a broker's holdings to MoneyForward's rows.
package assetname

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// Limit is the longest name MoneyForward accepts, in characters.
// CONFIRMED 2026-08-01 by exceeding it: 名称は20文字以内でお願いします.
const Limit = 20

// Ellipsis marks a name that had to be shortened.
const Ellipsis = "…"

// Scheme renders names of the form "[category] holding".
//
// The category is a prefix rather than a suffix so that it survives truncation:
// it is what keeps テスト電機 held under 米国株 distinct from テスト電機 held
// under ミニアプリ, and losing it would merge the two.
type Scheme struct {
	// Category labels where the holding is held. Kept whole.
	Category string
}

// For renders the name for one holding, trimmed to fit [Limit].
func (s Scheme) For(holding string) string {
	prefix := "[" + s.Category + "] "
	room := Limit - len([]rune(prefix))
	return prefix + truncateRunes(holding, room)
}

// truncateRunes shortens s to at most n runes, marking that it was cut.
//
// Counted in runes rather than bytes: the limit is a character count and these
// names are mostly Japanese, where the two differ by a factor of three.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return Ellipsis
	}
	// Keep a stable fingerprint of the full holding name. Names that share a
	// long prefix (common for leveraged ETFs and fund share classes) must not
	// collapse onto the same MoneyForward row after truncation.
	if n > 5 {
		h := fnv.New32a()
		_, _ = h.Write([]byte(s))
		suffix := fmt.Sprintf("%s%04x", Ellipsis, h.Sum32()&0xffff)
		return string(r[:n-len([]rune(suffix))]) + suffix
	}
	return string(r[:n-1]) + Ellipsis
}

// Set collects names and refuses a duplicate.
//
// Names come from the broker at runtime, so no static check can rule a
// collision out. Two holdings mapping to one name would each write to the same
// row and the recorded figure would be whichever ran last — indistinguishable
// from a correct balance, which is why this fails instead of resolving it.
type Set struct {
	seen map[string]string
}

// Add records a name, reporting the holding that already claimed it.
func (s *Set) Add(name, holding string) error {
	if s.seen == nil {
		s.seen = map[string]string{}
	}
	if prev, dup := s.seen[name]; dup {
		return fmt.Errorf("two holdings map to the asset %q: %s, and %s", name, prev, holding)
	}
	s.seen[name] = holding
	return nil
}

// Len is how many distinct names have been added.
func (s *Set) Len() int { return len(s.seen) }

// Validate reports why a category is unusable, or nil.
func (s Scheme) Validate() error {
	if strings.TrimSpace(s.Category) == "" {
		return fmt.Errorf("assetname: empty category; names would carry an empty prefix")
	}
	if room := Limit - len([]rune("["+s.Category+"] ")); room < 2 {
		return fmt.Errorf("assetname: category %q leaves %d characters for the holding",
			s.Category, room)
	}
	return nil
}

// CategoryOf reads back the category [Scheme.For] wrote, and says whether the
// name has one at all.
//
// The inverse exists because a ledger entry is only a string by the time it
// comes back: the service stores the rendered name and nothing else. Deciding
// whether an entry is stale means knowing which category it belongs to, and the
// prefix is the only place that survives the round trip.
//
// Deliberately strict about the shape. A name a person typed into the account
// by hand has no prefix, and reporting one for it would file a stranger's row
// under a category this program manages — and then delete it as stale.
func CategoryOf(name string) (string, bool) {
	if !strings.HasPrefix(name, "[") {
		return "", false
	}
	end := strings.Index(name, "] ")
	if end < 2 {
		return "", false
	}
	return name[1:end], true
}
