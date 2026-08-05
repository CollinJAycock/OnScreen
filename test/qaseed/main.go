// Command qaseed populates an OnScreen server with sample "QA" media libraries
// built from free, legally-clear content (Creative Commons / royalty-free), so
// a reviewer or QA tester can sign in and immediately browse + play real
// titles. This is the answer to the Amazon Appstore "provide test credentials /
// the reviewer can't get past login" rejection: point them at a server seeded
// by this tool with the QA account it creates.
//
// The OnScreen scanner works fully OFFLINE — titles come from the on-disk
// filename/folder layout and playback from ffprobe, with TMDB/artwork strictly
// optional — so well-named free files are enough to get a browsable, playable
// library with NO external metadata keys.
//
// What it does (each step is independently toggleable):
//  1. download — fetch the manifest content into <media-root> in the exact
//     layout the scanner expects (Movies "Title (Year)/Title (Year).mp4",
//     Shows "Show/Season 01/Show S01E01 - Title.mp4").
//  2. user     — (optional) create a non-admin QA/reviewer account via the API.
//  3. libs     — create one library per manifest group (idempotent: reuses a
//     library of the same name if it already exists).
//  4. scan     — trigger a scan of each library.
//
// IMPORTANT: <media-root> must live on a filesystem the SERVER can read — the
// library scan_paths are resolved server-side. Run this on the server host (or
// point -media-root at a server-visible path / share).
//
// Usage:
//
//	go run ./test/qaseed \
//	  -server https://onscreen.example.com \
//	  -admin-user admin -admin-pass '...' \
//	  -media-root /var/onscreen/qa-media \
//	  -qa-user qa_reviewer -qa-pass 'QAPass123!'
//
// Use -dry-run first to print the plan without touching the network or server.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---- Curated free-content manifest -------------------------------------------
//
// All Movies entries are Blender Foundation open movies, licensed CC BY 3.0
// (attribution required — written to CREDITS.txt). Shows entries are Google's
// royalty-free sample clips, arranged as a sample series purely to exercise the
// TV/episode browse path. Hosted on Google's long-lived public sample bucket
// (commondatastorage.googleapis.com/gtv-videos-bucket) for reliable downloads.
//
// To extend: add Items (or a whole Lib of Type "music"/"photo"/etc.) — the
// scanner naming rules are documented in test/qaseed/README.md.

type Item struct {
	Rel    string // path relative to the library's Dir
	URL    string
	Credit string
}

type Lib struct {
	Name  string
	Type  string // movie | show | music | photo | ... (server enum)
	Dir   string // subdir under -media-root; becomes the library scan_path
	Items []Item
}

const bucket = "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample"

var manifest = []Lib{
	{
		Name: "QA Movies", Type: "movie", Dir: "movies",
		Items: []Item{
			{Rel: "Big Buck Bunny (2008)/Big Buck Bunny (2008).mp4", URL: bucket + "/BigBuckBunny.mp4", Credit: "Big Buck Bunny (2008) — (c) Blender Foundation, CC BY 3.0 — peach.blender.org"},
			{Rel: "Elephants Dream (2006)/Elephants Dream (2006).mp4", URL: bucket + "/ElephantsDream.mp4", Credit: "Elephants Dream (2006) — (c) Blender Foundation, CC BY 3.0 — orange.blender.org"},
			{Rel: "Sintel (2010)/Sintel (2010).mp4", URL: bucket + "/Sintel.mp4", Credit: "Sintel (2010) — (c) Blender Foundation, CC BY 3.0 — durian.blender.org"},
			{Rel: "Tears of Steel (2012)/Tears of Steel (2012).mp4", URL: bucket + "/TearsOfSteel.mp4", Credit: "Tears of Steel (2012) — (c) Blender Foundation, CC BY 3.0 — mango.blender.org"},
		},
	},
	{
		Name: "QA Shows", Type: "show", Dir: "shows",
		Items: []Item{
			{Rel: "OnScreen Sample Series/Season 01/OnScreen Sample Series S01E01 - For Bigger Blazes.mp4", URL: bucket + "/ForBiggerBlazes.mp4", Credit: "Google sample media (royalty-free), arranged as a QA sample series"},
			{Rel: "OnScreen Sample Series/Season 01/OnScreen Sample Series S01E02 - For Bigger Escapes.mp4", URL: bucket + "/ForBiggerEscapes.mp4", Credit: "Google sample media (royalty-free), arranged as a QA sample series"},
			{Rel: "OnScreen Sample Series/Season 01/OnScreen Sample Series S01E03 - For Bigger Fun.mp4", URL: bucket + "/ForBiggerFun.mp4", Credit: "Google sample media (royalty-free), arranged as a QA sample series"},
		},
	},
}

