package selector

import (
	"strings"
	"testing"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/assetname"
)

// TestTargetKeysAreUnique guards the identifier used in logs and error messages.
func TestTargetKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, target := range Targets {
		if seen[target.Key] {
			t.Errorf("duplicate target key %q", target.Key)
		}
		seen[target.Key] = true
	}
}

// TestTargetNamesAreUnique guards the asset-name prefix.
//
// The prefix is the only thing separating one category's 銘柄 from another's, so
// two targets sharing a name would collapse into one namespace — and a fund held
// on both 投資信託 tabs would then map to a single asset.
func TestTargetNamesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, target := range Targets {
		if prev, dup := seen[target.Name]; dup {
			t.Errorf("targets %q and %q share the name %q", prev, target.Key, target.Name)
			continue
		}
		seen[target.Name] = target.Key
	}
}

// TestTargetsAreFullyDescribed catches an entry added without the fields the
// write side needs. A target missing its Name still scrapes fine and then
// produces assets called "[] テスト電機".
func TestTargetsAreFullyDescribed(t *testing.T) {
	for _, target := range Targets {
		t.Run(target.Key, func(t *testing.T) {
			if target.Key == "" {
				t.Error("Key is empty")
			}
			if strings.TrimSpace(target.Name) == "" {
				t.Error("Name is empty; assets would carry an empty prefix")
			}
			if target.URL == "" {
				t.Error("URL is empty")
			}
			switch target.Bucket {
			case BucketApp, BucketMiniApp:
			default:
				t.Errorf("Bucket is %q, which is neither bucket", target.Bucket)
			}
			prefix, ok := expectedPrefix[target.Key]
			if !ok {
				t.Fatalf("no expected prefix recorded for %q; add one to expectedPrefix", target.Key)
			}
			// Spelled out rather than computed with the same helper the code
			// uses. Deriving the expectation from prefix() means a wrong
			// prefix() agrees with itself and the test says nothing — and the
			// prefix is what keeps one 銘柄 held under 米国株 distinct from the
			// same 銘柄 held under ミニアプリ, which is a real case and would
			// otherwise reconcile as one row overwriting the other.
			if got, want := target.AssetName("銘柄"), "["+prefix+"] 銘柄"; got != want {
				t.Errorf("AssetName() = %q, want %q", got, want)
			}
		})
	}
}

// TestSharedURLTargetsAreReadOverTheAPI pins the invariant behind the 投資信託
// pair: two targets on one URL are two views of that page, and the page offers
// no way to tell which view its numbers belong to.
//
// The page swaps between them with a tab. The click is instant and the figures
// arrive about a second later, so for that second the document is in the new
// state showing the old bucket's numbers. Reading it there once reported a
// category holding two 銘柄 as empty and deleted both — twice, under two
// different waiting strategies. Anything sharing a URL has to come from
// [investapi], where a bucket is a request rather than a view.
func TestSharedURLTargetsAreReadOverTheAPI(t *testing.T) {
	byURL := map[string][]Target{}
	for _, target := range Targets {
		byURL[target.URL] = append(byURL[target.URL], target)
	}
	for url, shared := range byURL {
		if len(shared) < 2 {
			continue
		}
		buckets := map[Bucket]string{}
		for _, target := range shared {
			if !target.ViaAPI {
				t.Errorf("%s is one of %d targets on %s but is read from the page, "+
					"which cannot say which of them its figures belong to",
					target.Key, len(shared), url)
			}
			if other, dup := buckets[target.Bucket]; dup {
				t.Errorf("%s and %s share %s and the same bucket, so nothing "+
					"distinguishes their holdings", target.Key, other, url)
			}
			buckets[target.Bucket] = target.Key
		}
	}
}

// TestEveryTargetProducesUsableNames ties the target list to the naming rules.
//
// The rules themselves live in domain/assetname and are tested there; this
// checks that every category configured here can actually satisfy them — a
// category long enough to crowd out the 銘柄 would produce names that are
// unique in principle and useless in practice.
func TestEveryTargetProducesUsableNames(t *testing.T) {
	for _, target := range Targets {
		t.Run(target.Key, func(t *testing.T) {
			if err := target.scheme().Validate(); err != nil {
				t.Errorf("scheme() = %v", err)
			}
			name := target.AssetName("テスト・グローバル・ファンドインデックス・ファンド")
			if n := len([]rune(name)); n > assetname.Limit {
				t.Errorf("AssetName() = %q, %d runes over the %d limit", name, n, assetname.Limit)
			}
		})
	}
}

// expectedPrefix is what each target labels its holdings with, written out.
//
// A target added without an entry here fails the check above rather than being
// waved through, because an unlabelled category is how two holdings end up
// under one asset name.
var expectedPrefix = map[string]string{
	"japan":           "日本株",
	"japan-etf":       "日本株ETF",
	"usa":             "米国株",
	"usa-etf":         "米国株ETF",
	"robo":            "ロボ貯蓄",
	"miniapp":         "ミニ",
	"toushin-app":     "投信ア",
	"toushin-miniapp": "投信ミ",
}
