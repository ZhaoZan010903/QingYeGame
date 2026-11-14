package catalog

import (
	"fmt"
	"strings"
	"testing"
)

func TestR212PeriodQuoteValidationAndNormalization(t *testing.T) {
	valid := []RentalQuote{
		{Unit: "period", Amount: "80", Label: "包夜"},
		{Unit: "period", Amount: "20", Label: "夜间", StartTime: "22:00", EndTime: "07:00"},
		{Unit: "period", Amount: "8", Label: "夜间", StartTime: "22:00", EndTime: "07:00", BillingUnit: "hour"},
		{Unit: "period", Amount: "140", Label: "早七晚七", StartTime: "07:00", EndTime: "19:00", BillingUnit: "week"},
		{Unit: "hour", Amount: "5"}, {Unit: "day", Amount: "30"}, {Unit: "week", Amount: "180"}, {Unit: "month", Amount: "600"},
	}
	in := ProductInput{TradeType: "rent", PriceUnit: "unknown", Attributes: map[string]any{"kind": "single"}, RentalQuotes: valid}
	NormalizeInput(&in)
	if err := ValidateBasicInput(in, false); err != nil {
		t.Fatal(err)
	}
	if in.PriceUnit != "period" || in.PriceAmount == nil || *in.PriceAmount != "80" || in.RentalQuotes[0].BillingUnit != "period" {
		t.Fatal("first quote or default basis not normalized", in)
	}
	if _, exists := in.Attributes["kind"]; exists {
		t.Fatal("retired publication field retained for a new product")
	}
	in.PreviousAttributes = map[string]any{"kind": []any{"legacy-linked"}}
	PreserveHistoricalKind(&in)
	if fmt.Sprint(in.Attributes["kind"]) != "[legacy-linked]" {
		t.Fatal("old kind not retained")
	}
}

func TestR212InvalidPeriodQuotesAndBoundaries(t *testing.T) {
	valid := RentalQuote{Unit: "period", Amount: "20", Label: "包夜"}
	cases := []RentalQuote{
		{Unit: "period", Amount: "20"},
		{Unit: "period", Amount: "20", Label: "夜\n间"},
		{Unit: "period", Amount: "20", Label: strings.Repeat("叶", 31)},
		{Unit: "period", Amount: "20", Label: string([]byte{0xff})},
		{Unit: "period", Amount: "20", Label: "夜间", StartTime: "22:00"},
		{Unit: "period", Amount: "20", Label: "夜间", StartTime: "24:00", EndTime: "07:00"},
		{Unit: "period", Amount: "20", Label: "夜间", StartTime: "07:00", EndTime: "07:00"},
		{Unit: "period", Amount: "20", Label: "夜间", BillingUnit: "account"},
		{Unit: "period", Amount: "-1", Label: "包夜"},
		{Unit: "day", Amount: "20", Label: "全天"},
		{Unit: "day", Amount: "20", StartTime: "07:00", EndTime: "19:00"},
		{Unit: "day", Amount: "20", BillingUnit: "day"},
	}
	for i, quote := range cases {
		if err := ValidateExtendedInput(ProductInput{TradeType: "rent", RentalQuotes: []RentalQuote{quote}}); err == nil {
			t.Fatalf("accepted invalid quote %d", i)
		}
	}
	if err := ValidateExtendedInput(ProductInput{TradeType: "rent", RentalQuotes: []RentalQuote{valid, valid}}); err == nil {
		t.Fatal("duplicate period accepted")
	}
	if err := ValidateExtendedInput(ProductInput{TradeType: "rent", RentalQuotes: []RentalQuote{{Unit: "day", Amount: "10"}, {Unit: "day", Amount: "20"}}}); err == nil {
		t.Fatal("duplicate ordinary unit accepted")
	}
	quotes := []RentalQuote{{Unit: "hour", Amount: "1"}, {Unit: "day", Amount: "2"}, {Unit: "week", Amount: "3"}, {Unit: "month", Amount: "4"}}
	for i := 0; i < 12; i++ {
		quotes = append(quotes, RentalQuote{Unit: "period", Amount: "20", Label: fmt.Sprintf("包段%d", i)})
	}
	if err := ValidateExtendedInput(ProductInput{TradeType: "rent", RentalQuotes: quotes}); err != nil {
		t.Fatal("sixteen valid quotes rejected", err)
	}
	quotes = append(quotes, valid)
	if err := ValidateExtendedInput(ProductInput{TradeType: "rent", RentalQuotes: quotes}); err == nil {
		t.Fatal("seventeen quotes accepted")
	}
	if err := ValidateExtendedInput(ProductInput{TradeType: "rent", RentalQuotes: quotes[4:]}); err == nil {
		t.Fatal("thirteen periods accepted")
	}
	if err := ValidateExtendedInput(ProductInput{TradeType: "rent", PriceUnit: "period"}); err == nil {
		t.Fatal("bare period price accepted without quote details")
	}
	if err := ValidateExtendedInput(ProductInput{TradeType: "sale", RentalQuotes: []RentalQuote{valid}}); err == nil {
		t.Fatal("sale accepted rental quotes")
	}
}

func TestR212RentalSearchEnums(t *testing.T) {
	for _, in := range []SearchInput{
		{Limit: 20, PriceUnit: "period", PeriodBasis: "hour", RentalCategory: "hour_day"},
		{Limit: 20, PriceUnit: "period", PeriodBasis: "", RentalCategory: "week_month"},
	} {
		if err := in.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []SearchInput{
		{Limit: 20, PriceUnit: "period", PeriodBasis: "account"},
		{Limit: 20, PriceUnit: "day", PeriodBasis: "period"},
		{Limit: 20, RentalCategory: "guess"},
		{Limit: 20, TradeType: "sale", RentalCategory: "hour_day"},
		{Limit: 20, PriceUnit: "invalid"},
	} {
		if err := in.Validate(); err == nil {
			t.Fatal("invalid rental search accepted", in)
		}
	}
}
