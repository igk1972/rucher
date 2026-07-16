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
	// Native systemd units routed to the user unit dir, including a cadre-shipped .service.
	for _, n := range []string{"backup.timer", "api.socket", "watch.path", "job.service"} {
		if !IsSystemdUnit(n) {
			t.Fatalf("%q should be a systemd unit", n)
		}
	}
	// Quadlet units and support files are not native systemd units.
	for _, n := range []string{"web.container", "data.volume", "app.env"} {
		if IsSystemdUnit(n) {
			t.Fatalf("%q should NOT be a systemd unit", n)
		}
	}
}

func TestShouldEnable(t *testing.T) {
	// Activator units are always enabled, regardless of content.
	for _, n := range []string{"backup.timer", "api.socket", "watch.path"} {
		if !ShouldEnable(n, nil) {
			t.Fatalf("%q should be enabled", n)
		}
	}
	// A .service is enabled only when it ships an [Install] section.
	withInstall := []byte("[Unit]\nDescription=x\n[Service]\nExecStart=/bin/true\n[Install]\nWantedBy=default.target\n")
	if !ShouldEnable("worker.service", withInstall) {
		t.Fatal("a .service with [Install] should be enabled")
	}
	oneshot := []byte("[Unit]\nDescription=x\n[Service]\nType=oneshot\nExecStart=/bin/true\n")
	if ShouldEnable("job.service", oneshot) {
		t.Fatal("an [Install]-less .service must not be enabled (install-only oneshot)")
	}
	// Section match is exact and case-sensitive: neither a lookalike nor a different case counts.
	for _, body := range []string{"[Service]\n[InstallSection]\n", "[install]\nWantedBy=x\n"} {
		if ShouldEnable("edge.service", []byte(body)) {
			t.Fatalf("%q must not count as an [Install] section", body)
		}
	}
	// Non-native files are never enabled, even with an [Install] body (Quadlet owns their .service).
	for _, n := range []string{"web.container", "app.env"} {
		if ShouldEnable(n, withInstall) {
			t.Fatalf("%q should never be enabled by ShouldEnable", n)
		}
	}
}

func TestUnitService(t *testing.T) {
	cases := map[string]string{
		"web.container": "web.service",
		"app.kube":      "app.service", // .kube, like .container, maps to the bare stem
		"data.volume":   "data-volume.service",
		"net.network":   "net-network.service",
		"db.pod":        "db-pod.service",
		"img.image":     "img-image.service",
		"x.build":       "x-build.service",
		"web":           "web", // no extension: returned unchanged, must not panic
	}
	for in, want := range cases {
		if got := UnitService(in); got != want {
			t.Fatalf("UnitService(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsReserved(t *testing.T) {
	for _, n := range []string{PruneTimer, PruneService} {
		if !IsReserved(n) {
			t.Fatalf("%q should be reserved", n)
		}
	}
	for _, n := range []string{"backup.timer", "rucher-prune.container", "web.container"} {
		if IsReserved(n) {
			t.Fatalf("%q should NOT be reserved", n)
		}
	}
}
