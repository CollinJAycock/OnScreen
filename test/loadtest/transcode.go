// Transcode load test — simulates real-world viewing patterns.
//
// A new viewer starts a random movie every -interval (default 30s).
// Each viewer watches for -watch-duration (default 5m), consuming HLS
// segments like a real player, then stops. The test runs until -duration
// elapses and no more viewers are active.
//
// Usage:
//
//	go run ./test/loadtest/ -mode transcode -base http://localhost:7070 -user admin -pass pw
//	go run ./test/loadtest/ -mode transcode -base http://localhost:7070 -user admin -pass pw -duration 10m -interval 15s -watch 5m
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type transcodeSession struct {
	sessionID   string
	playlistURL string
	token       string
}

type viewerStats struct {
	viewer       int
	movieTitle   string
	startLatency time.Duration // time until first playlist ready
	segCount     int
	segBytes     int64
	segLatencies []time.Duration
	playlistPolls int
	errors       int
	watchTime    time.Duration // actual wall-clock watch time
}

func (s *viewerStats) segAvg() time.Duration {
	if len(s.segLatencies) == 0 {
		return 0
	}
	var total time.Duration
	for _, l := range s.segLatencies {
		total += l
	}
	return total / time.Duration(len(s.segLatencies))
}

type movieItem struct {
	ID    string
	Title string
}

func runTranscodeLoadTest(client *http.Client, base, token string, _ int, dur time.Duration) {
	fmt.Println("=== Transcode Load Test (Real-World) ===")
	fmt.Printf("Target:         %s\n", base)
	fmt.Printf("Duration:       %s (new viewers spawn for this long)\n", dur)
	fmt.Printf("New viewer:     every %s\n", *interval)
	fmt.Printf("Watch duration: %s per viewer\n", *watchDur)
	fmt.Println()

	// Discover all movies.
	movies := discoverAllMovies(client, base, token)
	if len(movies) == 0 {
		fmt.Println("ERROR: no movies found in any library")
		return
	}
	fmt.Printf("Found %d movies in library\n\n", len(movies))

	var (
		mu          sync.Mutex
		allViewers  []*viewerStats
		wg          sync.WaitGroup
		viewerCount int32
		totalSegs   int64
		peakActive  int32
		curActive   int32
	)

	start := time.Now()

	// Progress ticker — shows active viewers and segment throughput.
	ticker := time.NewTicker(10 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				elapsed := time.Since(start).Truncate(time.Second)
				segs := atomic.LoadInt64(&totalSegs)
				active := atomic.LoadInt32(&curActive)
				peak := atomic.LoadInt32(&peakActive)
				fmt.Printf("  [%s] active: %d (peak: %d) | segments: %d (%.1f seg/s)\n",
					elapsed, active, peak, segs, float64(segs)/time.Since(start).Seconds())
			case <-done:
				return
			}
		}
	}()

	// Spawn viewers on the interval until duration expires.
	spawnEnd := time.After(dur)
	spawnTicker := time.NewTicker(*interval)
	// Spawn the first viewer immediately.
	spawnViewer := func() {
		idx := int(atomic.AddInt32(&viewerCount, 1))
		movie := movies[rand.Intn(len(movies))]

		st := &viewerStats{viewer: idx, movieTitle: movie.Title}
		mu.Lock()
		allViewers = append(allViewers, st)
		mu.Unlock()

		wg.Add(1)
		go func() {
			defer wg.Done()
			active := atomic.AddInt32(&curActive, 1)
			// Update peak.
			for {
				peak := atomic.LoadInt32(&peakActive)
				if active <= peak || atomic.CompareAndSwapInt32(&peakActive, peak, active) {
					break
				}
			}

			fmt.Printf("  Viewer %d started: %s\n", idx, movie.Title)
			runViewer(client, base, token, idx, movie, *watchDur, st, &totalSegs)
			atomic.AddInt32(&curActive, -1)
			fmt.Printf("  Viewer %d finished: %d segs, %s\n", idx, st.segCount, formatBytes(st.segBytes))
		}()
	}

	spawnViewer() // first one immediately
