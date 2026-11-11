package member

import (
	"context"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/member"
	"testing"
)

func TestServiceDeniesForgedPrivilegeBeforeRepository(t *testing.T) {
	service := New(nil)
	_, err := service.Gallery(context.Background(), member.Actor{ID: "m", Role: "member", Admin: true}, "product", member.GalleryInput{Limit: 20})
	f, ok := fault.As(err)
	if !ok || f.Kind != fault.Forbidden {
		t.Fatalf("err=%v", err)
	}
	_, err = service.Gallery(context.Background(), member.Actor{ID: "m", Role: "member"}, "product", member.GalleryInput{Limit: 20, ReviewID: "private-review"})
	f, ok = fault.As(err)
	if !ok || f.Kind != fault.Forbidden {
		t.Fatalf("err=%v", err)
	}
}
func TestServiceRejectsUnboundedOperationsBeforeRepository(t *testing.T) {
	service := New(nil)
	actor := member.Actor{ID: "m", Role: "member"}
	_, err := service.List(context.Background(), actor, member.ListInput{Limit: 100000})
	if err == nil {
		t.Fatal("unbounded page accepted")
	}
	_, err = service.AppendImages(context.Background(), actor, "p", member.AppendImagesInput{MediaIDs: make([]string, 21)})
	if err == nil {
		t.Fatal("unbounded batch accepted")
	}
}
