//go:build integration

package integration

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A self-hosted headscale on the host replaces SaaS Tailscale for the overlay
// tests: no auth key from .env, the coordination server + embedded DERP run
// locally, and cross-node traffic relays through the DERP (the Lima nodes are in
// separate usernets and cannot reach each other directly).
//
// Two non-obvious requirements (learned by spiking):
//   - headscale must serve over HTTPS: the tailscale client always dials the DERP
//     node over TLS, so a plain-HTTP DERP fails the relay handshake. A self-signed
//     cert whose SAN covers host.lima.internal is enough.
//   - the sidecar trusts that cert via SSL_CERT_FILE (tailscale is pure-Go, so it
//     honors SSL_CERT_FILE for both the control and DERP TLS).
const (
	headscaleVersion = "0.29.2"
	headscalePort    = "8085"
	hostGatewayIP    = "192.168.5.2" // the Lima gateway == the host, as guests see it
)

const headscaleConfig = `server_url: https://host.lima.internal:` + headscalePort + `
listen_addr: 0.0.0.0:` + headscalePort + `
tls_cert_path: ./tls.crt
tls_key_path: ./tls.key
noise:
  private_key_path: ./noise_private.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
  allocation: sequential
derp:
  server:
    enabled: true
    region_id: 999
    region_code: "headscale"
    region_name: "Headscale Embedded DERP"
    stun_listen_addr: "0.0.0.0:3478"
    private_key_path: ./derp_server_private.key
    automatically_add_embedded_derp_region: true
    ipv4: ` + hostGatewayIP + `
  urls: []
  paths: []
database:
  type: sqlite
  sqlite:
    path: ./db.sqlite
policy:
  mode: database
dns:
  magic_dns: false
  override_local_dns: false
  base_domain: essaim.internal
  nameservers:
    global: []
node:
  ephemeral:
    inactivity_timeout: 30m
disable_check_updates: true
unix_socket: ./headscale.sock
`

type headscale struct {
	dir        string
	bin        string
	preauthkey string
	certPEM    string
}

// ensureHeadscaleBinary returns a headscale binary: a system install if one is on
// PATH, otherwise a release binary downloaded once into a shared cache. Skips the
// test (rather than failing) if neither is available.
func ensureHeadscaleBinary(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("headscale"); err == nil {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	dir := filepath.Join(home, ".cache", "rucher-integration", "hs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir hs cache: %v", err)
	}
	asset := "headscale_" + headscaleVersion + "_darwin_arm64"
	bin := filepath.Join(dir, asset)
	if fi, err := os.Stat(bin); err == nil && fi.Mode()&0o111 != 0 {
		return bin
	}
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not found; cannot fetch headscale")
	}
	cmd := exec.Command("gh", "release", "download", "v"+headscaleVersion,
		"--repo", "juanfont/headscale", "--pattern", asset, "--output", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot download headscale: %v\n%s", err, out)
	}
	os.Chmod(bin, 0o755)
	return bin
}

