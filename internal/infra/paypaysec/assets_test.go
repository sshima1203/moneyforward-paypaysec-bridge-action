package paypaysec

import (
	"testing"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/application/domain/asset"

	"github.com/mpyw/moneyforward-paypaysec-bridge-action/v3/internal/infra/paypaysec/selector"
)

func TestBalancesAssets(t *testing.T) {
	balances := Balances{Readings: []Reading{
		{
			Target: selector.Target{Key: "usa", Name: "米国株", Kind: asset.USStock},
			Holdings: []Holding{
				{Name: "テスト電機", Yen: 456789, HasYen: true, Ref: "/trade/brand/35/0"},
				{Name: "アップル", Yen: 234567, HasYen: true},
			},
		},
		{
			Target: selector.Target{Key: "toushin-miniapp", Name: "投資信託（ミニアプリ）", ShortName: "投信ミ", Kind: asset.MutualFund},
			Holdings: []Holding{
				{Name: "テスト・グローバル・ファンドインデックス", Yen: 345678, HasYen: true},
			},
		},
	}}

	assets, err := balances.Assets()
	if err != nil {
		t.Fatalf("Assets() error = %v", err)
	}
	// The fund name overruns MoneyForward's 20-character limit, so it arrives
	// truncated. That is the behaviour under test, not an accident of the
	// fixture: an untruncated name is rejected, and rejected silently.
	want := map[string]int64{
		"[米国株] テスト電機":          456789,
		"[米国株] アップル":           234567,
		"[投信ミ] テスト・グローバル…95cb": 345678,
	}
	if len(assets) != len(want) {
		t.Fatalf("Assets() returned %d assets, want %d", len(assets), len(want))
	}
	for _, a := range assets {
		yen, ok := want[a.Name]
		if !ok {
			t.Errorf("unexpected asset %q", a.Name)
			continue
		}
		if a.Yen != yen {
			t.Errorf("%s = %d, want %d", a.Name, a.Yen, yen)
		}
		// Which page it came from, for whoever checks a number by hand.
		if a.Source == "" {
			t.Errorf("%s lost its provenance", a.Name)
		}
		if !a.Kind.Valid() {
			t.Errorf("%s has no instrument kind, so the ledger cannot file it", a.Name)
		}
	}
}

// TestBalancesAssetsSkipsPlaceholders keeps a holding with no figure out of the
// records. Writing a zero would assert something the page never said.
func TestBalancesAssetsSkipsPlaceholders(t *testing.T) {
	balances := Balances{Readings: []Reading{{
		Target: selector.Target{Key: "usa", Name: "米国株", Kind: asset.USStock},
		Holdings: []Holding{
			{Name: "テスト電機", Yen: 456789, HasYen: true},
			{Name: "空っぽ", InvestText: "—"},
		},
	}}}

	assets, err := balances.Assets()
	if err != nil {
		t.Fatalf("Assets() error = %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("Assets() returned %d assets, want only the one with a figure", len(assets))
	}
	if assets[0].Name != "[米国株] テスト電機" {
		t.Errorf("kept the wrong asset: %q", assets[0].Name)
	}
}

func TestBalancesAssetsSkipsRoboSavingsMirror(t *testing.T) {
	balances := Balances{Readings: []Reading{{
		Target: selector.Target{Key: "robo", Name: "ロボ貯蓄", Kind: asset.MutualFund},
		Holdings: []Holding{{Name: "ロボ貯蓄", Yen: 66879, HasYen: true}},
	}}}

	assets, err := balances.Assets()
	if err != nil {
		t.Fatalf("Assets() error = %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("Assets() returned %d assets, want the mirrored Robo Savings total excluded", len(assets))
	}
	if got := balances.Categories(); len(got) != 1 || got[0] != "ロボ貯蓄" {
		t.Fatalf("Categories() = %#v, want Robo Savings coverage for cleanup", got)
	}
}

// TestAssetsRefusesTwoHoldingsUnderOneName covers the guard balance.go calls
// the one against a failure that "looks exactly like a correct balance".
//
// Two 銘柄 mapping to one MoneyForward entry leave whichever was written last,
// and the account then shows a real name with a real-looking figure that is
// simply the wrong holding's. Names come from the site at runtime — a 20-rune
// cap plus a category prefix is not much room — so nothing static can rule this
// out, and the assetname.Set tests prove only that the set works, not that
// Assets consults it.
func TestAssetsKeepsLongCommonPrefixHoldingsDistinct(t *testing.T) {
	// Two funds under the same category, differing only past the truncation
	// point, so both render as the same 20-rune asset name.
	const a = "テスト・グローバル・インデックスAコース"
	const b = "テスト・グローバル・インデックスBコース"

	target := selector.Target{
		Key: "toushin-miniapp", Name: "投資信託（ミニアプリ）", ShortName: "投信ミ",
		Bucket: selector.BucketMiniApp, Kind: asset.MutualFund,
	}
	if target.AssetName(a) == target.AssetName(b) {
		t.Fatalf("holdings still collide; got %q and %q",
			target.AssetName(a), target.AssetName(b))
	}

	balances := Balances{Readings: []Reading{{
		Target: target,
		Holdings: []Holding{
			{Name: a, Yen: 100000, HasYen: true},
			{Name: b, Yen: 250000, HasYen: true},
		},
	}}}

	assets, err := balances.Assets()
	if err != nil {
		t.Fatalf("Assets() error = %v", err)
	}
	if len(assets) != 2 || assets[0].Name == assets[1].Name {
		t.Fatalf("Assets() = %#v, want two distinct records", assets)
	}
}

// TestAssetsRefusesAnUnfilableHolding covers the check that a target says what
// its holdings are.
//
// A Kind that never got set would reach MoneyForward as the zero value and be
// filed under whatever that maps to — a real holding recorded under the wrong
// 資産クラス, with no error anywhere. The subclass translation refuses it too,
// but by then the run has already read every page.
func TestAssetsRefusesAnUnfilableHolding(t *testing.T) {
	balances := Balances{Readings: []Reading{{
		Target:   selector.Target{Key: "usa", Name: "米国株"}, // no Kind
		Holdings: []Holding{{Name: "テスト電機", Yen: 456789, HasYen: true}},
	}}}

	if _, err := balances.Assets(); err == nil {
		t.Fatal("Assets() accepted a holding with no instrument kind")
	}
}
