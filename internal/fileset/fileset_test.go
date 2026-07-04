package fileset

import "testing"

func TestHashStable(t *testing.T) {
	if Hash([]byte("abc")) != Hash([]byte("abc")) {
		t.Fatal("hash not stable")
	}
	if Hash([]byte("abc")) == Hash([]byte("abd")) {
		t.Fatal("hash collision on different input")
	}
}

func TestIsUnitFile(t *testing.T) {
	units := []string{"a.container", "b.volume", "c.network", "d.pod", "e.kube", "f.image", "g.build"}
	for _, n := range units {
		if !IsUnitFile(n) {
			t.Fatalf("%q should be a unit file", n)
		}
	}
	for _, n := range []string{"nginx.conf", "app.env", "compartment.yml"} {
		if IsUnitFile(n) {
			t.Fatalf("%q should NOT be a unit file", n)
		}
	}
}