// ---- flags -------------------------------------------------------------------

var (
	server     = flag.String("server", "http://localhost:7070", "OnScreen server base URL")
	adminUser  = flag.String("admin-user", "admin", "admin username (used to log in and create libraries)")
	adminPass  = flag.String("admin-pass", "", "admin password")
	token      = flag.String("token", "", "admin access token (skips login if set)")
	bootstrap  = flag.Bool("bootstrap", false, "register -admin-user as the first admin if the server has no users yet")
	mediaRoot  = flag.String("media-root", "qa-media", "directory to download content into (must be readable by the server)")
	scanRoot   = flag.String("scan-root", "", "path the SERVER sees for the content, used for library scan_paths; defaults to -media-root. Set this when -media-root is a share/mount the server reads at a different path (e.g. you download to an SMB mount but the OnScreen container sees it at /media/demo).")
	qaUser     = flag.String("qa-user", "", "if set, create this non-admin QA/reviewer account")
	qaPass     = flag.String("qa-pass", "", "password for -qa-user")
	restrict   = flag.Bool("restrict", true, "restrict -qa-user to ONLY the seeded demo libraries (replaces their entire library-access set)")
	doDownload = flag.Bool("download", true, "download manifest content")
	doLibs     = flag.Bool("libs", true, "create libraries via the API")
	doScan     = flag.Bool("scan", true, "trigger a scan of each library")
	dryRun     = flag.Bool("dry-run", false, "print the plan; touch neither network nor server")
)

