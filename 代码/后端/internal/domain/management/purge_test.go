package management

import (
	"qingye/backend/internal/domain/fault"
	"strings"
	"testing"
)

func TestPurgeRequiresExplicitConfirmationVersionAndReplayIdentity(t *testing.T) {
	valid := PurgeProductInput{ExpectedVersion: 2, Confirmation: "永久删除", RequestID: "purge:123-abc", Reason: strings.Repeat("因", 500)}
	if err := ValidatePurgeProduct("product", valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*PurgeProductInput){
		func(in *PurgeProductInput) { in.ExpectedVersion = 0 },
		func(in *PurgeProductInput) { in.Confirmation = " 永久删除" },
		func(in *PurgeProductInput) { in.Confirmation = "清空回收站" },
		func(in *PurgeProductInput) { in.RequestID = "" },
		func(in *PurgeProductInput) { in.RequestID = strings.Repeat("a", 81) },
		func(in *PurgeProductInput) { in.RequestID = "bad\x00key" },
		func(in *PurgeProductInput) { in.Reason = strings.Repeat("因", 501) },
		func(in *PurgeProductInput) { in.Reason = "bad\x00reason" },
	} {
		in := valid
		change(&in)
		if f, ok := fault.As(ValidatePurgeProduct("product", in)); !ok || f.Kind != fault.Invalid {
			t.Fatalf("invalid purge accepted: %+v", in)
		}
	}
	bulk := PurgeTrashInput{Snapshot: strings.Repeat("a", 64), Confirmation: "清空回收站", RequestID: "bulk-1"}
	if err := ValidatePurgeTrash(bulk); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []string{"", "all", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		bulk.Snapshot = snapshot
		if err := ValidatePurgeTrash(bulk); err == nil {
			t.Fatal("invalid snapshot accepted", snapshot)
		}
	}
}
