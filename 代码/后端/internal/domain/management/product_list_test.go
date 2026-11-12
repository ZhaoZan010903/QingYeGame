package management

import "testing"

func TestAdminNumberedPagination(t *testing.T) {
	for _, c := range []struct {
		limit, page, cursor string
		offset              int
		valid               bool
	}{
		{"20", "3", "", 40, true}, {"50", "2", "", 50, true}, {"20", "", "MjA", 20, true},
		{"20", "0", "", 0, false}, {"0", "1", "", 0, false}, {"50", "20002", "", 0, false},
		{"20", "2", "MjA", 0, false}, {"20", "1.5", "", 0, false}, {"20", "99999999999999999999999", "", 0, false},
	} {
		_, offset, err := DecodeProductPage(c.limit, c.page, c.cursor)
		if (err == nil) != c.valid || c.valid && offset != c.offset {
			t.Fatalf("%+v: offset=%d err=%v", c, offset, err)
		}
	}
}

func TestAdminProductQueryRejectsUnknownEnums(t *testing.T) {
	for _, q := range []ProductQuery{{Limit: 20, Source: "platform,member"}, {Limit: 20, TradeType: "sale,rent"}, {Limit: 20, Sort: "published;DROP TABLE products"}, {Limit: 20, Status: "trash"}, {Limit: 20, Offset: -1}} {
		if ValidateProductQuery(q) == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
	if ValidateProductQuery(ProductQuery{Limit: 50, Sort: "published", Trash: true, TradeType: "rent"}) != nil {
		t.Fatal("valid query rejected")
	}
}
