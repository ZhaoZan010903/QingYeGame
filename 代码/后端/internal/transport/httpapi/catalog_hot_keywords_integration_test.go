package httpapi

import (
	"context"
	"encoding/json"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/infrastructure/postgres"
	"strings"
	"testing"
)

func TestR211HotKeywordSeedAndPublicFilters(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	var labels []string
	if err := a.DB.QueryRow(ctx, `SELECT array_agg(label ORDER BY sort_order,value) FROM product_dictionary_options WHERE dictionary_key='keyword'`).Scan(&labels); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 23 || labels[0] != "600外搭以上" || labels[2] != "包子" || labels[22] != "白骨" {
		t.Fatalf("unexpected hot keyword seed: %v", labels)
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name) VALUES('r211-filters','筛选测试')`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := (&postgres.CatalogStore{DB: a.DB}).Categories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), `"key":"kind"`) || !strings.Contains(string(raw), `"key":"keyword"`) {
		t.Fatalf("buyer filters include retired structure or miss keywords: %s", raw)
	}
	groups, err := (&postgres.CatalogStore{DB: a.DB}).Dictionaries(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(groups)
	if !strings.Contains(string(raw), `"key":"kind"`) {
		t.Fatal("publication dictionary was removed")
	}
}

func TestR211HotKeywordsSearchAndManagement(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	recommendationUser(t, a, "r211-keyword-admin", "admin")
	store := &postgres.ManagementStore{DB: a.DB, NewID: newID}
	actor := management.Actor{ID: "r211-keyword-admin", Role: "admin"}
	in := catalog.DictionaryInput{ExpectedVersion: 1, Options: []catalog.DictionaryOption{
		{Value: "first", Label: "包子", Enabled: true, SortOrder: 2},
		{Value: "second", Label: "永夜头", Enabled: true, SortOrder: 1},
		{Value: "literal", Label: "100%_珍藏", Enabled: true, SortOrder: 3},
		{Value: "disabled", Label: "停用词", Enabled: false, SortOrder: 4},
	}}
	if _, err := store.SaveDictionary(ctx, management.Actor{ID: actor.ID, Role: "member"}, "keyword", in); err == nil {
		t.Fatal("member modified hot keywords")
	}
	if _, err := store.SaveDictionary(ctx, actor, "keyword", in); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveDictionary(ctx, actor, "keyword", in); err == nil {
		t.Fatal("stale keyword version overwritten")
	} else if f, ok := fault.As(err); !ok || f.Code != "VERSION_CONFLICT" {
		t.Fatal(err)
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type) VALUES('r211-hot','关键词测试','sale');
	 INSERT INTO product_dictionary_options(dictionary_key,value,label) VALUES('server','r211-east','东区'),('server','r211-west','西区');
	 INSERT INTO products(id,product_code,category_id,title,description,trade_type,price_unit,price_amount,attributes,status,approved_version) VALUES
	 ('r211-hot-a','R211-A','r211-hot','普通标题','完整介绍','sale','account',100,'{"server":"r211-east","tags":["包子"]}','published',1),
	 ('r211-hot-b','R211-B','r211-hot','普通标题','永夜头','sale','account',200,'{"server":"r211-west"}','published',1),
	 ('r211-hot-c','R211-C','r211-hot','100%_珍藏','完整介绍','sale','account',300,'{"server":"r211-east"}','published',1),
	 ('r211-hot-d','R211-D','r211-hot','100X珍藏','完整介绍','sale','account',400,'{"server":"r211-east"}','published',1),
	 ('r211-hot-hidden','R211-HIDDEN','r211-hot','包子','完整介绍','sale','account',100,'{"server":"r211-east"}','draft',NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	search := func(filters map[string][]string, want int) {
		t.Helper()
		page, err := a.catalogService().Search(ctx, catalog.SearchInput{Limit: 20, Filters: filters}, "")
		if err != nil || page.Total != want || len(page.Data) != want {
			t.Fatalf("search %v: total=%d rows=%d error=%v", filters, page.Total, len(page.Data), err)
		}
	}
	search(map[string][]string{"keyword": {"first", "second"}}, 2)
	search(map[string][]string{"keyword": {"first", "second"}, "server": {"r211-east"}}, 1)
	search(map[string][]string{"keyword": {"literal"}}, 1)
	for _, value := range []string{"disabled", "forged"} {
		if _, err := a.catalogService().Search(ctx, catalog.SearchInput{Limit: 20, Filters: map[string][]string{"keyword": {value}}}, ""); err == nil {
			t.Fatalf("invalid keyword accepted: %s", value)
		}
	}
	// Rename changes the effective search text without changing the selected URL code.
	in.ExpectedVersion = 2
	in.Options[0].Label = "不存在的词"
	in.Options[1].Enabled = false
	if _, err = store.SaveDictionary(ctx, actor, "keyword", in); err != nil {
		t.Fatal(err)
	}
	search(map[string][]string{"keyword": {"first"}}, 0)
	public, err := (&postgres.CatalogStore{DB: a.DB}).Dictionaries(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(public)
	if strings.Contains(string(raw), `"value":"second"`) {
		t.Fatal("disabled keyword exposed")
	}
	var audit int
	if err = a.DB.QueryRow(ctx, `SELECT count(*) FROM admin_logs WHERE action='dictionary.save' AND resource_id='keyword'`).Scan(&audit); err != nil || audit != 2 {
		t.Fatalf("missing audit: %d %v", audit, err)
	}
	in.ExpectedVersion = 3
	in.Options = nil
	if _, err = store.SaveDictionary(ctx, actor, "keyword", in); err != nil {
		t.Fatal(err)
	}
	search(nil, 4)
}
