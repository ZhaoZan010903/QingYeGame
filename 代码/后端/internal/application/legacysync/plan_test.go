package legacysync

import (
	"encoding/json"
	"testing"

	"qingye/backend/internal/domain/legacycatalog"
)

func TestExplicitQuoteKeepsAmountAndUnitTogether(t *testing.T) {
	tests := []struct {
		title, price, unit, trade string
		wantAmount, wantUnit      string
	}{
		{title: "仅有价格数字", price: "90", trade: "rent", wantUnit: "unknown"},
		{title: "日租90，周租450", price: "90", trade: "rent", wantAmount: "90.00", wantUnit: "day"},
		{title: "日租90，周租90", price: "90", trade: "rent", wantUnit: "unknown"},
		{title: "日租980", price: "98", trade: "rent", wantUnit: "unknown"},
		{title: "450元/周", price: "450", trade: "rent", wantAmount: "450.00", wantUnit: "week"},
		{title: "周月租号", price: "240.5", unit: "week", trade: "rent", wantAmount: "240.50", wantUnit: "week"},
		{title: "整号出售", price: "2500", trade: "sale", wantAmount: "2500.00", wantUnit: "account"},
		{title: "整号出售", price: "0", trade: "sale", wantUnit: "unknown"},
	}
	for _, test := range tests {
		amount, unit := explicitQuote(Record{
			LegacyRecord: legacycatalog.LegacyRecord{Title: test.title, Price: json.Number(test.price)},
			PriceUnit:    test.unit,
		}, test.trade)
		if got := amountKey(amount); got != test.wantAmount || unit != test.wantUnit {
			t.Errorf("%s (%s / %s): got %s %s, want %s %s", test.title, test.price, test.unit, got, unit, test.wantAmount, test.wantUnit)
		}
	}
}
