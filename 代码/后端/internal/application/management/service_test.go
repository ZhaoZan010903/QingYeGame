package management

import (
	"context"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"testing"
)

func TestManagementServiceRechecksRoleBeforeRepository(t *testing.T) {
	service := New(nil, nil, nil)
	actor := management.Actor{ID: "m", Role: "member"}
	actions := []func() error{
		func() error {
			_, e := service.Products(context.Background(), actor, management.ProductQuery{Limit: 20})
			return e
		},
		func() error {
			_, e := service.SavePage(context.Background(), actor, "page", management.PageInput{Title: "hello"})
			return e
		},
		func() error {
			_, e := service.TransitionProduct(context.Background(), actor, "p", "publish", management.TransitionInput{})
			return e
		},
	}
	for _, action := range actions {
		f, ok := fault.As(action())
		if !ok || f.Kind != fault.Forbidden {
			t.Fatal("management role gate failed")
		}
	}
}
func TestManagementServicePreservesSelfProtection(t *testing.T) {
	service := New(nil, nil, nil)
	actor := management.Actor{ID: "admin", Role: "admin"}
	enabled := false
	_, err := service.UpdateUser(context.Background(), actor, "admin", management.UserInput{Enabled: &enabled})
	f, ok := fault.As(err)
	if !ok || f.Code != "INVALID_USER" {
		t.Fatalf("err=%v", err)
	}
}
func TestManagementValidationRunsBeforePersistence(t *testing.T) {
	service := New(nil, nil, nil)
	actor := management.Actor{ID: "admin", Role: "admin"}
	_, err := service.SaveContact(context.Background(), actor, "", management.ContactInput{Name: "test", Channel: "url", Value: "javascript:alert(1)"})
	f, ok := fault.As(err)
	if !ok || f.Code != "INVALID_CONTACT" {
		t.Fatalf("err=%v", err)
	}
	_, err = service.SaveSetting(context.Background(), actor, "private_secret", management.SettingInput{})
	f, ok = fault.As(err)
	if !ok || f.Code != "UNKNOWN_SETTING" {
		t.Fatalf("err=%v", err)
	}
}
