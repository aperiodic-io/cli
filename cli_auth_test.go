package aperiodic

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	logins       int
	loginName    string
	revoked      []string
	// newKey is the key a finished login hands out (a default when empty).
	newKey string
	// deviceURL replaces the server's own /device link when set.
	deviceURL string
	// dead keys are revoked: whoami, data and DELETE /cli/key answer 401.
	dead map[string]bool
	// dataKeys are the keys data requests arrived with.
	dataKeys []string
	// deletes are the keys every DELETE /cli/key arrived with, answered or not.
	deletes []string
}

func (f *fakeAperiodic) isDead(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dead[key]
}

func deny(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"Access denied"}`))
}

func (f *fakeAperiodic) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/cli/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.logins++
		f.loginName = body["name"]
		device := f.deviceURL
		f.mu.Unlock()
		// Like aperiodic.io, the link is on the server's own origin.
		if device == "" {
			device = "http://" + r.Host + "/device?code=BCDF-GHJK"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "secret-device-code",
			"user_code":                 "BCDF-GHJK",
			"verification_uri":          "http://" + r.Host + "/device",
			"verification_uri_complete": device,
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
		key := f.newKey
		if key == "" {
			key = "apk_testkeytestkeytestkeytestkeytestkeytestkeyt"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"api_key":   key,
			"key_name":  "research-vm (aperiodic CLI)",
			"plan":      "loggedInNoSubscription",
			"plan_name": "Free",
		})
	})
	mux.HandleFunc("GET /api/v1/cli/whoami", func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-KEY")
		if key == "DEMO-KEY" {
			_, _ = w.Write([]byte(`{"plan":"demo","plan_name":"Preview","key":{"kind":"demo"},"covers":{"datasets_in_full":0,"datasets_total":19}}`))
			return
		}
		if !strings.HasPrefix(key, "apk_") || f.isDead(key) {
			deny(w)
			return
		}
		_, _ = w.Write([]byte(`{"plan":"loggedInNoSubscription","plan_name":"Free",
			"key":{"kind":"named","name":"research-vm (aperiodic CLI)","hint":"keyt"},
			"covers":{"datasets_in_full":19,"datasets_total":19,"history":"History for 2025","venues":["Binance"],"markets":"BTC, ETH, SOL and 10 RWA perps","live":"Delayed live stream"}}`))
	})
	mux.HandleFunc("DELETE /api/v1/cli/key", func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-KEY")
		f.mu.Lock()
		f.deletes = append(f.deletes, key)
		f.mu.Unlock()
		if f.isDead(key) {
			deny(w)
			return
		}
		if !strings.HasPrefix(key, "apk_") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"not_a_named_key","error_description":"This is not a named key. Rotate an account’s default key at https://aperiodic.io/home/api."}`))
			return
		}
		f.mu.Lock()
		f.revoked = append(f.revoked, key)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"revoked":true}`))
	})
	dataHandler := func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-KEY")
		f.mu.Lock()
		f.dataKeys = append(f.dataKeys, key)
		f.mu.Unlock()
		if f.isDead(key) {
			deny(w)
			return
		}
		_, _ = w.Write([]byte(`{"files":[],"symbols":[]}`))
	}
	mux.HandleFunc("GET /api/v1/data/preview/{metric}", dataHandler)
	mux.HandleFunc("GET /api/v1/data/{metric}", dataHandler)
	mux.HandleFunc("GET /api/v1/metadata/symbols", dataHandler)
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
		Stdout: h.stdout,
		Stderr: h.stderr,
		Env:    func(key string) string { return h.env[key] },
		client: server.Client(),
		OpenURL: func(url string) error {
			h.opened = append(h.opened, url)
			return nil
		},
		sleepFunc: func(d time.Duration) {
			h.slept = append(h.slept, d)
			clock = clock.Add(d)
		},
		nowFunc: func() time.Time { return clock },
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

func savedPath(t *testing.T, h *authHarness) string {
	t.Helper()
	path, err := CredentialsPath(h.cli.Env)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoginKeepsASavedKeyThatStillWorks(t *testing.T) {
	fake := &fakeAperiodic{}
	h := newAuthHarness(t, fake)
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "apk_saved", KeyName: "laptop"}); err != nil {
		t.Fatal(err)
	}

	if code := h.cli.Run([]string{"login"}); code != 0 {
		t.Fatalf("login exit %d: %s", code, h.stderr)
	}
	want := `Already signed in as "research-vm (aperiodic CLI)" on Free. Run 'aperiodic login --force' to replace it.`
	if !strings.Contains(h.stdout.String(), want) {
		t.Errorf("stdout lacks %q:\n%s", want, h.stdout)
	}
	if fake.logins != 0 || len(h.opened) != 0 {
		t.Errorf("started a login: %d logins, opened %v", fake.logins, h.opened)
	}
	if creds, _ := LoadCredentials(h.cli.Env); creds == nil || creds.APIKey != "apk_saved" {
		t.Errorf("the saved key changed: %+v", creds)
	}
}

func TestForcedLoginRevokesTheKeyItReplaces(t *testing.T) {
	fake := &fakeAperiodic{newKey: "apk_newkeynewkeynewkeynewkeynewkeynewkeynewke"}
	h := newAuthHarness(t, fake)
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "apk_old", KeyName: "laptop"}); err != nil {
		t.Fatal(err)
	}

	if code := h.cli.Run([]string{"login", "--force", "--no-browser"}); code != 0 {
		t.Fatalf("login exit %d: %s", code, h.stderr)
	}
	if fake.logins != 1 {
		t.Errorf("logins %d, want 1", fake.logins)
	}
	if creds, _ := LoadCredentials(h.cli.Env); creds == nil || creds.APIKey != fake.newKey {
		t.Errorf("saved %+v, want the new key", creds)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != "apk_old" {
		t.Errorf("revoked %v, want the old key only", fake.revoked)
	}
	if !strings.Contains(h.stdout.String(), `The key this machine had before, "laptop", is revoked.`) {
		t.Errorf("stdout: %s", h.stdout)
	}
}

func TestLoginReplacesASavedKeyThatWasRevoked(t *testing.T) {
	fake := &fakeAperiodic{dead: map[string]bool{"apk_dead": true}}
	h := newAuthHarness(t, fake)
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "apk_dead"}); err != nil {
		t.Fatal(err)
	}

	if code := h.cli.Run([]string{"login", "--no-browser"}); code != 0 {
		t.Fatalf("login exit %d: %s", code, h.stderr)
	}
	if fake.logins != 1 {
		t.Errorf("logins %d, want 1", fake.logins)
	}
	if creds, _ := LoadCredentials(h.cli.Env); creds == nil || creds.APIKey == "apk_dead" {
		t.Errorf("saved %+v, want a new key", creds)
	}
	// The old key is tried once more, and its 401 is not an error.
	if len(fake.deletes) != 1 || fake.deletes[0] != "apk_dead" {
		t.Errorf("revoke attempts %v", fake.deletes)
	}
	if h.stderr.Len() != 0 {
		t.Errorf("stderr: %s", h.stderr)
	}
}

func TestPreviewUsesTheDemoKeyEvenWithASavedKey(t *testing.T) {
	fake := &fakeAperiodic{dead: map[string]bool{"apk_revoked": true}}
	h := newAuthHarness(t, fake)
	t.Setenv("APERIODIC_API_URL", h.env["APERIODIC_API_URL"])
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "apk_revoked"}); err != nil {
		t.Fatal(err)
	}
	args := []string{"ohlcv", "--preview", "--symbol", "perpetual-BTC-USDT:USDT",
		"--interval", "5m", "--start-date", "2025-05-01", "--end-date", "2025-05-31", "--output-dir", t.TempDir()}

	if code := h.cli.Run(args); code != 0 {
		t.Fatalf("preview exit %d: %s", code, h.stderr)
	}
	h.env["APERIODIC_API_KEY"] = "from-env"
	if code := h.cli.Run(args); code != 0 {
		t.Fatalf("preview with APERIODIC_API_KEY exit %d: %s", code, h.stderr)
	}
	if want := []string{"DEMO-KEY", "from-env"}; strings.Join(fake.dataKeys, ",") != strings.Join(want, ",") {
		t.Errorf("data requests sent keys %v, want %v", fake.dataKeys, want)
	}
}

func TestARevokedSavedKeySaysToSignInAgain(t *testing.T) {
	fake := &fakeAperiodic{dead: map[string]bool{"apk_revoked": true, "env-revoked": true}}
	h := newAuthHarness(t, fake)
	t.Setenv("APERIODIC_API_URL", h.env["APERIODIC_API_URL"])
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "apk_revoked"}); err != nil {
		t.Fatal(err)
	}
	want := "The key saved in " + savedPath(t, h) + " was rejected (revoked?). Run 'aperiodic login' again."

	for _, args := range [][]string{{"symbols"}, {"whoami"}} {
		h.stderr.Reset()
		if code := h.cli.Run(args); code != 1 {
			t.Fatalf("%v exit %d", args, code)
		}
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("%v stderr lacks %q:\n%s", args, want, h.stderr)
		}
	}

	// A key from the environment is not the saved file's to explain.
	h.env["APERIODIC_API_KEY"] = "env-revoked"
	h.stderr.Reset()
	if code := h.cli.Run([]string{"symbols"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(h.stderr.String(), "The key saved in") || !strings.Contains(h.stderr.String(), "401") {
		t.Errorf("stderr: %s", h.stderr)
	}
}

func TestLoginOpensOnlyAWebLinkOnTheAPIHost(t *testing.T) {
	for _, link := range []string{"file:///etc/passwd", "https://evil.example/device?code=BCDF-GHJK"} {
		fake := &fakeAperiodic{deviceURL: link}
		h := newAuthHarness(t, fake)

		if code := h.cli.Run([]string{"login"}); code != 0 {
			t.Fatalf("login exit %d: %s", code, h.stderr)
		}
		if len(h.opened) != 0 {
			t.Errorf("opened %v", h.opened)
		}
		if !strings.Contains(h.stdout.String(), "open this link on any device:\n\n  "+link) {
			t.Errorf("the link is not printed:\n%s", h.stdout)
		}
	}
}

func TestIsOpenableLink(t *testing.T) {
	for base, cases := range map[string]map[string]bool{
		"https://aperiodic.io/api/v1": {
			"https://aperiodic.io/device?code=BCDF-GHJK": true,
			"https://APERIODIC.io/device":                true,
			"http://aperiodic.io/device":                 false,
			"https://aperiodic.io.evil.example/device":   false,
			"https://evil.example/device":                false,
			"file:///etc/passwd":                         false,
			`C:\Windows\System32\calc.exe`:               false,
			"javascript:alert(1)":                        false,
			"ms-settings:":                               false,
			"":                                           false,
		},
		"http://127.0.0.1:8080/api/v1": {
			"http://127.0.0.1:9999/device?code=BCDF-GHJK": true,
			"http://localhost/device":                     false,
		},
	} {
		c := &CLI{Env: func(key string) string {
			if key == "APERIODIC_API_URL" {
				return base
			}
			return ""
		}}
		for link, want := range cases {
			if got := c.isOpenableLink(link); got != want {
				t.Errorf("base %s: isOpenableLink(%q) = %v, want %v", base, link, got, want)
			}
		}
	}
}

func TestCredentialsLoadWhateverTheNotesSay(t *testing.T) {
	for _, file := range []string{
		`{"api_key":"apk_saved","created_at":""}`,
		`{"api_key":"apk_saved","created_at":"2026-11-10 12:00"}`,
		`{"api_key":"apk_saved","created_at":"2026-11-10T12:00:00.123456+00:00","key_name":"laptop (aperiodic Python)"}`,
	} {
		dir := t.TempDir()
		env := func(key string) string {
			if key == "APERIODIC_CONFIG_DIR" {
				return dir
			}
			return ""
		}
		if err := os.WriteFile(dir+"/credentials", []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		creds, err := LoadCredentials(env)
		if err != nil || creds == nil || creds.APIKey != "apk_saved" {
			t.Errorf("%s: got %+v, %v", file, creds, err)
		}
	}
}

func TestSavingLeavesNoTemporaryFileAndKeepsTheModePrivate(t *testing.T) {
	dir := t.TempDir()
	env := func(key string) string {
		if key == "APERIODIC_CONFIG_DIR" {
			return dir
		}
		return ""
	}
	// A leftover from the old fixed name must not lend the file its mode.
	if err := os.WriteFile(dir+"/credentials.tmp", []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := SaveCredentials(env, Credentials{APIKey: "apk_saved"})
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "credentials-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
}

func TestLogoutSaysWhatTheServerSaid(t *testing.T) {
	fake := &fakeAperiodic{dead: map[string]bool{"apk_gone": true}}

	h := newAuthHarness(t, fake)
	if _, err := SaveCredentials(h.cli.Env, Credentials{APIKey: "11111111-2222-4333-8444-555555555555", KeyName: "default"}); err != nil {
		t.Fatal(err)
	}
	if code := h.cli.Run([]string{"logout"}); code != 0 {
		t.Fatalf("logout exit %d: %s", code, h.stderr)
	}
	if out := h.stdout.String(); !strings.Contains(out, "default key") || !strings.Contains(out, "Rotate an account’s default key at https://aperiodic.io/home/api.") {
		t.Errorf("stdout: %s", out)
	}
	if creds, _ := LoadCredentials(h.cli.Env); creds != nil {
		t.Errorf("credentials still there: %+v", creds)
	}

	h2 := newAuthHarness(t, fake)
	if _, err := SaveCredentials(h2.cli.Env, Credentials{APIKey: "apk_gone", KeyName: "old laptop"}); err != nil {
		t.Fatal(err)
	}
	if code := h2.cli.Run([]string{"logout"}); code != 0 {
		t.Fatalf("logout exit %d: %s", code, h2.stderr)
	}
	if !strings.Contains(h2.stdout.String(), `The key "old laptop" was already revoked`) {
		t.Errorf("stdout: %s", h2.stdout)
	}
}

func TestWhoamiNamesTheDemoKey(t *testing.T) {
	h := newAuthHarness(t, &fakeAperiodic{})
	h.env["APERIODIC_API_KEY"] = "DEMO-KEY"

	if code := h.cli.Run([]string{"whoami"}); code != 0 {
		t.Fatalf("whoami exit %d: %s", code, h.stderr)
	}
	if out := h.stdout.String(); !strings.Contains(out, "the shared demo key") || strings.Contains(out, "default key") {
		t.Errorf("stdout: %s", out)
	}
}

func TestLoginHelpExitsZero(t *testing.T) {
	h := newAuthHarness(t, &fakeAperiodic{})
	for _, flag := range []string{"-h", "--help"} {
		if code := h.cli.Run([]string{"login", flag}); code != 0 {
			t.Errorf("login %s exit %d", flag, code)
		}
	}
	if !strings.Contains(h.stderr.String(), "-force") {
		t.Errorf("usage does not list --force: %s", h.stderr)
	}
}
