// Load test for OnScreen API server.
//
// Usage:
//
//	go run ./test/loadtest/ -base http://localhost:7070 -user admin -pass yourpassword
//	go run ./test/loadtest/ -base http://localhost:7070 -user admin -pass yourpassword -duration 60s -concurrency 20
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	baseURL     = flag.String("base", "http://localhost:7070", "server base URL")
	username    = flag.String("user", "admin", "login username")
	password    = flag.String("pass", "", "login password")
	duration    = flag.Duration("duration", 30*time.Second, "test duration")
	concurrency = flag.Int("concurrency", 10, "concurrent workers per endpoint")
	warmup      = flag.Duration("warmup", 2*time.Second, "warmup before measuring")
	mode        = flag.String("mode", "api", "test mode: api or transcode")
	sessions    = flag.Int("sessions", 4, "concurrent transcode sessions (transcode mode)")
	interval    = flag.Duration("interval", 30*time.Second, "time between new viewers (transcode mode)")
	watchDur    = flag.Duration("watch", 5*time.Minute, "how long each viewer watches (transcode mode)")
)

// result captures a single request outcome.
type result struct {
	endpoint  string
	status    int
	latency   time.Duration
	err       error
	bodyBytes int64
}

// stats aggregates results for one endpoint.
type stats struct {
	name        string
	total       int
	success     int
	throttled   int // 429 rate-limited
	fail        int
	latencies   []time.Duration
	okLatencies []time.Duration // only non-429 successes
	totalBytes  int64
}

func (s *stats) add(r result) {
	s.total++
	if r.err != nil {
		s.fail++
	} else if r.status == 429 {
		s.throttled++
	} else if r.status >= 400 {
		s.fail++
	} else {
		s.success++
		s.okLatencies = append(s.okLatencies, r.latency)
	}
	s.latencies = append(s.latencies, r.latency)
	s.totalBytes += r.bodyBytes
}

