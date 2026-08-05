// onscreen-cli is a headless/terminal OnScreen client that plays through mpv.
//
// It exists for two audiences: people driving a TV from a Raspberry Pi or an
// SSH session, and this repo's own QA — `play --print-url` exercises the full
// login → browse → playback-decision → stream/transcode chain from a shell,
// which makes it the cheapest end-to-end probe of the playback matrix.
//
//	onscreen-cli login https://onscreen.example.com
//	onscreen-cli libraries
//	onscreen-cli ls Movies
//	onscreen-cli search "space odyssey"
//	onscreen-cli play "2001"           # spawns mpv
//	onscreen-cli play <item-uuid> --print-url   # prints URL + mpv args only
//
// Design notes:
//   - The capability profile sent on every request declares what mpv can do
//     (essentially everything), so the server's playback decision almost
//     always lands on direct play and the file streams untouched. A
//     `transcode` verdict (or --transcode) starts a real session and plays
//     the HLS playlist; the session is stopped when mpv exits.
//   - Direct-play auth rides an Authorization header handed to mpv
//     (--http-header-fields) — no token minting, no token-in-URL for the
//     direct path. Transcode playlists carry their own session token, the
//     same contract every other client uses.
//   - Progress reporting uses mpv's JSON IPC over a unix socket and is
//     therefore non-Windows only; without it playback still works, resume
//     just isn't recorded. Windows named-pipe IPC is a wontfix until someone
//     asks — the CLI's audience is overwhelmingly Linux.
//   - Credentials prompt on the terminal (no echo for the password) and can
//     be supplied via ONSCREEN_USERNAME / ONSCREEN_PASSWORD for scripted QA.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

// mpv decodes effectively every codec/container OnScreen serves, so the
// profile claims the works: the server should hand us the source whenever the
// business rules (HDR policy, resolution caps, DV refusal) allow it.
const mpvCapabilities = "videoDecoder=h264:h265:av1:vp9,audioDecoder=aac:ac3:eac3:dts:truehd:flac:mp3:opus:vorbis:pcm,protocols=mp4:mkv:hls,maxWidth=7680,maxHeight=4320,maxAudioChannels=8,maxbitdepth=12,hdr=1"

const clientName = "onscreen-cli"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "login":
		err = cmdLogin(args)
	case "logout":
		err = cmdLogout()
	case "status":
		err = cmdStatus()
	case "libraries":
		err = cmdLibraries()
	case "ls":
		err = cmdLs(args)
	case "search":
		err = cmdSearch(args)
	case "play":
		err = cmdPlay(args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `onscreen-cli — terminal OnScreen client (plays via mpv)

  login <server-url>     sign in (ONSCREEN_USERNAME/ONSCREEN_PASSWORD skip prompts)
  logout                 forget the stored session
  status                 show server + signed-in user
  libraries              list libraries
  ls <library>           list items (library id or name)  [--limit N]
  search <query>         search across libraries
  play <item|query>      play through mpv  [--transcode] [--height N] [--print-url]
`)
}

// ── config ───────────────────────────────────────────────────────────────────