func main() {
	flag.Parse()
	*server = strings.TrimRight(*server, "/")
	root, err := filepath.Abs(*mediaRoot)
	must(err)

	fmt.Printf("server     : %s\n", *server)
	fmt.Printf("media-root : %s  (must be readable by the server)\n", root)
	fmt.Printf("libraries  : %d\n\n", len(manifest))

	if *dryRun {
		planOnly(root)
		return
	}

	// 1. download content into the scanner layout.
	if *doDownload {
		for _, lib := range manifest {
			for _, it := range lib.Items {
				dest := filepath.Join(root, lib.Dir, filepath.FromSlash(it.Rel))
				if err := download(it.URL, dest); err != nil {
					fmt.Printf("  ! download failed %s: %v\n", it.Rel, err)
				}
			}
		}
		writeCredits(root)
	}

	// API steps need an admin token.
	needAPI := *doLibs || *doScan || *qaUser != ""
	var tok string
	if needAPI {
		tok, err = adminToken()
		must(err)
	}

	// 2. optional QA/reviewer account.
	if *qaUser != "" {
		if *qaPass == "" {
			fmt.Println("  ! -qa-user set but -qa-pass empty; skipping user creation")
		} else if err := createUser(tok, *qaUser, *qaPass, false); err != nil {
			fmt.Printf("  ! create QA user: %v\n", err)
		} else {
			fmt.Printf("  + QA user ready: %s\n", *qaUser)
		}
	}

	// 3 + 4. create + scan libraries.
	var demoLibIDs []string
	for _, lib := range manifest {
		if !*doLibs {
			break
		}
		scanPath := serverPath(root, lib.Dir)
		id, err := ensureLibrary(tok, lib.Name, lib.Type, scanPath)
		if err != nil {
			fmt.Printf("  ! library %q: %v\n", lib.Name, err)
			continue
		}
		demoLibIDs = append(demoLibIDs, id)
		fmt.Printf("  + library %q (%s) -> %s\n", lib.Name, lib.Type, id)
		if *doScan {
			if err := scan(tok, id); err != nil {
				fmt.Printf("    ! scan: %v\n", err)
			} else {
				fmt.Printf("    + scan queued\n")
			}
		}
	}

	// 5. Restrict the QA user to ONLY the demo libraries. The server is
	// default-deny (a non-admin sees only libraries with an explicit
	// library_access row), so this single REPLACE is the whole isolation
	// story — real libraries stay invisible, and any auto-granted library is
	// removed from the QA user's set.
	if *qaUser != "" && *restrict && len(demoLibIDs) > 0 {
		uid := findUserID(tok, *qaUser)
		if uid == "" {
			fmt.Printf("  ! could not resolve user id for %q; skipping access restriction\n", *qaUser)
		} else if err := restrictUser(tok, uid, demoLibIDs); err != nil {
			fmt.Printf("  ! restrict QA user: %v\n", err)
		} else {
			fmt.Printf("  + QA user %q restricted to %d demo library(ies) only\n", *qaUser, len(demoLibIDs))
		}
	}

	fmt.Println("\nDone. Scans run in the background; libraries appear once each completes.")
	if *qaUser != "" {
		note := ""
		if *restrict {
			note = "  Access    : restricted to the demo libraries only\n"
		}
		fmt.Printf("\nTesting credentials for the app / Amazon review:\n  Server URL: %s\n  Username  : %s\n  Password  : %s\n%s  (ensure this account has TOTP/2FA disabled)\n", *server, *qaUser, *qaPass, note)
	}
}

// ---- API helpers -------------------------------------------------------------

var httpc = &http.Client{Timeout: 30 * time.Minute}

func apiURL(p string) string { return *server + "/api/v1" + p }

// doJSON sends an optional JSON body and decodes the response into out (if
// non-nil). Returns the status code; errors only on transport/encode failures.
func doJSON(method, url, tok string, body, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, out)
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return resp.StatusCode, nil
}

func adminToken() (string, error) {
	if *token != "" {
		return *token, nil
	}
	if *adminPass == "" {
		return "", fmt.Errorf("need -token or -admin-pass")
	}
	if *bootstrap {
		// First-user registration needs no auth; ignore "already exists".
		if err := createUser("", *adminUser, *adminPass, true); err != nil {
			fmt.Printf("  (bootstrap register: %v — continuing to login)\n", err)
		}
	}
	var res struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if _, err := doJSON(http.MethodPost, apiURL("/auth/login"), "",
		map[string]string{"username": *adminUser, "password": *adminPass}, &res); err != nil {
		return "", fmt.Errorf("admin login: %w", err)
	}
	if res.Data.AccessToken == "" {
		return "", fmt.Errorf("admin login returned no access_token")
	}
	return res.Data.AccessToken, nil
}

func createUser(tok, user, pass string, admin bool) error {
	code, err := doJSON(http.MethodPost, apiURL("/auth/register"), tok, map[string]any{
		"username": user, "password": pass, "is_admin": admin,
	}, nil)
	if code == http.StatusConflict || (err != nil && strings.Contains(strings.ToLower(err.Error()), "exist")) {
		return nil // already there — idempotent
	}
	return err
}

func ensureLibrary(tok, name, typ, scanPath string) (string, error) {
	// Idempotent: reuse an existing library with the same name.
	if id := findLibrary(tok, name); id != "" {
		return id, nil
	}
	var res struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_, err := doJSON(http.MethodPost, apiURL("/libraries"), tok, map[string]any{
		"name":       name,
		"type":       typ,
		"scan_paths": []string{scanPath},
		"agent":      "tmdb",
		"language":   "en",
	}, &res)
	if err != nil {
		return "", err
	}
	return res.Data.ID, nil
}

