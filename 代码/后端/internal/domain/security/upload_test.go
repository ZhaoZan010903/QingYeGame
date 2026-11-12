package security

import "testing"

func TestRestrictionRequiresExplicitAdminDecision(t *testing.T) {
	valid := RestrictInput{Blocked: true, Reason: "确认恶意上传", PurgeStaging: true}
	if err := ValidateRestriction("admin", valid); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRestriction("member", valid); err == nil {
		t.Fatal("member authorized")
	}
	valid.Blocked = false
	if err := ValidateRestriction("admin", valid); err == nil {
		t.Fatal("unblock may not purge")
	}
}
