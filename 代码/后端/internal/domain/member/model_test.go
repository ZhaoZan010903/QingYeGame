package member

import (
	"qingye/backend/internal/domain/fault"
	"testing"
)

func TestMemberTransitionRules(t *testing.T) {
	cases := []struct {
		status string
		submit bool
		want   string
	}{{"draft", true, "pending"}, {"rejected", true, "pending"}, {"offline", true, "pending"}, {"pending", false, "draft"}, {"published", false, "offline"}, {"publishing", false, "offline"}, {"published", true, ""}, {"pending", true, ""}, {"draft", false, ""}}
	for _, tc := range cases {
		t.Run(tc.status+map[bool]string{true: "/submit", false: "/withdraw"}[tc.submit], func(t *testing.T) {
			next, err := NextStatus(tc.status, tc.submit)
			if next != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("next=%q err=%v", next, err)
			}
		})
	}
}
func TestMemberIdentityCannotGrantManagementAccess(t *testing.T) {
	for _, a := range []Actor{{}, {ID: "u", Role: "member", Admin: true}, {ID: "u", Role: "admin"}, {ID: "u", Role: "unknown"}} {
		if err := ValidateActor(a); err == nil {
			t.Fatalf("accepted %#v", a)
		}
	}
	for _, a := range []Actor{{ID: "u", Role: "member"}, {ID: "a", Role: "admin", Admin: true}} {
		if err := ValidateActor(a); err != nil {
			t.Fatal(err)
		}
	}
}
func TestVersionMustBePositiveAndCurrent(t *testing.T) {
	for _, wanted := range []int{0, -1, 1, 3} {
		f, ok := fault.As(CheckVersion(2, wanted))
		if !ok || f.Kind != fault.Conflict {
			t.Fatalf("accepted stale version %d", wanted)
		}
	}
	if err := CheckVersion(2, 2); err != nil {
		t.Fatal(err)
	}
}