type config struct {
	Server       string `json:"server"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Username     string `json:"username"`
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".onscreen", "cli.json"), nil
}

func loadConfig() (*config, error) {
	p, err := configPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("not signed in — run: onscreen-cli login <server-url>")
		}
		return nil, err
	}
	var c config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &c, nil
}

func (c *config) save() error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0600)
}

// ── API client ───────────────────────────────────────────────────────────────

type client struct {
	cfg  *config
	http *http.Client
}

func newClient() (*client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return &client{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.msg, e.status)
	}
	return fmt.Sprintf("HTTP %d", e.status)
}

// request performs an authenticated API call, refreshing the access token
// once on a 401 — the same contract every first-party client implements.
func (c *client) request(method, path string, body, out any) error {
	err := c.do(method, path, body, out, true)
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusUnauthorized && c.cfg.RefreshToken != "" {
		if rerr := c.refresh(); rerr == nil {
			return c.do(method, path, body, out, true)
		}
	}
	return err
}

func (c *client) do(method, path string, body, out any, auth bool) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.cfg.Server+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Client-Capabilities", mpvCapabilities)
	if auth && c.cfg.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &apiError{status: resp.StatusCode, code: e.Error.Code, msg: e.Error.Message}
	}
	if out == nil {
		return nil
	}
	// API success shape: {"data": ...}; unwrap for the caller.
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Data != nil {
		return json.Unmarshal(envelope.Data, out)
	}
	return json.Unmarshal(raw, out)
}

func (c *client) refresh() error {
	var pair struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	err := c.do("POST", "/api/v1/auth/refresh",
		map[string]string{"refresh_token": c.cfg.RefreshToken}, &pair, false)
	if err != nil {
		return err
	}
	c.cfg.AccessToken = pair.AccessToken
	if pair.RefreshToken != "" {
		c.cfg.RefreshToken = pair.RefreshToken
	}
	return c.cfg.save()
}

// ── commands ─────────────────────────────────────────────────────────────────

func cmdLogin(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: onscreen-cli login <server-url>")
	}
	server := strings.TrimRight(args[0], "/")
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		server = "https://" + server
	}

	username := os.Getenv("ONSCREEN_USERNAME")
	password := os.Getenv("ONSCREEN_PASSWORD")
	rd := bufio.NewReader(os.Stdin)
	if username == "" {
		fmt.Print("username: ")
		line, err := rd.ReadString('\n')
		if err != nil {
			return err
		}
		username = strings.TrimSpace(line)
	}
	if password == "" {
		fmt.Print("password: ")
		if term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			password = string(b)
		} else {
			line, err := rd.ReadString('\n')
			if err != nil {
				return err
			}
			password = strings.TrimSpace(line)
		}
	}

	c := &client{cfg: &config{Server: server}, http: &http.Client{Timeout: 30 * time.Second}}
	var pair struct {
		AccessToken         string `json:"access_token"`
		RefreshToken        string `json:"refresh_token"`
		TotpRequired        bool   `json:"totp_required"`
		LoginChallengeToken string `json:"login_challenge_token"`
	}
	if err := c.do("POST", "/api/v1/auth/login",
		map[string]string{"username": username, "password": password}, &pair, false); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if pair.TotpRequired {
		fmt.Print("2FA code: ")
		line, err := rd.ReadString('\n')
		if err != nil {
			return err
		}
		code := strings.TrimSpace(line)
		if err := c.do("POST", "/api/v1/auth/totp/verify",
			map[string]string{"challenge_token": pair.LoginChallengeToken, "code": code}, &pair, false); err != nil {
			return fmt.Errorf("2fa verify: %w", err)
		}
	}
	cfg := &config{Server: server, AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, Username: username}
	if err := cfg.save(); err != nil {
		return err
	}
	fmt.Printf("signed in to %s as %s\n", server, username)
	return nil
}

func cmdLogout() error {
	c, err := newClient()
	if err != nil {
		return err
	}
	// Best-effort server-side revocation; always forget local state.
	_ = c.request("POST", "/api/v1/auth/logout",
		map[string]string{"refresh_token": c.cfg.RefreshToken}, nil)
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("signed out")
	return nil
}

func cmdStatus() error {
	c, err := newClient()
	if err != nil {
		return err
	}
	if err := c.request("GET", "/api/v1/users/me/preferences", nil, &json.RawMessage{}); err != nil {
		return fmt.Errorf("server unreachable or session expired: %w", err)
	}
	fmt.Printf("server: %s\nuser:   %s\nauth:   ok\n", c.cfg.Server, c.cfg.Username)
	return nil
}

type library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func cmdLibraries() error {
	c, err := newClient()
	if err != nil {
		return err
	}
	var libs []library
	if err := c.request("GET", "/api/v1/libraries", nil, &libs); err != nil {
		return err
	}
	for _, l := range libs {
		fmt.Printf("%s  %-10s  %s\n", l.ID, l.Type, l.Name)
	}
	return nil
}

type item struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Year  *int   `json:"year"`
	Type  string `json:"type"`
}

func (i item) label() string {
	if i.Year != nil {
		return fmt.Sprintf("%s (%d)", i.Title, *i.Year)
	}
	return i.Title
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	limit := fs.Int("limit", 50, "max items")
	parseFlagsAnywhere(fs, args, "limit")
	if fs.NArg() < 1 {
		return errors.New("usage: onscreen-cli ls <library-id-or-name>")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	var libs []library
	if err := c.request("GET", "/api/v1/libraries", nil, &libs); err != nil {
		return err
	}
	want := strings.ToLower(fs.Arg(0))
	var lib *library
	for i := range libs {
		if libs[i].ID == fs.Arg(0) || strings.ToLower(libs[i].Name) == want {
			lib = &libs[i]
			break
		}
	}
	if lib == nil {
		return fmt.Errorf("no library matching %q (try: onscreen-cli libraries)", fs.Arg(0))
	}
	var items []item
	if err := c.request("GET",
		fmt.Sprintf("/api/v1/libraries/%s/items?limit=%d", lib.ID, *limit), nil, &items); err != nil {
		return err
	}
	for _, it := range items {
		fmt.Printf("%s  %s\n", it.ID, it.label())
	}
	return nil
}

func (c *client) search(q string) ([]item, error) {
	// The search payload is a bare array today; tolerate an {items: [...]}
	// wrapper too so a future envelope change doesn't break the CLI.
	var raw json.RawMessage
	if err := c.request("GET", "/api/v1/search?q="+url.QueryEscape(q), nil, &raw); err != nil {
		return nil, err
	}
	var flat []item
	if json.Unmarshal(raw, &flat) == nil {
		return flat, nil
	}
	var wrapped struct {
		Items []item `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("unexpected search payload: %w", err)
	}
	return wrapped.Items, nil
}

