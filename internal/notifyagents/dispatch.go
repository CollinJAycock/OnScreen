package notifyagents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/email"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/observability"
)

// ── Intake ───────────────────────────────────────────────────────────────────

// coalesceKey identifies "the same event": MarkFulfilled notifies every
// requester of a title in one loop, and the channel should get one "X is
// ready to watch — requested by alice and bob", not one message per requester.
type coalesceKey struct {
	event, title, body string
	item               uuid.UUID
}

type pendingEvent struct {
	ev    notification.AgentEvent
	event string
	users []uuid.UUID
	timer *time.Timer
}

// DispatchAgentEvent implements notification.AgentDispatcher. Non-blocking:
// the event is held for the coalesce window, then queued.
func (s *Service) DispatchAgentEvent(_ context.Context, ev notification.AgentEvent) {
	key, ok := eventFor(ev)
	if !ok {
		return
	}
	ck := coalesceKey{event: key, title: ev.Title, body: ev.Body}
	if ev.ItemID != nil {
		ck.item = *ev.ItemID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if p, ok := s.pending[ck]; ok {
		if ev.UserID != nil && len(p.users) < maxRequesters && !slices.Contains(p.users, *ev.UserID) {
			p.users = append(p.users, *ev.UserID)
		}
		return
	}
	p := &pendingEvent{ev: ev, event: key}
	if ev.UserID != nil {
		p.users = []uuid.UUID{*ev.UserID}
	}
	s.pending[ck] = p
	p.timer = time.AfterFunc(s.coalesceWindow, func() { s.flushPending(ck) })
}

// flushPending renders a coalesced event and queues it.
func (s *Service) flushPending(ck coalesceKey) {
	s.mu.Lock()
	p, ok := s.pending[ck]
	delete(s.pending, ck)
	closed := s.closed
	s.mu.Unlock()
	if !ok || closed {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	// Channel privacy (privacy.go): no title from a private library, no *arr
	// failure reason.
	title, body := p.ev.Title, p.ev.Body
	private := p.ev.ItemID != nil && s.itemIsPrivate(ctx, *p.ev.ItemID)
	if private {
		title, body = privateItemText(p.event, title, body)
	}
	if p.event == EventRequestFailed {
		body = requestFailedText(body)
	}
	if names := s.usernames(ctx, p.users); len(names) > 0 {
		if body != "" {
			body += "\n"
		}
		body += "Requested by " + joinNames(names) + "."
	}
	var link string
	if p.ev.ItemID != nil && !private && (p.event == EventRequestAvailable || p.event == EventRequestSeasonAvailable) {
		link = s.link(ctx, "/watch/"+p.ev.ItemID.String())
	} else {
		link = s.link(ctx, eventPath(p.event))
	}
	m := Message{
		Event:    p.event,
		Title:    title,
		Body:     body,
		URL:      link,
		Severity: eventSeverity(p.event),
		Time:     s.now(),
	}
	if p.ev.ItemID != nil {
		// Two different private titles can render identically; they are
		// still two events.
		m.dedupe = p.ev.ItemID.String()
	}
	s.Emit(m)
}

// usernames resolves requester ids to usernames (best effort).
func (s *Service) usernames(ctx context.Context, ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		u, err := s.db.GetUser(ctx, id)
		if err != nil || u.Username == "" {
			continue
		}
		out = append(out, u.Username)
	}
	return out
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// link joins the configured public base URL and a web path; "" when no base
// URL is configured or it isn't an absolute http(s) URL.
func (s *Service) link(ctx context.Context, path string) string {
	base := strings.TrimRight(strings.TrimSpace(s.baseURL(ctx)), "/")
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	if path == "/" {
		return base + "/"
	}
	return base + path
}

// Emit queues a rendered message for every enabled agent subscribed to its
// event. Non-blocking: a full queue drops the message with a warning. Ops
// sources (scheduler, fleet monitor) call it directly.
func (s *Service) Emit(m Message) {
	if m.Time.IsZero() {
		m.Time = s.now()
	}
	if m.Severity == "" {
		m.Severity = eventSeverity(m.Event)
	}
	if s.isDuplicate(m) {
		return
	}
	select {
	case s.events <- m:
	default:
		s.logger.Warn("notification agents: event queue full, dropping", "event", m.Event)
	}
}

// isDuplicate drops a message identical to one queued within the dedupe
// window (a fulfilment reported by both the arr webhook and the reconcile
// pass, for instance).
func (s *Service) isDuplicate(m Message) bool {
	key := m.Event + "\x00" + m.Title + "\x00" + m.Body + "\x00" + m.dedupe
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, t := range s.recent {
		if now.Sub(t) > s.dedupeWindow {
			delete(s.recent, k)
		}
	}
	if t, ok := s.recent[key]; ok && now.Sub(t) <= s.dedupeWindow {
		return true
	}
	s.recent[key] = now
	return false
}

// fanOutLoop hands each queued message to the per-agent workers.
func (s *Service) fanOutLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case m := <-s.events:
			s.fanOut(m)
		}
	}
}

