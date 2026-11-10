package aperiodic

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Signing in: `aperiodic login` gets this machine a named key of its own
// through aperiodic.io/device, on a laptop or a remote machine alike, and
// saves it (credentials.go). The server side is /api/v1/cli/*.

type loginStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type loginToken struct {
	APIKey           string `json:"api_key"`
	KeyName          string `json:"key_name"`
	Plan             string `json:"plan"`
	PlanName         string `json:"plan_name"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type whoami struct {
	Plan     string `json:"plan"`
	PlanName string `json:"plan_name"`
	Key      struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Hint string `json:"hint"`
	} `json:"key"`
	Covers struct {
		DatasetsInFull int      `json:"datasets_in_full"`
		DatasetsTotal  int      `json:"datasets_total"`
		History        string   `json:"history"`
		Venues         []string `json:"venues"`
		Markets        string   `json:"markets"`
		Live           string   `json:"live"`
	} `json:"covers"`
}

func (c *CLI) baseURL() string {
	if url := c.Env("APERIODIC_API_URL"); url != "" {
		return strings.TrimRight(url, "/")
	}
	return DefaultBaseURL
}

func (c *CLI) request(method, path, apiKey string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL()+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "aperiodic-cli")
	if apiKey != "" {
		req.Header.Set("X-API-KEY", apiKey)
	}
	if id, secret := c.Env("CF_ACCESS_CLIENT_ID"), c.Env("CF_ACCESS_CLIENT_SECRET"); id != "" && secret != "" {
		req.Header.Set("CF-Access-Client-Id", id)
		req.Header.Set("CF-Access-Client-Secret", secret)
	}
	return c.httpClient().Do(req)
}

func (c *CLI) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// machineName names the key after this machine, so it is easy to find and
// revoke on the API page.
func (c *CLI) machineName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "this computer"
	}
	return host + " (aperiodic CLI)"
}

// canOpenBrowser is false over SSH and on a Linux machine with no display,
// where the link is for another device.
func (c *CLI) canOpenBrowser() bool {
	if c.Env("SSH_CONNECTION") != "" || c.Env("SSH_TTY") != "" {
		return false
	}
	if runtime.GOOS == "linux" && c.Env("DISPLAY") == "" && c.Env("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func (c *CLI) runLogin(args []string) int {
	fs := flag.NewFlagSet("aperiodic login", flag.ContinueOnError)
	fs.SetOutput(c.Stderr)
	noBrowser := fs.Bool("no-browser", false, "Print the link instead of opening a browser")
	name := fs.String("name", "", "Name for this machine's key (default: the host name)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	keyName := strings.TrimSpace(*name)
	if keyName == "" {
		keyName = c.machineName()
	}

	resp, err := c.request(http.MethodPost, "/cli/login", "", map[string]string{"name": keyName})
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error: could not reach Aperiodic: %v\n", err)
		return 1
	}
	var start loginStart
	err = decodeJSON(resp, &start)
	if err != nil || start.DeviceCode == "" {
		fmt.Fprintf(c.Stderr, "Error: could not start the login: %v\n", errOr(err, "no code in the answer"))
		return 1
	}

	opened := false
	if !*noBrowser && c.canOpenBrowser() {
		opened = c.OpenURL(start.VerificationURIComplete) == nil
	}
	if opened {
		fmt.Fprintln(c.Stdout, "Opened your browser to sign in. If it didn't open, go to:")
	} else {
		fmt.Fprintln(c.Stdout, "To sign in, open this link on any device:")
	}
	fmt.Fprintf(c.Stdout, "\n  %s\n\n", start.VerificationURIComplete)
	fmt.Fprintf(c.Stdout, "and check it shows the code %s.\n", start.UserCode)
	fmt.Fprintln(c.Stdout, "Waiting for you to allow it…")

	interval := time.Duration(max(start.Interval, 1)) * time.Second
	deadline := c.now().Add(time.Duration(max(start.ExpiresIn, 60)) * time.Second)
	for c.now().Before(deadline) {
		c.sleep(interval)
		resp, err := c.request(http.MethodPost, "/cli/token", "", map[string]string{"device_code": start.DeviceCode})
		if err != nil {
			// A dropped connection is not an answer: try again next round.
			continue
		}
		var token loginToken
		if err := decodeJSONAnyStatus(resp, &token); err != nil {
			continue
		}
		switch token.Error {
		case "":
			return c.finishLogin(token)
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			fmt.Fprintln(c.Stderr, "Error: the login was denied in the browser.")
			return 1
		case "expired_token":
			fmt.Fprintln(c.Stderr, "Error: the code expired. Run 'aperiodic login' again.")
			return 1
		default:
			fmt.Fprintf(c.Stderr, "Error: %s\n", errOr(errors.New(token.ErrorDescription), token.Error))
			return 1
		}
	}
	fmt.Fprintln(c.Stderr, "Error: the code expired. Run 'aperiodic login' again.")
	return 1
}

func (c *CLI) finishLogin(token loginToken) int {
	if token.APIKey == "" {
		fmt.Fprintln(c.Stderr, "Error: the login finished without a key. Run 'aperiodic login' again.")
		return 1
	}
	path, err := SaveCredentials(c.Env, Credentials{
		APIKey:    token.APIKey,
		KeyName:   token.KeyName,
		Plan:      token.Plan,
		PlanName:  token.PlanName,
		CreatedAt: c.now().UTC(),
	})
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error: signed in, but the key could not be saved: %v\n", err)
		return 1
	}
	fmt.Fprintf(c.Stdout, "Signed in on %s. This machine's key, %q, is saved in %s.\n", token.PlanName, token.KeyName, path)
	if c.Env("APERIODIC_API_KEY") != "" {
		fmt.Fprintln(c.Stdout, "Note: APERIODIC_API_KEY is set in this shell, and it is used before the saved key.")
	}
	return 0
}

func (c *CLI) runLogout(args []string) int {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Fprintln(c.Stdout, "Usage: aperiodic logout\n\nRevokes this machine's key and removes it from this machine.")
		return 0
	}
	creds, err := LoadCredentials(c.Env)
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error: %v\n", err)
		return 1
	}
	if creds == nil {
		fmt.Fprintln(c.Stdout, "Not signed in on this machine.")
		return 0
	}

	revoked := false
	if resp, err := c.request(http.MethodDelete, "/cli/key", creds.APIKey, nil); err == nil {
		var answer struct {
			Revoked bool `json:"revoked"`
		}
		revoked = decodeJSON(resp, &answer) == nil && answer.Revoked
	}
	if err := DeleteCredentials(c.Env); err != nil {
		fmt.Fprintf(c.Stderr, "Error: %v\n", err)
		return 1
	}
	if revoked {
		fmt.Fprintf(c.Stdout, "Signed out. The key %q is revoked and removed from this machine.\n", creds.KeyName)
	} else {
		fmt.Fprintf(c.Stdout, "Removed the key from this machine. It could not be revoked from here: revoke %q at https://aperiodic.io/home/api.\n", creds.KeyName)
	}
	return 0
}

func (c *CLI) runWhoami(args []string) int {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Fprintln(c.Stdout, "Usage: aperiodic whoami\n\nShows which key this machine uses and what its plan covers.")
		return 0
	}
	apiKey, source, ok := c.findAPIKey()
	if !ok {
		fmt.Fprintln(c.Stderr, missingKeyMessage)
		return 1
	}
	resp, err := c.request(http.MethodGet, "/cli/whoami", apiKey, nil)
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error: could not reach Aperiodic: %v\n", err)
		return 1
	}
	var me whoami
	if err := decodeJSON(resp, &me); err != nil {
		fmt.Fprintf(c.Stderr, "Error: %v\n", err)
		return 1
	}

	key := "your account's default key"
	if me.Key.Kind == "named" {
		key = fmt.Sprintf("%q (apk_…%s)", me.Key.Name, me.Key.Hint)
	}
	fmt.Fprintf(c.Stdout, "Plan:     %s\n", me.PlanName)
	fmt.Fprintf(c.Stdout, "Key:      %s, from %s\n", key, source)
	fmt.Fprintf(c.Stdout, "Datasets: %d of %d in full\n", me.Covers.DatasetsInFull, me.Covers.DatasetsTotal)
	if me.Covers.Markets != "" {
		fmt.Fprintf(c.Stdout, "Markets:  %s\n", me.Covers.Markets)
	}
	if me.Covers.History != "" {
		fmt.Fprintf(c.Stdout, "History:  %s\n", me.Covers.History)
	}
	if me.Covers.Live != "" {
		fmt.Fprintf(c.Stdout, "Live:     %s\n", me.Covers.Live)
	}
	return 0
}

const missingKeyMessage = "Error: no API key. Run 'aperiodic login', or set APERIODIC_API_KEY."

// findAPIKey looks for a key the way every command does: APERIODIC_API_KEY
// first, then the key `aperiodic login` saved.
func (c *CLI) findAPIKey() (key, source string, ok bool) {
	if apiKey := c.Env("APERIODIC_API_KEY"); apiKey != "" {
		return apiKey, "APERIODIC_API_KEY", true
	}
	creds, err := LoadCredentials(c.Env)
	if err != nil {
		fmt.Fprintf(c.Stderr, "Warning: %v\n", err)
		return "", "", false
	}
	if creds != nil {
		path, _ := CredentialsPath(c.Env)
		return creds.APIKey, path, true
	}
	return "", "", false
}

func decodeJSON(resp *http.Response, into any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var answer struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&answer)
		message := answer.ErrorDescription
		if message == "" {
			message = answer.Error
		}
		if message == "" {
			message = resp.Status
		}
		return errors.New(message)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// decodeJSONAnyStatus reads a token poll, whose 400s carry the answer.
func decodeJSONAnyStatus(resp *http.Response, into any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(into)
}

func errOr(err error, fallback string) string {
	if err != nil && err.Error() != "" {
		return err.Error()
	}
	return fallback
}
