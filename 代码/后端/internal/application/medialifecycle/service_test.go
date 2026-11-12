package medialifecycle

import "testing"

func TestNextKeyRejectsDirectoryAndLegacyPaths(t *testing.T) {
	for _, key := range []string{"uploads/x.jpg", "", "r20/test/objects/id/gen/", "r20/test/objects/id/../original", "r20/test/objects/id/gen/original?x=1", "r20/test/objects/id/gen/original/child"} {
		if _, err := NextKey(key, "new"); err == nil {
			t.Fatalf("accepted %q", key)
		}
	}
	key, err := NextKey("r20/test/objects/media/old/original", "new")
	if err != nil || key != "r20/test/objects/media/new/original" {
		t.Fatal(key, err)
	}
}