func (s *Service) fanOut(m Message) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	agents, err := s.db.ListEnabledNotificationAgentsForEvent(ctx, m.Event)
	if err != nil {
		s.logger.Warn("notification agents: list subscribers", "event", m.Event, "err", err)
		return
	}
	for _, a := range agents {
		s.enqueue(a.ID, m)
	}
}

// ── Per-agent workers ────────────────────────────────────────────────────────

// agentWorker delivers one agent's messages in order, rate limited. Each
// agent gets its own so a dead endpoint backing off for minutes stalls only
// itself.
type agentWorker struct {
	id   uuid.UUID
	jobs chan Message
	lim  rateLimiter
}

func (s *Service) enqueue(id uuid.UUID, m Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	w, ok := s.workers[id]
	if !ok {
		w = &agentWorker{id: id, jobs: make(chan Message, agentQueueSize)}
		s.workers[id] = w
		s.wg.Add(1)
		observability.SafeGo(s.logger, "notifyagents:worker", func() { s.runWorker(w) })
	}
	select {
	case w.jobs <- m:
	default:
		s.logger.Warn("notification agents: agent queue full, dropping", "agent_id", id, "event", m.Event)
	}
}

func (s *Service) runWorker(w *agentWorker) {
	defer s.wg.Done()
	// However the worker exits (idle, shutdown, a recovered panic), stop
	// routing jobs to it; the next message starts a fresh one.
	defer func() {
		s.mu.Lock()
		if s.workers[w.id] == w {
			delete(s.workers, w.id)
		}
		s.mu.Unlock()
	}()
	idle := time.NewTimer(s.idleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case m := <-w.jobs:
			s.deliverQueued(w, m)
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(s.idleTimeout)
		case <-idle.C:
			// Exit only if nothing was queued meanwhile; enqueue holds s.mu
			// while it sends, so this check can't race a new job.
			s.mu.Lock()
			if len(w.jobs) == 0 {
				delete(s.workers, w.id)
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			idle.Reset(s.idleTimeout)
		}
	}
}

// deliverQueued sends one queued message with retries and records the
// outcome on the agent row.
func (s *Service) deliverQueued(w *agentWorker, m Message) {
	var lastErr error
	for attempt, delay := range s.retryDelays {
		if delay > 0 && !s.sleep(delay) {
			return // shutting down
		}
		// Re-read the agent every attempt: a disable, delete or secret change
		// made while this message waited applies to it.
		rctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		row, err := s.getRow(rctx, w.id)
		cancel()
		if errors.Is(err, ErrNotFound) {
			return
		}
		if err != nil {
			lastErr = err
			continue
		}
		if !row.Enabled || !slices.Contains(row.Events, m.Event) {
			return
		}
		if wait := w.lim.reserve(s.now(), rateLimitFor(Kind(row.Kind)), s.rateWindow); wait > 0 {
			if !s.sleep(wait) {
				return
			}
		}
		err = s.send(s.ctx, row, m)
		if err == nil {
			s.recordSuccess(row.ID)
			return
		}
		lastErr = err
		s.logger.Warn("notification agent delivery failed",
			"agent_id", row.ID, "kind", row.Kind, "event", m.Event, "attempt", attempt+1, "err", err.Error())
		var perm *permanentError
		if errors.As(err, &perm) {
			break
		}
		var ra *retryAfterError
		if errors.As(err, &ra) && attempt+1 < len(s.retryDelays) && ra.after > s.retryDelays[attempt+1] {
			if !s.sleep(ra.after - s.retryDelays[attempt+1]) {
				return
			}
		}
	}
	if lastErr != nil && s.ctx.Err() == nil {
		s.recordFailure(w.id, lastErr)
	}
}

