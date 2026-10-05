package domain

import "testing"

func TestPhysicalIdentityAndTokenCategories(t *testing.T) {
	source := SourceFingerprint{HostId: "host", Provider: Claude, ResolvedHomePath: "/home/test", VolumeId: "disk"}
	copy := source
	if source.Id() != copy.Id() {
		t.Fatal("same physical source must match")
	}
	copy.VolumeId = "replacement"
	if source.Id() == copy.Id() {
		t.Fatal("replaced volume must remain distinct")
	}
	tokens := Tokens{1000, 2000, 500, 100, 40}
	if tokens.Total() != 3600 {
		t.Fatal("reasoning was charged twice")
	}
	if AccountId(Cursor, " Alice@Example.COM ") != "cursor:alice@example.com" {
		t.Fatal("account normalization")
	}
}