// pct returns the p-th percentile from a pre-sorted duration slice.
func pct(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

// endpoint defines a load test target.
type endpoint struct {
	name   string
	method string
	path   string
	body   any  // JSON body for POST/PUT, nil for GET
	admin  bool // requires admin token
}

// tokenPool holds multiple access tokens, each with its own rate-limit budget.
// Workers draw tokens round-robin so no single session exceeds its 1000 req/min limit.
type tokenPool struct {
	tokens []string
	idx    uint64
}

func (tp *tokenPool) next() string {
	i := atomic.AddUint64(&tp.idx, 1)
	return tp.tokens[i%uint64(len(tp.tokens))]
}

func main() {
	flag.Parse()
	if *password == "" {
		fmt.Fprintln(os.Stderr, "ERROR: -pass is required")
		flag.Usage()
		os.Exit(1)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 200,
			MaxConnsPerHost:     200,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Login once to get the initial token pair.
	accessToken, refreshToken, err := login(client, *baseURL, *username, *password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		os.Exit(1)
	}

	// Transcode mode — spin up concurrent HLS sessions.
	if *mode == "transcode" {
		runTranscodeLoadTest(client, *baseURL, accessToken, *sessions, *duration)
		return
	}

	// ── API Load Test ─────────────────────────────────────────────────────
	fmt.Println("=== OnScreen Load Test ===")
	fmt.Printf("Target:      %s\n", *baseURL)
	fmt.Printf("Duration:    %s\n", *duration)
	fmt.Printf("Concurrency: %d per endpoint\n", *concurrency)
	fmt.Println()
	fmt.Println("Authenticated successfully")

	// ── Build token pool ──────────────────────────────────────────────────
	// Rate limit is 1000 req/min per session. Figure out how many tokens we
	// need so that each token stays under budget.
	//
	// Rough estimate: each worker does ~200 req/s (5ms latency).
	// Total workers = concurrency * numEndpoints (up to ~15).
	// Requests per token per minute = (total_rps / num_tokens) * 60.
	// We want that < 1000, so num_tokens >= total_rps * 60 / 1000.
	//
	// Conservative: one token per worker. Auth rate limit is 10/min on login,
	// but /auth/refresh has the session rate limit. We use refresh to mint
	// new tokens — each refresh creates a distinct session hash.
	numEndpoints := 15 // approximate
	totalWorkers := (*concurrency) * numEndpoints
	// Each token budget = 1000 req/min ≈ 16.7 req/s.
	// We need enough tokens so totalWorkers * ~200 req/s / numTokens < 16.7.
	// => numTokens >= totalWorkers * 200 / 16.7 ≈ totalWorkers * 12
	// But that's way too many refreshes. Instead: one token per 16 req/s budget.
	// At 10 concurrency * 15 endpoints = 150 workers * ~200 req/s = 30000 req/s total.
	// 30000 / 16.7 = ~1800 tokens needed. That's too many refreshes.
	//
	// Practical approach: mint a reasonable number of tokens (one per worker group)
	// and accept that the rate limiter will cap throughput per-token to ~16 req/s.
	// With enough tokens, aggregate throughput = numTokens * 16.7 req/s.
	numTokens := totalWorkers
	if numTokens > 200 {
		numTokens = 200 // cap to avoid hammering refresh
	}
	if numTokens < 1 {
		numTokens = 1
	}

	fmt.Printf("Minting %d session tokens (rate limit: 1000 req/min each)...\n", numTokens)

	pool := &tokenPool{tokens: make([]string, 0, numTokens)}
	pool.tokens = append(pool.tokens, accessToken)

	// Mint additional tokens via /auth/refresh. Each refresh returns a new
	// access_token with a unique session hash → independent rate-limit bucket.
	// We stagger refreshes with a small delay to avoid hitting the auth rate limiter.
	for i := 1; i < numTokens; i++ {
		newAccess, newRefresh, err := refresh(client, *baseURL, refreshToken)
		if err != nil {
			fmt.Printf("  Warning: refresh #%d failed (%v), using %d tokens\n", i, err, len(pool.tokens))
			break
		}
		pool.tokens = append(pool.tokens, newAccess)
		refreshToken = newRefresh
		if i%20 == 0 {
			fmt.Printf("  ...minted %d tokens\n", i)
		}
	}
	fmt.Printf("Token pool: %d tokens (aggregate budget: %d req/min)\n\n",
		len(pool.tokens), len(pool.tokens)*1000)

	// ── Discover content for realistic requests ───────────────────────────
	libraryID := discoverLibrary(client, *baseURL, accessToken)
	itemID := discoverItem(client, *baseURL, accessToken, libraryID)

	fmt.Printf("Library:     %s\n", libraryID)
	fmt.Printf("Item:        %s\n", itemID)
	fmt.Println()

	// ── Define endpoints to test ──────────────────────────────────────────
	endpoints := []endpoint{
		{name: "GET /health/live", method: "GET", path: "/health/live"},
		{name: "GET /hub", method: "GET", path: "/api/v1/hub"},
		{name: "GET /libraries", method: "GET", path: "/api/v1/libraries"},
		{name: "GET /search?q=the", method: "GET", path: "/api/v1/search?q=the"},
		{name: "GET /history", method: "GET", path: "/api/v1/history"},
		{name: "GET /settings", method: "GET", path: "/api/v1/settings", admin: true},
		{name: "GET /settings/fleet", method: "GET", path: "/api/v1/settings/fleet", admin: true},
		{name: "GET /settings/encoders", method: "GET", path: "/api/v1/settings/encoders", admin: true},
		{name: "GET /notifications", method: "GET", path: "/api/v1/notifications"},
		{name: "GET /notifications/unread", method: "GET", path: "/api/v1/notifications/unread-count"},
	}

	if libraryID != "" {
		endpoints = append(endpoints,
			endpoint{name: "GET /libraries/{id}", method: "GET", path: "/api/v1/libraries/" + libraryID},
			endpoint{name: "GET /libraries/{id}/items", method: "GET", path: "/api/v1/libraries/" + libraryID + "/items"},
			endpoint{name: "GET /libraries/{id}/genres", method: "GET", path: "/api/v1/libraries/" + libraryID + "/genres"},
		)
	}
	if itemID != "" {
		endpoints = append(endpoints,
			endpoint{name: "GET /items/{id}", method: "GET", path: "/api/v1/items/" + itemID},
			endpoint{name: "GET /items/{id}/children", method: "GET", path: "/api/v1/items/" + itemID + "/children"},
		)
	}

	// ── Run load test ─────────────────────────────────────────────────────
	fmt.Printf("Testing %d endpoints for %s...\n\n", len(endpoints), *duration)

	var (
		mu       sync.Mutex
		allStats = make(map[string]*stats)
		wg       sync.WaitGroup
		stop     int32
		reqCount int64
	)

	// Init stats.
	for _, ep := range endpoints {
		allStats[ep.name] = &stats{name: ep.name}
	}

	// Warmup — one request per endpoint.
	fmt.Print("Warming up...")
	for _, ep := range endpoints {
		doRequest(client, *baseURL, ep, accessToken)
	}
	time.Sleep(*warmup)
	fmt.Println(" done")

	start := time.Now()

	// Launch workers — each picks a token from the pool round-robin.
	for _, ep := range endpoints {
		for i := 0; i < *concurrency; i++ {
			wg.Add(1)
			go func(ep endpoint) {
				defer wg.Done()
				for atomic.LoadInt32(&stop) == 0 {
					tok := pool.next()
					r := doRequest(client, *baseURL, ep, tok)
					atomic.AddInt64(&reqCount, 1)

					mu.Lock()
					allStats[ep.name].add(r)
					mu.Unlock()
				}
			}(ep)
		}
	}

	// Progress ticker.
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			if atomic.LoadInt32(&stop) != 0 {
				return
			}
			elapsed := time.Since(start).Truncate(time.Second)
			count := atomic.LoadInt64(&reqCount)
			rps := float64(count) / time.Since(start).Seconds()
			fmt.Printf("  [%s] %d requests (%.0f req/s)\n", elapsed, count, rps)
		}
	}()

	// Wait for duration.
	time.Sleep(*duration)
	atomic.StoreInt32(&stop, 1)
	ticker.Stop()
	wg.Wait()
	elapsed := time.Since(start)

	// ── Report ────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println(strings.Repeat("=", 130))
	fmt.Printf("%-35s %8s %8s %8s %8s %10s %10s %10s %10s\n",
		"ENDPOINT", "TOTAL", "OK", "429", "ERR", "AVG(ok)", "P50(ok)", "P95(ok)", "P99(ok)")
	fmt.Println(strings.Repeat("-", 130))

	var totalReqs, totalOK, totalThrottled, totalFail int
	sorted := make([]*stats, 0, len(allStats))
	for _, s := range allStats {
		sorted = append(sorted, s)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })

	for _, s := range sorted {
		totalReqs += s.total
		totalOK += s.success
		totalThrottled += s.throttled
		totalFail += s.fail
		// Show latency stats for successful (non-429) requests only.
		avgOK, p50OK, p95OK, p99OK := time.Duration(0), time.Duration(0), time.Duration(0), time.Duration(0)
		if len(s.okLatencies) > 0 {
			sort.Slice(s.okLatencies, func(i, j int) bool { return s.okLatencies[i] < s.okLatencies[j] })
			var sum time.Duration
			for _, l := range s.okLatencies {
				sum += l
			}
			avgOK = sum / time.Duration(len(s.okLatencies))
			p50OK = pct(s.okLatencies, 50)
			p95OK = pct(s.okLatencies, 95)
			p99OK = pct(s.okLatencies, 99)
		}
		fmt.Printf("%-35s %8d %8d %8d %8d %10s %10s %10s %10s\n",
			s.name, s.total, s.success, s.throttled, s.fail,
			avgOK.Truncate(time.Microsecond),
			p50OK.Truncate(time.Microsecond),
			p95OK.Truncate(time.Microsecond),
			p99OK.Truncate(time.Microsecond),
		)
	}

	fmt.Println(strings.Repeat("-", 130))
	fmt.Printf("%-35s %8d %8d %8d %8d\n", "TOTAL", totalReqs, totalOK, totalThrottled, totalFail)
	fmt.Println(strings.Repeat("=", 130))
	fmt.Printf("\nTokens: %d | Duration: %s | Total requests: %d | Overall RPS: %.1f\n",
		len(pool.tokens), elapsed.Truncate(time.Millisecond), totalReqs, float64(totalReqs)/elapsed.Seconds())
	fmt.Printf("Successful RPS: %.1f | Throttled: %d (%.1f%%)\n",
		float64(totalOK)/elapsed.Seconds(), totalThrottled,
		float64(totalThrottled)/float64(totalReqs)*100)

	if totalFail > 0 {
		fmt.Printf("Real errors: %d (%.2f%%)\n", totalFail, float64(totalFail)/float64(totalReqs)*100)
	}
	fmt.Println()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func login(client *http.Client, base, user, pass string) (access, refresh string, err error) {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := client.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("login returned %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}
	return result.Data.AccessToken, result.Data.RefreshToken, nil
}

func refresh(client *http.Client, base, refreshToken string) (access, newRefresh string, err error) {
	body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	resp, err := client.Post(base+"/api/v1/auth/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("refresh returned %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}
	return result.Data.AccessToken, result.Data.RefreshToken, nil
}

func discoverLibrary(client *http.Client, base, token string) string {
	req, _ := http.NewRequest("GET", base+"/api/v1/libraries", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Data) > 0 {
		return result.Data[0].ID
	}
	return ""
}

func discoverItem(client *http.Client, base, token, libraryID string) string {
	if libraryID == "" {
		return ""
	}
	req, _ := http.NewRequest("GET", base+"/api/v1/libraries/"+libraryID+"/items?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Data) > 0 {
		return result.Data[0].ID
	}
	return ""
}

func doRequest(client *http.Client, base string, ep endpoint, token string) result {
	var bodyReader io.Reader
	if ep.body != nil {
		b, _ := json.Marshal(ep.body)
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(ep.method, base+ep.path, bodyReader)
	if err != nil {
		return result{endpoint: ep.name, err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if ep.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		return result{endpoint: ep.name, latency: latency, err: err}
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	return result{
		endpoint:  ep.name,
		status:    resp.StatusCode,
		latency:   latency,
		bodyBytes: n,
	}
}
