package registration

import (
	"strings"
	"testing"
)

func TestRegistrationInputBoundaries(t *testing.T) {
	in := Input{Phone: "13800000000", Code: "123456", ChallengeID: "00000000-0000-4000-8000-000000000001", Password: "password-123", Agreement: Agreement{true, 1, 1}}
	if e := Validate(in); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Input){func(v *Input) { v.Phone = "+8613800000000" }, func(v *Input) { v.Code = "12345" }, func(v *Input) { v.Password = "1234567" }, func(v *Input) { v.Password = strings.Repeat("中", 25) }, func(v *Input) { v.ChallengeID = "not-id" }, func(v *Input) { v.Agreement.Accepted = false }, func(v *Input) { v.Agreement.Terms = 0 }} {
		next := in
		mutate(&next)
		if Validate(next) == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