func findLibrary(tok, name string) string {
	var res struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if _, err := doJSON(http.MethodGet, apiURL("/libraries"), tok, nil, &res); err != nil {
		return ""
	}
	for _, l := range res.Data {
		if l.Name == name {
			return l.ID
		}
	}
	return ""
}

func scan(tok, id string) error {
	_, err := doJSON(http.MethodPost, apiURL("/libraries/"+id+"/scan"), tok, nil, nil)
	return err
}

// findUserID resolves a username to its id via the admin user list (works
// whether the user was just created or already existed).
func findUserID(tok, username string) string {
	var res struct {
		Data []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if _, err := doJSON(http.MethodGet, apiURL("/users"), tok, nil, &res); err != nil {
		return ""
	}
	for _, u := range res.Data {
		if u.Username == username {
			return u.ID
		}
	}
	return ""
}

// restrictUser REPLACES a user's accessible-library set with exactly libIDs
// (PUT /users/{id}/libraries). With the server's default-deny model this scopes
// the QA/reviewer account to only the demo libraries.
func restrictUser(tok, userID string, libIDs []string) error {
	_, err := doJSON(http.MethodPut, apiURL("/users/"+userID+"/libraries"), tok,
		map[string]any{"library_ids": libIDs}, nil)
	return err
}

// ---- download + layout -------------------------------------------------------

func download(url, dest string) error {
	if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
		fmt.Printf("  = exists %s\n", rel(dest))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	fmt.Printf("  v downloading %s\n", rel(dest))
	resp, err := httpc.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, dest)
}

func writeCredits(root string) {
	var b strings.Builder
	b.WriteString("OnScreen QA sample content — attributions\n")
	b.WriteString("=========================================\n\n")
	for _, lib := range manifest {
		b.WriteString(lib.Name + " (" + lib.Type + ")\n")
		for _, it := range lib.Items {
			b.WriteString("  - " + it.Credit + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Blender open movies are licensed CC BY 3.0 (https://creativecommons.org/licenses/by/3.0/).\n")
	_ = os.WriteFile(filepath.Join(root, "CREDITS.txt"), []byte(b.String()), 0o644)
}

// ---- misc --------------------------------------------------------------------

func planOnly(root string) {
	fmt.Println("DRY RUN — planned actions:")
	for _, lib := range manifest {
		fmt.Printf("\n  library %q (%s)  scan_path=%s\n", lib.Name, lib.Type, serverPath(root, lib.Dir))
		for _, it := range lib.Items {
			fmt.Printf("    download %s\n             -> %s\n", it.URL, filepath.Join(root, lib.Dir, filepath.FromSlash(it.Rel)))
		}
	}
	if *qaUser != "" {
		fmt.Printf("\n  create QA user %q (non-admin)\n", *qaUser)
		if *restrict {
			fmt.Printf("  restrict %q to ONLY the %d demo library(ies) above (PUT /users/{id}/libraries)\n", *qaUser, len(manifest))
		}
	}
	fmt.Println("\n(no network or server calls were made)")
}

// serverPath is the path the SERVER uses in scan_paths. Defaults to the local
// download path; when -scan-root is set (server reads the content at a
// different mount — e.g. an SMB-download / Linux-container split) it is joined
// with forward slashes, since the server is POSIX.
func serverPath(localRoot, dir string) string {
	if *scanRoot != "" {
		return strings.TrimRight(*scanRoot, "/") + "/" + dir
	}
	return filepath.Join(localRoot, dir)
}

func rel(p string) string {
	if r, err := filepath.Rel(must2(filepath.Abs(*mediaRoot)), p); err == nil {
		return r
	}
	return p
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func must2[T any](v T, err error) T {
	must(err)
	return v
}