loop:
	for {
		select {
		case <-spawnEnd:
			break loop
		case <-spawnTicker.C:
			spawnViewer()
		}
	}
	spawnTicker.Stop()

	// Wait for all viewers to finish their watch duration.
	fmt.Printf("\nNo more new viewers. Waiting for %d active viewers to finish...\n",
		atomic.LoadInt32(&curActive))
	wg.Wait()
	ticker.Stop()
	close(done)
	elapsed := time.Since(start)

	// ── Report ────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println(strings.Repeat("=", 120))
	fmt.Printf("%-8s %-30s %6s %10s %12s %10s %10s %10s %6s\n",
		"VIEWER", "MOVIE", "SEGS", "BYTES", "START_LAT", "SEG_AVG", "SEG_P50", "SEG_P95", "ERRS")
	fmt.Println(strings.Repeat("-", 120))

	var grandSegs, grandErrors int
	var grandBytes int64
	var grandLatencies []time.Duration
	var startLatencies []time.Duration

	for _, s := range allViewers {
		grandSegs += s.segCount
		grandErrors += s.errors
		grandBytes += s.segBytes
		grandLatencies = append(grandLatencies, s.segLatencies...)
		if s.startLatency > 0 {
			startLatencies = append(startLatencies, s.startLatency)
		}

		p50, p95 := time.Duration(0), time.Duration(0)
		if len(s.segLatencies) > 0 {
			sl := make([]time.Duration, len(s.segLatencies))
			copy(sl, s.segLatencies)
			sort.Slice(sl, func(i, j int) bool { return sl[i] < sl[j] })
			p50 = pct(sl, 50)
			p95 = pct(sl, 95)
		}

		title := s.movieTitle
		if len(title) > 28 {
			title = title[:28] + ".."
		}

		fmt.Printf("%-8d %-30s %6d %10s %12s %10s %10s %10s %6d\n",
			s.viewer, title, s.segCount, formatBytes(s.segBytes),
			s.startLatency.Truncate(time.Millisecond),
			s.segAvg().Truncate(time.Microsecond),
			p50.Truncate(time.Microsecond),
			p95.Truncate(time.Microsecond),
			s.errors,
		)
	}

	fmt.Println(strings.Repeat("=", 120))

	// Grand totals.
	grandAvg, grandP50, grandP95, grandP99 := time.Duration(0), time.Duration(0), time.Duration(0), time.Duration(0)
	if len(grandLatencies) > 0 {
		sort.Slice(grandLatencies, func(i, j int) bool { return grandLatencies[i] < grandLatencies[j] })
		var sum time.Duration
		for _, l := range grandLatencies {
			sum += l
		}
		grandAvg = sum / time.Duration(len(grandLatencies))
		grandP50 = pct(grandLatencies, 50)
		grandP95 = pct(grandLatencies, 95)
		grandP99 = pct(grandLatencies, 99)
	}

	startAvg, startP50, startP95 := time.Duration(0), time.Duration(0), time.Duration(0)
	if len(startLatencies) > 0 {
		sort.Slice(startLatencies, func(i, j int) bool { return startLatencies[i] < startLatencies[j] })
		var sum time.Duration
		for _, l := range startLatencies {
			sum += l
		}
		startAvg = sum / time.Duration(len(startLatencies))
		startP50 = pct(startLatencies, 50)
		startP95 = pct(startLatencies, 95)
	}

	fmt.Printf("\nViewers: %d total | Peak concurrent: %d | Duration: %s\n",
		len(allViewers), atomic.LoadInt32(&peakActive), elapsed.Truncate(time.Second))
	fmt.Printf("Segments: %d total (%.1f seg/s) | Data: %s (%.1f MB/s)\n",
		grandSegs, float64(grandSegs)/elapsed.Seconds(),
		formatBytes(grandBytes), float64(grandBytes)/1024/1024/elapsed.Seconds())
	fmt.Printf("Startup latency — avg: %s | p50: %s | p95: %s\n",
		startAvg.Truncate(time.Millisecond), startP50.Truncate(time.Millisecond), startP95.Truncate(time.Millisecond))
	fmt.Printf("Segment latency — avg: %s | p50: %s | p95: %s | p99: %s\n",
		grandAvg.Truncate(time.Microsecond), grandP50.Truncate(time.Microsecond),
		grandP95.Truncate(time.Microsecond), grandP99.Truncate(time.Microsecond))
	if grandErrors > 0 {
		fmt.Printf("Errors: %d\n", grandErrors)
	}
	fmt.Println()
}