func cmdSearch(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: onscreen-cli search <query>")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	items, err := c.search(strings.Join(args, " "))
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("no results")
		return nil
	}
	for _, it := range items {
		fmt.Printf("%s  %-8s %s\n", it.ID, it.Type, it.label())
	}
	return nil
}

// ── play ─────────────────────────────────────────────────────────────────────

type itemFile struct {
	ID         string `json:"id"`
	DurationMs *int64 `json:"duration_ms"`
}

type itemDetail struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Files []itemFile `json:"files"`
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
	}
	return true
}

// parseFlagsAnywhere lets flags follow positionals (`play "query" --print-url`)
// — Go's flag package stops at the first non-flag, which silently swallowed
// trailing flags into the search query. Value-taking flags are listed so their
// separated form (`--height 720`) stays attached.
func parseFlagsAnywhere(fs *flag.FlagSet, args []string, valueFlags ...string) {
	takesValue := map[string]bool{}
	for _, v := range valueFlags {
		takesValue["-"+v], takesValue["--"+v] = true, true
	}
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if takesValue[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, a)
		}
	}
	_ = fs.Parse(append(flags, pos...))
}

func cmdPlay(args []string) error {
	fs := flag.NewFlagSet("play", flag.ExitOnError)
	forceTranscode := fs.Bool("transcode", false, "force a transcode session instead of direct play")
	height := fs.Int("height", 0, "transcode height cap (0 = source)")
	printOnly := fs.Bool("print-url", false, "print the resolved URL + mpv invocation instead of playing")
	parseFlagsAnywhere(fs, args, "height")
	if fs.NArg() < 1 {
		return errors.New("usage: onscreen-cli play <item-id-or-query>")
	}
	c, err := newClient()
	if err != nil {
		return err
	}

	// Resolve the target: a UUID plays directly, anything else is a search
	// whose first video hit wins (printed, so a wrong guess is obvious).
	target := strings.Join(fs.Args(), " ")
	if !looksLikeUUID(target) {
		hits, err := c.search(target)
		if err != nil {
			return err
		}
		var pick *item
		for i := range hits {
			switch hits[i].Type {
			case "movie", "episode", "video", "home_video", "anime", "cartoons":
				pick = &hits[i]
			}
			if pick != nil {
				break
			}
		}
		if pick == nil && len(hits) > 0 {
			pick = &hits[0]
		}
		if pick == nil {
			return fmt.Errorf("nothing found for %q", target)
		}
		fmt.Printf("playing: %s\n", pick.label())
		target = pick.ID
	}

	var detail itemDetail
	if err := c.request("GET", "/api/v1/items/"+target, nil, &detail); err != nil {
		return err
	}
	if len(detail.Files) == 0 {
		return errors.New("item has no playable files")
	}
	file := detail.Files[0]

	// Server-authoritative verdict with the mpv capability profile.
	var verdict struct {
		Decision string `json:"decision"`
	}
	if err := c.request("POST", "/api/v1/items/"+target+"/playback-decision",
		map[string]string{"file_id": file.ID}, &verdict); err != nil {
		return err
	}
	if verdict.Decision == "unsupported" {
		return errors.New("the server refuses this file (e.g. Dolby Vision) — no playable path")
	}

	mpvArgs := []string{"--force-media-title=" + detail.Title}
	var streamURL string
	var stopSession func()
	if *forceTranscode || verdict.Decision == "transcode" {
		body := map[string]any{
			"file_id": file.ID, "height": *height,
			"supports_hevc": true, "supports_av1": true,
		}
		var sess struct {
			SessionID   string `json:"session_id"`
			PlaylistURL string `json:"playlist_url"`
			Token       string `json:"token"`
		}
		if err := c.request("POST", "/api/v1/items/"+target+"/transcode", body, &sess); err != nil {
			return err
		}
		streamURL = c.cfg.Server + sess.PlaylistURL
		stopSession = func() {
			_ = c.request("DELETE",
				"/api/v1/transcode/sessions/"+sess.SessionID+"?token="+url.QueryEscape(sess.Token), nil, nil)
		}
		fmt.Printf("transcode session %s (%s)\n", sess.SessionID, verdict.Decision)
	} else {
		// Direct play: mpv fetches the source with our Bearer attached.
		streamURL = c.cfg.Server + "/media/stream/" + file.ID
		mpvArgs = append(mpvArgs, "--http-header-fields=Authorization: Bearer "+c.cfg.AccessToken)
		fmt.Printf("direct play (%s)\n", verdict.Decision)
	}
	mpvArgs = append(mpvArgs, streamURL)

	if *printOnly {
		fmt.Println("url:", streamURL)
		fmt.Println("mpv", strings.Join(mpvArgs, " "))
		if stopSession != nil {
			stopSession()
		}
		return nil
	}

	mpvPath, err := exec.LookPath("mpv")
	if err != nil {
		if stopSession != nil {
			stopSession()
		}
		return errors.New("mpv not found on PATH — install mpv, or use --print-url")
	}

	// Progress IPC (non-Windows): poll time-pos and report resume position.
	var ipcSock string
	if runtime.GOOS != "windows" {
		ipcSock = filepath.Join(os.TempDir(), fmt.Sprintf("onscreen-mpv-%d.sock", os.Getpid()))
		mpvArgs = append([]string{"--input-ipc-server=" + ipcSock}, mpvArgs...)
	}

	cmd := exec.CommandContext(context.Background(), mpvPath, mpvArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		if stopSession != nil {
			stopSession()
		}
		return err
	}

	durationMs := int64(0)
	if file.DurationMs != nil {
		durationMs = *file.DurationMs
	}
	stopProgress := make(chan struct{})
	if ipcSock != "" {
		go reportProgress(c, target, ipcSock, durationMs, stopProgress)
	}

	// Ctrl-C tears mpv down; mpv exiting resolves Wait either way.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		_ = cmd.Process.Kill()
	}()

	waitErr := cmd.Wait()
	close(stopProgress)
	if stopSession != nil {
		stopSession()
	}
	if waitErr != nil && !errors.Is(waitErr, exec.ErrNotFound) {
		// mpv's non-zero exit on user quit is normal; only surface real spawn
		// failures (already handled above). Treat exit codes as success.
		return nil
	}
	return nil
}