// sleep waits d or until shutdown; false means shutting down.
func (s *Service) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *Service) recordSuccess(id uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.RecordNotificationAgentSuccess(ctx, id); err != nil {
		s.logger.Warn("notification agents: record success", "agent_id", id, "err", err)
	}
}

func (s *Service) recordFailure(id uuid.UUID, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg := cause.Error() // already sanitized by send
	if err := s.db.RecordNotificationAgentFailure(ctx, gen.RecordNotificationAgentFailureParams{ID: id, LastError: msg}); err != nil {
		s.logger.Warn("notification agents: record failure", "agent_id", id, "err", err)
	}
}

// ── Rate limit ───────────────────────────────────────────────────────────────

// rateLimitFor is the per-agent budget per rate window (a minute in
// production). Discord allows ~30 webhook messages a minute per channel,
// Telegram ~20 a minute into a group.
func rateLimitFor(k Kind) int {
	switch k {
	case KindDiscord:
		return 25
	case KindTelegram:
		return 20
	case KindEmail:
		return 10
	case KindNtfy, KindGotify:
		return 60
	}
	return 60
}

// rateLimiter is a sliding-window limiter owned by one worker goroutine.
type rateLimiter struct{ sent []time.Time }

// reserve records a send at now and returns how long to wait before it may
// go out (0 = now). The reservation is placed at now+wait, so back-to-back
// calls queue up behind each other.
func (l *rateLimiter) reserve(now time.Time, limit int, window time.Duration) time.Duration {
	cut := now.Add(-window)
	i := 0
	for i < len(l.sent) && !l.sent[i].After(cut) {
		i++
	}
	l.sent = l.sent[i:]
	var wait time.Duration
	if limit > 0 && len(l.sent) >= limit {
		wait = l.sent[len(l.sent)-limit].Add(window).Sub(now)
		if wait < 0 {
			wait = 0
		}
	}
	l.sent = append(l.sent, now.Add(wait))
	return wait
}

// ── Sending ──────────────────────────────────────────────────────────────────

// permanentError: retrying won't help (a 4xx such as a deleted webhook or a
// revoked token, a config that no longer validates).
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

// retryAfterError: a 429 / 503 with a Retry-After hint.
type retryAfterError struct {
	msg   string
	after time.Duration
}

func (e *retryAfterError) Error() string { return e.msg }

// SendTest sends the Test event to one agent synchronously — enabled or not,
// whatever its subscriptions — and records the outcome. A failed delivery is
// a *DeliveryError; ErrNotFound and lookup errors pass through.
func (s *Service) SendTest(ctx context.Context, id uuid.UUID) error {
	row, err := s.getRow(ctx, id)
	if err != nil {
		return err
	}
	m := Message{
		Event:    EventTest,
		Title:    "OnScreen test notification",
		Body:     fmt.Sprintf("If you can read this, the %s agent %q is set up correctly.", row.Kind, row.Name),
		URL:      s.link(ctx, "/"),
		Severity: SeverityInfo,
		Time:     s.now(),
	}
	if err := s.send(ctx, row, m); err != nil {
		s.recordFailure(row.ID, err)
		return &DeliveryError{Msg: err.Error()}
	}
	s.recordSuccess(row.ID)
	return nil
}

// DeliveryError is SendTest's "the agent was reached for but the message
// didn't go through". Msg is sanitized — no secret, no secret-bearing URL —
// and meant for the admin.
type DeliveryError struct{ Msg string }

func (e *DeliveryError) Error() string { return e.Msg }