// hscli runs a headscale CLI command against the local config, failing on error.
func hscli(t *testing.T, hs *headscale, args ...string) string {
	t.Helper()
	cmd := exec.Command(hs.bin, append([]string{"-c", "config.yaml"}, args...)...)
	cmd.Dir = hs.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("headscale %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// startHeadscale brings up headscale (HTTPS + embedded DERP) on the host, creates a
// user and a reusable preauthkey, and returns the running instance. Everything is
// torn down on test cleanup.
func startHeadscale(t *testing.T) *headscale {
	t.Helper()
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not found")
	}
	bin := ensureHeadscaleBinary(t)
	dir := homeTemp(t, "hs-")

	// Self-signed cert; SAN must cover the server_url host and the DERP IPv4.
	openssl := exec.Command("openssl", "req", "-x509", "-newkey", "ec",
		"-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes",
		"-keyout", "tls.key", "-out", "tls.crt", "-days", "2",
		"-subj", "/CN=host.lima.internal",
		"-addext", "subjectAltName=DNS:host.lima.internal,IP:"+hostGatewayIP,
		"-addext", "basicConstraints=critical,CA:TRUE")
	openssl.Dir = dir
	if out, err := openssl.CombinedOutput(); err != nil {
		t.Fatalf("openssl: %v\n%s", err, out)
	}
	writeFileP(t, filepath.Join(dir, "config.yaml"), headscaleConfig)

	srv := exec.Command(bin, "-c", "config.yaml", "serve")
	srv.Dir = dir
	logf, _ := os.Create(filepath.Join(dir, "serve.log"))
	srv.Stdout, srv.Stderr = logf, logf
	if err := srv.Start(); err != nil {
		t.Fatalf("start headscale: %v", err)
	}
	t.Cleanup(func() { srv.Process.Kill(); srv.Wait() })

	// Wait for health (host side, cert not verified — we only need it up).
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   2 * time.Second,
	}
	healthy := false
	for i := 0; i < 30; i++ {
		if resp, err := client.Get("https://127.0.0.1:" + headscalePort + "/health"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				healthy = true
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !healthy {
		log, _ := os.ReadFile(filepath.Join(dir, "serve.log"))
		t.Fatalf("headscale did not become healthy:\n%s", log)
	}

	hs := &headscale{dir: dir, bin: bin}
	hscli(t, hs, "users", "create", "essaim")
	raw := hscli(t, hs, "preauthkeys", "create", "--reusable", "--expiration", "24h", "--user", "1", "-o", "json")
	var pk struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &pk); err != nil || pk.Key == "" {
		t.Fatalf("could not parse preauthkey from headscale output: %q (err %v)", raw, err)
	}
	hs.preauthkey = pk.Key
	cert, err := os.ReadFile(filepath.Join(dir, "tls.crt"))
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	hs.certPEM = string(cert)
	t.Log("headscale up (HTTPS + embedded DERP)")
	return hs
}

// overlayCadreFiles builds an overlay cadre (pod + kernel-mode tailscale sidecar +
// nginx) wired to the self-hosted headscale: the sidecar trusts the shipped cert and
// logs in to the local coordination server; the authkey rides the secret machinery.
func overlayCadreFiles(name, tsHost, certPEM string) map[string]string {
	ts := "[Container]\n" +
		"ContainerName=" + name + "-ts\n" +
		"Image=docker.io/tailscale/tailscale:latest\n" +
		"Pod=" + name + ".pod\n" +
		"AddDevice=/dev/net/tun\n" +
		"AddCapability=NET_ADMIN\n" +
		"AddCapability=NET_RAW\n" +
		"Volume=%h/.config/containers/systemd/tls.crt:/certs/tls.crt:ro\n" +
		"Secret=ts-authkey,type=env,target=TS_AUTHKEY\n" +
		"Environment=TS_USERSPACE=false\n" +
		"Environment=TS_HOSTNAME=" + tsHost + "\n" +
		"Environment=TS_STATE_DIR=/tmp/tsstate\n" +
		"Environment=TS_ACCEPT_DNS=false\n" +
		"Environment=SSL_CERT_FILE=/certs/tls.crt\n" +
		"Environment=TS_EXTRA_ARGS=--login-server=https://host.lima.internal:" + headscalePort + "\n" +
		"[Install]\nWantedBy=default.target\n"
	app := "[Container]\n" +
		"ContainerName=" + name + "-app\n" +
		"Image=docker.io/library/nginx:alpine\n" +
		"Pod=" + name + ".pod\n" +
		"[Install]\nWantedBy=default.target\n"
	// The sidecar shares the pod's netns, so host entries must be set on the pod, not
	// the container (podman rejects per-container --add-host in a shared netns).
	pod := "[Pod]\nPodName=" + name + "\nPodmanArgs=--add-host=host.lima.internal:" + hostGatewayIP + "\n"
	return map[string]string{
		"rucher.yml":            "name: " + name + "\nsecrets:\n  from: secrets.sops.yaml\n  create:\n    - ts-authkey\n",
		name + ".pod":           pod,
		"overlay-ts.container":  ts,
		"overlay-app.container": app,
		"tls.crt":               certPEM,
	}
}

// deployOverlay applies an overlay cadre on a node via rucher, with the headscale
// preauthkey delivered through the normal SOPS -> podman secret path.
func deployOverlay(t *testing.T, node, name, tsHost string, hs *headscale) {
	t.Helper()
	cleanupCadre(t, name, node)
	rec := rucherNode(t, node, "node", "cadre", "new", name)
	if rec.code != 0 {
		t.Fatalf("new %s on %s: %s", name, node, rec.stderr)
	}
	parent := newCadre(t, name, overlayCadreFiles(name, tsHost, hs.certPEM))
	sopsEncrypt(t, rec.out(), "ts-authkey: "+hs.preauthkey+"\n", filepath.Join(parent, name, "secrets.sops.yaml"))
	if r := nodeApply(t, node, parent, name); r.code != 0 {
		uid := nodeSudo(t, node, "id", "-u", "rucher-"+name).out()
		jr := nodeSudo(t, node, "journalctl", "_SYSTEMD_USER_UNIT=overlay-ts.service", "_UID="+uid, "-n", "60", "--no-pager")
		t.Fatalf("apply %s on %s: code=%d err=%q out=%q\n--- overlay-ts journal ---\n%s%s",
			name, node, r.code, r.stderr, r.stdout, jr.stdout, jr.stderr)
	}
}

// overlayIP polls the sidecar until tailscale reports its 100.64.x overlay address.
func overlayIP(t *testing.T, node, name string) string {
	t.Helper()
	for i := 0; i < 25; i++ {
		r := cadreUser(t, node, name, "podman", "exec", name+"-ts", "tailscale", "ip", "-4")
		if ip := strings.TrimSpace(r.stdout); r.code == 0 && strings.HasPrefix(ip, "100.") {
			return ip
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("cadre %s on %s never got an overlay IP", name, node)
	return ""
}

// TestHeadscaleServesToNode is a fast sanity check: headscale comes up on the host
// and is reachable from a node, and a preauthkey is minted (no .env key involved).
func TestHeadscaleServesToNode(t *testing.T) {
	requireNodes(t, node1)
	hs := startHeadscale(t)
	if hs.preauthkey == "" {
		t.Fatal("empty preauthkey")
	}
	r := nodeShell(t, node1, "curl", "-sk", "https://host.lima.internal:"+headscalePort+"/health")
	if !strings.Contains(r.stdout, "pass") {
		t.Fatalf("node cannot reach headscale health: code=%d out=%q err=%q", r.code, r.stdout, r.stderr)
	}
}

// TestHeadscaleOverlayCrossNode is the full flow: rucher deploys an overlay cadre on
// two nodes, both sidecars join the self-hosted headscale, and node-01's workload
// reaches node-02's nginx over the overlay IP — relayed through the embedded DERP,
// since the nodes cannot reach each other directly.
func TestHeadscaleOverlayCrossNode(t *testing.T) {
	requireNodes(t, node1, node2)
	// tun must be usable by the cadre user for a kernel-mode sidecar.
	if r := nodeSudo(t, node1, "test", "-c", "/dev/net/tun"); r.code != 0 {
		t.Skip("/dev/net/tun not present on node1")
	}
	hs := startHeadscale(t)
	const a, b = "itovla", "itovlb"
	t.Cleanup(func() { cleanupCadre(t, a, node1); cleanupCadre(t, b, node2) })

	deployOverlay(t, node1, a, "essaim-a", hs)
	deployOverlay(t, node2, b, "essaim-b", hs)

	ipA := overlayIP(t, node1, a)
	ipB := overlayIP(t, node2, b)
	t.Logf("overlay IPs: %s=%s  %s=%s", a, ipA, b, ipB)

	// Cross-node reachability via DERP: node-01's sidecar pings node-02's overlay IP.
	// --until-direct=false stops at the first (DERP-relayed) pong: the nodes are in
	// separate usernets and can never get a direct path, so the default (keep trying
	// for a direct connection) would exit non-zero despite the relay working.
	var ping string
	for i := 0; i < 30; i++ {
		r := cadreUser(t, node1, a, "podman", "exec", a+"-ts", "tailscale", "ping", "--until-direct=false", ipB)
		if r.code == 0 && strings.Contains(r.stdout, "pong") {
			ping = r.stdout
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.Contains(ping, "pong") {
		raw := cadreUser(t, node1, a, "podman", "exec", a+"-ts", "tailscale", "ping", "--until-direct=false", ipB).stdout
		st := cadreUser(t, node1, a, "podman", "exec", a+"-ts", "tailscale", "status").stdout
		nc := cadreUser(t, node1, a, "podman", "exec", a+"-ts", "tailscale", "netcheck").stdout
		t.Fatalf("node-01 sidecar could not reach node-02 overlay IP %s\n--- ping ---\n%s\n--- status ---\n%s\n--- netcheck ---\n%s",
			ipB, raw, st, nc)
	}
	if !strings.Contains(ping, "DERP") {
		t.Logf("note: reachable but not reported via DERP:\n%s", ping)
	}

	// L7 over the overlay: node-01's app fetches nginx running in node-02's pod.
	var body string
	for i := 0; i < 10; i++ {
		r := cadreUser(t, node1, a, "podman", "exec", a+"-app", "wget", "-qO-", "-T", "5", "http://"+ipB+"/")
		if r.code == 0 && strings.Contains(r.stdout, "nginx") {
			body = r.stdout
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.Contains(body, "nginx") {
		t.Fatalf("node-01 app could not fetch nginx on node-02 over the overlay:\n%s", body)
	}
	t.Logf("overlay reachable: %s app on %s fetched nginx on %s", a, node1, node2)
}
