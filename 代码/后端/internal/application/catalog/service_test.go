package catalog

import (
	"context"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/recommendation"
	"testing"
)

type catalogProbe struct {
	Repository
	query    catalog.SearchInput
	searches int
}

func (p *catalogProbe) Search(_ context.Context, q catalog.SearchInput) ([]map[string]any, int, error) {
	p.searches++
	p.query = q
	return []map[string]any{{"id": "p1", "description": "long", "review_note": "private", "legacy_payload": "private", "internal_secret": "future private column", "title": "public"}}, 72, nil
}
func (p *catalogProbe) FilterOptions(context.Context) (map[string]map[string]bool, error) {
	return map[string]map[string]bool{"server": {"east": true}}, nil
}
func (p *catalogProbe) PublicImages(_ context.Context, id, expected string, limit, offset int) ([]map[string]any, int, int, error) {
	if expected != "7" {
		return nil, 0, 0, fault.New(fault.Conflict, "VERSION_CONFLICT", "changed")
	}
	return []map[string]any{{"id": "image"}}, 50000, 7, nil
}

type personalProbe struct{ disabled bool }

func (p personalProbe) Personalized(context.Context, string) (recommendation.Profile, string, error) {
	profile := recommendation.NewProfile()
	profile.Enabled = !p.disabled
	return profile, "cold-start", nil
}

func TestSearchRejectsUnsafePriceComparisonBeforeQuery(t *testing.T) {
	repo := &catalogProbe{}
	s := New(repo, personalProbe{})
	price := "100"
	_, err := s.Search(context.Background(), catalog.SearchInput{Limit: 20, MinPrice: &price}, "")
	f, ok := fault.As(err)
	if !ok || f.Code != "PRICE_UNIT_REQUIRED" || repo.searches != 0 {
		t.Fatalf("err=%v queries=%d", err, repo.searches)
	}
	_, err = s.Search(context.Background(), catalog.SearchInput{Limit: 20, Filters: map[string][]string{"server": {"forged"}}}, "")
	f, ok = fault.As(err)
	if !ok || f.Code != "INVALID_FILTERS" || repo.searches != 0 {
		t.Fatalf("err=%v queries=%d", err, repo.searches)
	}
}
func TestSearchDoesNotExposePrivateFieldsOrAcceptCallerWeights(t *testing.T) {
	repo := &catalogProbe{}
	s := New(repo, personalProbe{disabled: true})
	page, err := s.Search(context.Background(), catalog.SearchInput{Limit: 20, Sort: "recommended", Weights: map[string]map[string]float64{"itemcf": {"forged": 99}}}, "member-hash")
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.query.Weights) != 0 {
		t.Fatal("caller supplied recommendation weights survived")
	}
	for _, key := range []string{"description", "review_note", "legacy_payload", "internal_secret"} {
		if _, ok := page.Data[0][key]; ok {
			t.Fatalf("leaked %s", key)
		}
	}
	if page.Total != 72 || page.NextCursor == nil || page.Data[0]["title"] != "public" {
		t.Fatalf("bad response %+v", page)
	}
}
func TestGalleryPreservesConflictAndUnboundedTotal(t *testing.T) {
	s := New(&catalogProbe{}, personalProbe{})
	_, err := s.Images(context.Background(), "p1", "6", 20, 0)
	f, ok := fault.As(err)
	if !ok || f.Kind != fault.Conflict {
		t.Fatalf("conflict lost: %v", err)
	}
	page, err := s.Images(context.Background(), "p1", "7", 20, 12000)
	if err != nil || page.Total != 50000 || page.Version != 7 || page.NextCursor == nil {
		t.Fatalf("gallery page: %+v %v", page, err)
	}
	_, err = s.Images(context.Background(), "p1", "7", 50000, 0)
	f, ok = fault.As(err)
	if !ok || f.Kind != fault.Invalid {
		t.Fatalf("unbounded batch accepted: %v", err)
	}
}
