package management

import "testing"

func TestR20FinalRejectionAndCancelPublishing(t *testing.T) {
	for _, test := range []struct{ status, operation, decision, action, reason, want string }{
		{"pending", "review", "reject_final", "", "确认违规", "rejected_final"},
		{"pending", "review", "reject_final", "", "", ""},
		{"published", "review", "reject_final", "", "确认违规", ""},
		{"publishing", "status", "", "offline", "停止上架", "offline"},
	} {
		got, _, err := Transition(test.status, "member", test.operation, TransitionInput{Decision: test.decision, Action: test.action, Reason: test.reason})
		if got != test.want || (err != nil) != (test.want == "") {
			t.Fatalf("%+v: %s %v", test, got, err)
		}
	}
}
