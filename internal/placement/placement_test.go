package placement

import (
	"slices"
	"testing"
)

func TestAssigned(t *testing.T) {
	data := []byte(`
placements:
  web: lima-essaim-01
  db: [lima-essaim-02, lima-essaim-01]
  cache: lima-essaim-03
`)
	got, err := Assigned(data, "lima-essaim-01")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"db", "web"}) {
		t.Fatalf("Assigned = %v, want [db web]", got)
	}
}

func TestAssignedNoneForNode(t *testing.T) {
	got, err := Assigned([]byte("placements: {web: a}\n"), "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected none, got %v", got)
	}
}