// runViewer simulates a single viewer: starts a transcode, consumes segments
// for watchDur, then stops the session.
func runViewer(client *http.Client, base, token string, idx int, movie movieItem, watchDur time.Duration, st *viewerStats, totalSegs *int64) {
	sess, err := startTranscode(client, base, token, movie.ID, 1080)
	if err != nil {
		st.errors++
		fmt.Printf("  Viewer %d: failed to start — %v\n", idx, err)
		return
	}
	defer stopTranscode(client, base, token, sess)

	seen := make(map[string]bool)
	playlistURL := base + sess.playlistURL
	viewStart := time.Now()

	// Wait for initial playlist.
	startWait := time.Now()
	var playlist string
	for {
		p, err := fetchPlaylist(client, playlistURL)
		if err == nil && p != "" {
			playlist = p
			break
		}
		st.playlistPolls++
		time.Sleep(200 * time.Millisecond)
		if time.Since(startWait) > 20*time.Second {
			st.errors++
			return
		}
	}
	st.startLatency = time.Since(startWait)

	// Consume segments until watch duration expires.
	for time.Since(viewStart) < watchDur {
		segments := parseSegmentURLs(playlist)
		for _, segURL := range segments {
			if seen[segURL] {
				continue
			}
			seen[segURL] = true

			if time.Since(viewStart) >= watchDur {
				break
			}

			t := time.Now()
			resp, err := client.Get(base + segURL)
			lat := time.Since(t)

			if err != nil {
				st.errors++
				continue
			}
			n, _ := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode != 200 {
				st.errors++
				continue
			}

			st.segCount++
			st.segBytes += n
			st.segLatencies = append(st.segLatencies, lat)
			atomic.AddInt64(totalSegs, 1)
		}

		// Poll for new segments — HLS player polls every ~2s.
		time.Sleep(2 * time.Second)
		st.playlistPolls++
		p, err := fetchPlaylist(client, playlistURL)
		if err != nil {
			st.errors++
			continue
		}
		playlist = p
	}
	st.watchTime = time.Since(viewStart)
}

func startTranscode(client *http.Client, base, token, itemID string, height int) (transcodeSession, error) {
	body, _ := json.Marshal(map[string]any{
		"height":      height,
		"position_ms": 0,
	})
	req, _ := http.NewRequest("POST", base+"/api/v1/items/"+itemID+"/transcode", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return transcodeSession{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return transcodeSession{}, fmt.Errorf("start returned %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Data struct {
			SessionID   string `json:"session_id"`
			PlaylistURL string `json:"playlist_url"`
			Token       string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return transcodeSession{}, err
	}
	return transcodeSession{
		sessionID:   result.Data.SessionID,
		playlistURL: result.Data.PlaylistURL,
		token:       result.Data.Token,
	}, nil
}

func stopTranscode(client *http.Client, base, token string, sess transcodeSession) {
	url := fmt.Sprintf("%s/api/v1/transcode/sessions/%s?token=%s", base, sess.sessionID, sess.token)
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func fetchPlaylist(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("playlist %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func parseSegmentURLs(playlist string) []string {
	var urls []string
	scanner := bufio.NewScanner(strings.NewReader(playlist))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "/api/v1/transcode/sessions/") && strings.Contains(line, ".ts") {
			urls = append(urls, line)
		}
	}
	return urls
}

func discoverAllMovies(client *http.Client, base, token string) []movieItem {
	// Get all libraries.
	req, _ := http.NewRequest("GET", base+"/api/v1/libraries", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var libResult struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&libResult)

	// Collect movies from all libraries.
	var movies []movieItem
	for _, lib := range libResult.Data {
		req, _ := http.NewRequest("GET", base+"/api/v1/libraries/"+lib.ID+"/items?limit=100", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var result struct {
			Data []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Type  string `json:"type"`
			} `json:"data"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		for _, item := range result.Data {
			if item.Type == "movie" {
				movies = append(movies, movieItem{ID: item.ID, Title: item.Title})
			}
		}
	}
	return movies
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
