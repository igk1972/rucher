// SPDX-License-Identifier: AGPL-3.0-or-later

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
	for _, n := range []string{"nginx.conf", "app.env", "rucher.yml"} {
		if IsUnitFile(n) {
			t.Fatalf("%q should NOT be a unit file", n)
		}
	}
}

func TestIsSystemdUnit(t *testing.T) {
	for _, n := range []string{"backup.timer", "api.socket", "watch.path"} {
		if !IsSystemdUnit(n) {
			t.Fatalf("%q should be a systemd unit", n)
		}
	}
	// Quadlet units, support files, and .service (Quadlet generates those) are not
	// native systemd units routed to the user unit dir.
	for _, n := range []string{"web.container", "data.volume", "app.env", "backup.service"} {
		if IsSystemdUnit(n) {
			t.Fatalf("%q should NOT be a systemd unit", n)
		}
	}
}
