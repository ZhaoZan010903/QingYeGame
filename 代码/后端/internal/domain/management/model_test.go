package management

import (
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"testing"
)

func TestPublishTemplateCannotReuseDedicatedFields(t *testing.T) {
	for _, key := range []string{"custom_tags", "private_credentials", "qq_account", "qq-account", "qqaccount", "qq_password", "password", "secret_note"} {
		if err := ValidateFilters([]catalog.FilterGroup{{Key: key, Label: "错误配置", Type: "text"}}); err == nil {
			t.Fatalf("reserved template field accepted: %s", key)
		}
	}
	for _, key := range []string{"game", "tags", "server", "custom_style"} {
		if err := ValidateFilters([]catalog.FilterGroup{{Key: key, Label: "历史兼容", Type: "text"}}); err != nil {
			t.Fatalf("compatible template field rejected: %s: %v", key, err)
		}
	}
}

func TestReviewCannotBypassMemberApproval(t *testing.T) {
	cases := []struct {
		name, status, source, operation string
		input                           TransitionInput
		want                            string
	}{
		{"unknown operation", "published", "member", "unknown", TransitionInput{Action: "offline", Reason: "下架"}, ""},
		{"member direct publish", "draft", "member", "publish", TransitionInput{}, ""},
		{"pending approval", "pending", "member", "review", TransitionInput{Decision: "approve"}, "published"},
		{"unsubmitted approval", "draft", "member", "review", TransitionInput{Decision: "approve"}, ""},
		{"reason required", "published", "member", "status", TransitionInput{Action: "offline"}, ""},
		{"offline with reason", "published", "member", "status", TransitionInput{Action: "offline", Reason: "下架"}, "offline"},
		{"platform publish", "draft", "platform", "publish", TransitionInput{}, "published"},
		{"reject requires reason", "pending", "member", "review", TransitionInput{Decision: "reject"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, _, err := Transition(tc.status, tc.source, tc.operation, tc.input)
			if target != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("target=%q err=%v", target, err)
			}
		})
	}
}
func TestContactAndPriceEnumsAreExact(t *testing.T) {
	bad := ContactInput{Name: "客服", Channel: "wechat|qq"}
	if err := ValidateContact(bad); err == nil {
		t.Fatal("combined channel accepted")
	}
	bad.Channel = "wechat"
	bad.Purpose = []string{"sale|rent"}
	if err := ValidateContact(bad); err == nil {
		t.Fatal("combined purpose accepted")
	}
	bad.Purpose = []string{"sale"}
	if err := ValidateContact(bad); err != nil {
		t.Fatal(err)
	}
	for _, unit := range []string{"hour|day", "account"} {
		err := ValidateCategoryRules(CategoryInput{TradeType: "rent", PriceUnits: []string{unit}})
		f, ok := fault.As(err)
		if !ok || f.Code != "INVALID_CATEGORY" {
			t.Fatalf("accepted unit %q", unit)
		}
	}
}
