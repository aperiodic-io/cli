package aperiodic

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAperiodic answers /api/v1/cli/* the way aperiodic.io does: a login,
// polls that stay pending for `pendingPolls` rounds, then the key.
type fakeAperiodic struct {
	mu           sync.Mutex
	pendingPolls int
	finalError   string
	polls        int
	loginName    string
	revoked      []string
}

func (f *fakeAperiodic) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/cli/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.loginName = body["name"]
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "secret-device-code",
			"user_code":                 "BCDF-GHJK",
			"verification_uri":          "https://aperiodic.io/device",
			"verification_uri_complete": "https://aperiodic.io/device?code=BCDF-GHJK",
			"expires_in":                600,
			"interval":                  5,
		})
	})
	mux.HandleFunc("POST /api/v1/cli/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["device_code"] != "secret-device-code" {
			t.Errorf("polled with %q", body["device_code"])
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.polls++
		if f.polls <= f.pendingPolls {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		if f.finalError != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": f.finalError})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"api_key":   "apk_testkeytestkeytestkeytestkeytestkeytestkeyt",
			"key_name":  "research-vm (aperiodic CLI)",
			"plan":      "loggedInNoSubscription",
			"plan_name": "Free",
		})
	})
	mux.HandleFunc("GET /api/v1/cli/whoami", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("X-API-KEY"), "apk_") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"plan":"loggedInNoSubscription","plan_name":"Free",
			"key":{"kind":"named","name":"research-vm (aperiodic CLI)","hint":"keyt"},
			"covers":{"datasets_in_full":19,"datasets_total":19,"history":"History for 2025","venues":["Binance"],"markets":"BTC, ETH, SOL and 10 RWA perps","live":"Delayed live stream"}}`))
	})
	mux.HandleFunc("DELETE /api/v1/cli/key", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Header.Get("X-API-KEY"))
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"revoked":true}`))
	})
	return mux
}

type authHarness struct {
	cli    *CLI
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	env    map[string]string
	opened []string
	slept  []time.Duration
}

func newAuthHarness(t *testing.T, fake *fakeAperiodic) *authHarness {
	t.Helper()
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)

	h := &authHarness{
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		env: map[string]string{
			"APERIODIC_API_URL":    server.URL + "/api/v1",
			"APERIODIC_CONFIG_DIR": t.TempDir(),
			"DISPLAY":              ":0",
		},
	}
	clock := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	h.cli = &CLI{
		Stdout:     h.stdout,
		Stderr:     h.stderr,
		Env:        func(key string) string { return h.env[key] },
		HTTPClient: server.Client(),
		OpenURL: func(url string) error {
			h.opened = append(h.opened, url)
			return nil
		},
		Sleep: func(d time.Duration) {
			h.slept = append(h.slept, d)
			clock = clock.Add(d)
		},
		Now: func() time.Time { return clock },
	}
	return h
}

func TestLoginSavesAKeyOnlyThisUserCanRead(t *testing.T) {
	fake := &fakeAperiodic{pendingPolls: 2}
	h := newAuthHarness(t, fake)

	if code := h.cli.Run([]string{"login"}); code != 0 {
		t.Fatalf("login exit %d, stderr %s", code, h.stderr)
	}

	if len(h.opened) != 1 || !strings.Contains(h.opened[0], "code=BCDF-GHJK") {
		t.Fatalf("opened %v", h.opened)
	}
	if !strings.Contains(h.stdout.String(), "BCDF-GHJK") {
		t.Errorf("the code is not shown: %s", h.stdout)
	}
	if fake.polls != 3 {
		t.Errorf("polled %d times, want 3", fake.polls)
	}
	if !strings.HasSuffix(fake.loginName, "(aperiodic CLI)") {
		t.Errorf("key named %q", fake.loginName)
	}

	creds, err := LoadCredentials(h.cli.Env)
	if err != nil || creds == nil {
		t.Fatalf("credentials: %v %v", creds, err)
	}
	if creds.APIKey != "apk_testkeytestkeytestkeytestkeytestkeytestkeyt" || creds.PlanName != "Free" {
		t.Errorf("saved %+v", creds)
	}
	path, _ := CredentialsPath(h.cli.Env)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no permission bits: Go reports any writable file as 0666, and
	// the file is kept private by the ACL of the user's profile (%APPDATA%).
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("credentials mode %v, want 0600", info.Mode().Perm())
	}
}

func TestLoginOverSSHPrintsTheLinkInsteadOfOpeningABrowser(t *testing.T) {
	h := newAuthHarness(t, &fakeAperiodic{})
	h.env["SSH_CONNECTION"] = "10.0.0.1 22 10.0.0.2 51000"

	if code := h.cli.Run([]string{"login"}); code != 0 {
		t.Fatalf("login exit %d: %s", code, h.stderr)
	}
	if len(h.opened) != 0 {
		t.Errorf("opened a browser over SSH: %v", h.opened)
	}
	if !strings.Contains(h.stdout.String(), "open this link on any device") {
		t.Errorf("stdout: %s", h.stdout)
	}
}

func TestLoginBacksOffOnSlowDownAndStopsWhenDenied(t *testing.T) {
	fake := &fakeAperiodic{pendingPolls: 0, finalError: "access_denied"}
	h := newAuthHarness(t, fake)

	if code := h.cli.Run([]string{"login", "--no-browser"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "denied") {
		t.Errorf("stderr: %s", h.stderr)
	}
	if _, err := os.Stat(func() string { p, _ := CredentialsPath(h.cli.Env); return p }()); !os.IsNotExist(err) {
		t.Errorf("a key was saved after a denial")
	}

	slow := &fakeAperiodic{pendingPolls: 1, finalError: "slow_down"}
	h2 := newAuthHarness(t, slow)
	slow.pendingPolls = 0
	h2.cli.Run([]string{"login", "--no-browser"})
	if len(h2.slept) < 2 || h2.slept[1] <= h2.slept[0] {
		t.Errorf("did not back off on slow_down: %v", h2.slept)
	}
}

func TestTheEnvironmentKeyComesBeforeTheSavedOne(t *testing.T) {
	h := newAuthHarness(t, &fakeAperiodic{})
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "apk_saved"}); err != nil {
		t.Fatal(err)
	}

	key, _, ok := h.cli.findAPIKey()
	if !ok || key != "apk_saved" {
		t.Fatalf("got %q %v", key, ok)
	}

	h.env["APERIODIC_API_KEY"] = "from-env"
	key, source, _ := h.cli.findAPIKey()
	if key != "from-env" || source != "APERIODIC_API_KEY" {
		t.Errorf("got %q from %q", key, source)
	}
}

func TestLogoutRevokesThisMachinesKeyAndRemovesIt(t *testing.T) {
	fake := &fakeAperiodic{}
	h := newAuthHarness(t, fake)
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "apk_saved", KeyName: "laptop"}); err != nil {
		t.Fatal(err)
	}

	if code := h.cli.Run([]string{"logout"}); code != 0 {
		t.Fatalf("logout exit %d: %s", code, h.stderr)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != "apk_saved" {
		t.Errorf("revoked %v", fake.revoked)
	}
	if creds, _ := LoadCredentials(h.cli.Env); creds != nil {
		t.Errorf("credentials still there: %+v", creds)
	}
	if !strings.Contains(h.stdout.String(), "revoked") {
		t.Errorf("stdout: %s", h.stdout)
	}
}

func TestWhoamiSaysWhichKeyAndWhatThePlanCovers(t *testing.T) {
	h := newAuthHarness(t, &fakeAperiodic{})
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "apk_saved"}); err != nil {
		t.Fatal(err)
	}

	if code := h.cli.Run([]string{"whoami"}); code != 0 {
		t.Fatalf("whoami exit %d: %s", code, h.stderr)
	}
	out := h.stdout.String()
	for _, want := range []string{"Free", "research-vm (aperiodic CLI)", "19 of 19", "Markets:  BTC, ETH, SOL and 10 RWA perps", "History for 2025", "Delayed live stream"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami output lacks %q:\n%s", want, out)
		}
	}
}

func TestWithoutAnyKeyCommandsSayHowToSignIn(t *testing.T) {
	h := newAuthHarness(t, &fakeAperiodic{})

	if code := h.cli.Run([]string{"whoami"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(h.stderr.String(), "aperiodic login") {
		t.Errorf("stderr: %s", h.stderr)
	}
}
