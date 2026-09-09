package assetname

import (
	"strings"
	"testing"
)

func TestSchemeFor(t *testing.T) {
	tests := []struct {
		name     string
		category string
		holding  string
		want     string
	}{
		{"short enough to keep whole", "米国株", "テスト電機", "[米国株] テスト電機"},
		{"latin holding", "米国株", "テスト商事", "[米国株] テスト商事"},
		{"exactly at the limit is untouched", "投信ミ", "テストファンドAB", "[投信ミ] テストファンドAB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Scheme{Category: tt.category}.For(tt.holding)
			if got != tt.want {
				t.Errorf("For() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSchemeForNeverExceedsTheLimit is the guard on a silent failure: a longer
// name is rejected with a 200 and a re-rendered page, so the holding simply
// never appears.
func TestSchemeForNeverExceedsTheLimit(t *testing.T) {
	categories := []string{"米国株", "投信ミ", "ミニ", "日本株ETF"}
	holdings := []string{
		"テスト電機",
		"テスト・グローバル・ファンドインデックス",
		"とても長い名前の投資信託ファンドです（為替ヘッジあり）",
		strings.Repeat("あ", 200),
	}
	for _, c := range categories {
		for _, h := range holdings {
			got := Scheme{Category: c}.For(h)
			if n := len([]rune(got)); n > Limit {
				t.Errorf("For(%q) under %q = %q, %d runes", h, c, got, n)
			}
		}
	}
}

// TestSchemeForKeepsTheCategoryWhole checks that shortening eats the holding.
// The category is what keeps the same 銘柄 under two categories distinct.
func TestSchemeForKeepsTheCategoryWhole(t *testing.T) {
	const long = "とても長い名前の投資信託ファンドです"
	for _, c := range []string{"米国株", "投信ミ"} {
		got := Scheme{Category: c}.For(long)
		if !strings.HasPrefix(got, "["+c+"] ") {
			t.Errorf("For() = %q, want it to start with [%s] ", got, c)
		}
	}
}

func TestSchemeForMarksTruncation(t *testing.T) {
	got := Scheme{Category: "投信ミ"}.For("テスト・グローバル・ファンドインデックス")
	if !strings.Contains(got, Ellipsis) {
		t.Errorf("For() = %q, want a shortened name to say so", got)
	}
}

func TestSchemeForDistinguishesLongCommonPrefixes(t *testing.T) {
	s := Scheme{Category: "米国株ETF"}
	a := s.For("Direxion デイリー 半導体株 ブル 3倍ETF")
	b := s.For("Direxion デイリー テスラ株ブル2倍ETF")
	if a == b {
		t.Fatalf("two long holdings map to %q", a)
	}
	if len([]rune(a)) > Limit || len([]rune(b)) > Limit {
		t.Fatalf("names exceed limit: %q / %q", a, b)
	}
}

// TestDistinctHoldingsStayDistinct guards the case truncation could break: two
// funds differing only past the cut.
func TestDistinctHoldingsStayDistinct(t *testing.T) {
	s := Scheme{Category: "投信ミ"}
	a, b := s.For("テストAIファンド"), s.For("グローバル債券ファンド")
	if a == b {
		t.Errorf("two different holdings both map to %q", a)
	}
}

func TestSetRejectsDuplicates(t *testing.T) {
	var set Set
	if err := set.Add("[米国株] テスト電機", "テスト電機"); err != nil {
		t.Fatalf("first Add() = %v", err)
	}
	err := set.Add("[米国株] テスト電機", "また テスト電機")
	if err == nil {
		t.Fatal("Add() accepted a duplicate name")
	}
	if !strings.Contains(err.Error(), "[米国株] テスト電機") {
		t.Errorf("error %v does not name the collision", err)
	}
	if set.Len() != 1 {
		t.Errorf("Len() = %d, want the duplicate not to have been recorded", set.Len())
	}
}

func TestSetAcceptsTheSameHoldingUnderDifferentCategories(t *testing.T) {
	// テスト電機 really is held under two categories on the live account.
	var set Set
	for _, c := range []string{"米国株", "ミニ"} {
		if err := set.Add(Scheme{Category: c}.For("テスト電機"), "テスト電機"); err != nil {
			t.Fatalf("Add() under %q = %v", c, err)
		}
	}
	if set.Len() != 2 {
		t.Errorf("Len() = %d, want 2 distinct names", set.Len())
	}
}

func TestSchemeValidate(t *testing.T) {
	if err := (Scheme{Category: "米国株"}).Validate(); err != nil {
		t.Errorf("Validate() on a usable category = %v", err)
	}
	if err := (Scheme{}).Validate(); err == nil {
		t.Error("Validate() accepted an empty category")
	}
	// A category so long the holding could not survive it.
	if err := (Scheme{Category: strings.Repeat("あ", 19)}).Validate(); err == nil {
		t.Error("Validate() accepted a category leaving no room for the holding")
	}
}