// send delivers m to one agent once. Every error it returns is sanitized:
// no secret, no secret-bearing URL.
func (s *Service) send(ctx context.Context, row gen.NotificationAgent, m Message) error {
	secret, err := s.open(row)
	if err != nil {
		return &permanentError{msg: err.Error()}
	}
	cfg, err := parseConfig(row.Config)
	if err != nil {
		return &permanentError{msg: "the agent's stored config is unreadable"}
	}
	kind := Kind(row.Kind)
	if kind == KindEmail {
		return s.sendEmail(ctx, cfg, m)
	}

	var out *outbound
	switch kind {
	case KindDiscord:
		// Re-checked at send time too: the allow-list must hold even if the
		// row was written around the API.
		if verr := validateDiscordWebhookURL(secret); verr != nil {
			return &permanentError{msg: verr.Error()}
		}
		out, err = buildDiscord(secret, m)
	case KindTelegram:
		out, err = buildTelegram(s.telegramBase, secret, cfg.ChatID, m)
	case KindNtfy:
		out, err = buildNtfy(cfg.ServerURL, cfg.Topic, secret, m)
	case KindGotify:
		out, err = buildGotify(cfg.ServerURL, secret, m)
	default:
		return &permanentError{msg: "unknown agent kind " + row.Kind}
	}
	if err != nil {
		return &permanentError{msg: err.Error()}
	}
	// Discord and Telegram are public services: never allow private targets,
	// whatever the row says. Plain http goes out only under the private-only
	// policy (re-checked here: the row may have been written around the API).
	policy, ok := agentNetPolicy(kind, row.AllowPrivateNetwork, out.url)
	if !ok {
		return &permanentError{msg: "the server URL must use https (plain http is only allowed for a server on your private network)"}
	}
	return s.do(ctx, s.clientFor(policy), out, secret)
}

func (s *Service) sendEmail(ctx context.Context, cfg Config, m Message) error {
	if s.email == nil {
		return &permanentError{msg: "email is not available on this server"}
	}
	if len(cfg.Recipients) == 0 {
		return &permanentError{msg: "no recipients configured"}
	}
	subject, text, html := buildEmail(m)
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := s.email.SendWithText(sctx, cfg.Recipients, subject, text, html); err != nil {
		if errors.Is(err, email.ErrNotConfigured) {
			return &permanentError{msg: "SMTP is not configured (Settings → Email)"}
		}
		return errors.New(clampError(err.Error()))
	}
	return nil
}

// do performs one HTTP delivery and classifies the result.
func (s *Service) do(ctx context.Context, client *http.Client, out *outbound, secret string) error {
	rctx, cancel := context.WithTimeout(ctx, s.sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, out.method, out.url, bytes.NewReader(out.body))
	if err != nil {
		return &permanentError{msg: "build request: " + sanitize(err, out.url, secret)}
	}
	req.Header = out.header.Clone()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New(sanitize(err, out.url, secret))
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg := "HTTP " + strconv.Itoa(resp.StatusCode)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Not followed (CheckRedirect): the POST must not move hosts.
		return &permanentError{msg: msg + ": unexpected redirect (not followed)"}
	}
	if detail := responseDetail(snippet, out.url, secret); detail != "" {
		msg += ": " + detail
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		return &retryAfterError{msg: msg, after: retryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout:
		return errors.New(msg)
	}
	return &permanentError{msg: msg}
}

// retryAfter parses a Retry-After seconds value, capped at five minutes.
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0
	}
	d := time.Duration(n) * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// responseDetail extracts a short, printable, secret-free explanation from an
// error response body (Telegram's "Bad Request: chat not found", Discord's
// "Unknown Webhook").
func responseDetail(body []byte, rawURL, secret string) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, string(body))
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	return clampError(redact(s, rawURL, secret))
}

// sanitize renders a transport error without the request URL — for Discord
// and Telegram the URL IS (or contains) the secret, and *url.Error quotes it.
func sanitize(err error, rawURL, secret string) string {
	var ue *url.Error
	msg := err.Error()
	if errors.As(err, &ue) {
		host := ""
		if u, perr := url.Parse(rawURL); perr == nil {
			host = u.Host
		}
		msg = ue.Op + " " + host + ": " + ue.Err.Error()
	}
	return clampError(redact(msg, rawURL, secret))
}

// redact removes every occurrence of the secret and the full request URL.
func redact(msg, rawURL, secret string) string {
	if rawURL != "" {
		msg = strings.ReplaceAll(msg, rawURL, "[redacted-url]")
	}
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "[redacted]")
		if esc := url.PathEscape(secret); esc != secret {
			msg = strings.ReplaceAll(msg, esc, "[redacted]")
		}
	}
	return msg
}

// clampError bounds an error for last_error (the column allows 500).
func clampError(s string) string {
	return truncateRunes(s, lastErrorMaxLen)
}