// reportProgress polls mpv's JSON IPC for time-pos and PUTs watch progress,
// so a CLI viewing session resumes on any other client. Best-effort: every
// error is silent — progress must never take playback down with it.
func reportProgress(c *client, itemID, sock string, durationMs int64, stop <-chan struct{}) {
	// mpv needs a beat to create the socket.
	var conn net.Conn
	dialer := &net.Dialer{}
	for i := 0; i < 20; i++ {
		var err error
		conn, err = dialer.DialContext(context.Background(), "unix", sock)
		if err == nil {
			break
		}
		select {
		case <-stop:
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	if conn == nil {
		return
	}
	defer conn.Close()
	rd := bufio.NewReader(conn)

	lastMs := int64(-1)
	send := func(state string) {
		if lastMs < 0 {
			return
		}
		_ = c.request("PUT", "/api/v1/items/"+itemID+"/progress", map[string]any{
			"view_offset_ms": lastMs,
			"duration_ms":    durationMs,
			"state":          state,
			"client_name":    clientName,
		}, nil)
	}
	defer send("stopped")

	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			_, err := conn.Write([]byte(`{"command":["get_property","time-pos"]}` + "\n"))
			if err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			var resp struct {
				Data  float64 `json:"data"`
				Error string  `json:"error"`
			}
			if json.Unmarshal([]byte(line), &resp) == nil && resp.Error == "success" {
				lastMs = int64(resp.Data * 1000)
				send("playing")
			}
		}
	}
}
