package notifyagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// DB is the query surface the service needs (*gen.Queries satisfies it).
type DB interface {
	ListNotificationAgents(ctx context.Context) ([]gen.NotificationAgent, error)
	GetNotificationAgent(ctx context.Context, id uuid.UUID) (gen.NotificationAgent, error)
	ListEnabledNotificationAgentsForEvent(ctx context.Context, event string) ([]gen.NotificationAgent, error)
	CreateNotificationAgent(ctx context.Context, arg gen.CreateNotificationAgentParams) (gen.NotificationAgent, error)
	UpdateNotificationAgent(ctx context.Context, arg gen.UpdateNotificationAgentParams) (gen.NotificationAgent, error)
	DeleteNotificationAgent(ctx context.Context, id uuid.UUID) (int64, error)
	RecordNotificationAgentSuccess(ctx context.Context, id uuid.UUID) error
	RecordNotificationAgentFailure(ctx context.Context, arg gen.RecordNotificationAgentFailureParams) error
	ListNewContentForNotificationAgents(ctx context.Context, arg gen.ListNewContentForNotificationAgentsParams) ([]gen.ListNewContentForNotificationAgentsRow, error)
	GetUser(ctx context.Context, id uuid.UUID) (gen.User, error)
}

// Encrypter seals agent secrets at rest (*auth.Encryptor satisfies it).
type Encrypter interface {
	EncryptContext(plaintext, context string) (string, error)
	DecryptContext(encoded, context string) (string, error)
}

// EmailSender sends email-agent messages (*email.Sender satisfies it).
type EmailSender interface {
	SendWithText(ctx context.Context, to []string, subject, textBody, htmlBody string) error
}

// Options configures a Service.
type Options struct {
	DB        DB
	Encrypter Encrypter
	Email     EmailSender
	// BaseURL returns the server's configured public URL (Settings → General),
	// or "" — links are omitted then. Read per message so a change applies
	// without a restart.
	BaseURL func(ctx context.Context) string
	Logger  *slog.Logger
}

// Service manages agents (CRUD for the admin API) and delivers events to
// them. Construct with New; Close drains it on shutdown.
type Service struct {
	db      DB
	enc     Encrypter
	email   EmailSender
	baseURL func(ctx context.Context) string
	logger  *slog.Logger

	// items looks up an item's library for the private-library check on
	// forwarded events; nil when DB doesn't provide it (every item is then
	// treated as private — a title is never announced by accident).
	items itemLibraryDB
	// httpClients holds one delivery client per network policy, built once
	// and reused so keep-alive connections are pooled across sends.
	httpClients [netPolicyCount]*http.Client

	// Seams, overridden by tests.
	clientFor      func(p netPolicy) *http.Client
	lookupHost     func(ctx context.Context, host string) ([]string, error)
	telegramBase   string
	retryDelays    []time.Duration
	sendTimeout    time.Duration
	coalesceWindow time.Duration
	dedupeWindow   time.Duration
	rateWindow     time.Duration
	idleTimeout    time.Duration
	taskRealert    time.Duration
	now            func() time.Time

	events chan Message

	mu         sync.Mutex
	pending    map[coalesceKey]*pendingEvent
	recent     map[string]time.Time
	workers    map[uuid.UUID]*agentWorker
	taskAlerts map[uuid.UUID]time.Time
	newContent *newContentBatcher
	closed     bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Queue and delivery tuning.
const (
	eventQueueSize  = 256
	agentQueueSize  = 32
	maxRequesters   = 5
	lastErrorMaxLen = 300
)

// New builds a Service and starts its fan-out loop.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.BaseURL == nil {
		opts.BaseURL = func(context.Context) string { return "" }
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		db:      opts.DB,
		enc:     opts.Encrypter,
		email:   opts.Email,
		baseURL: opts.BaseURL,
		logger:  opts.Logger,

		lookupHost:     net.DefaultResolver.LookupHost,
		telegramBase:   "https://api.telegram.org",
		retryDelays:    []time.Duration{0, 30 * time.Second, 5 * time.Minute},
		sendTimeout:    15 * time.Second,
		coalesceWindow: 3 * time.Second,
		dedupeWindow:   10 * time.Minute,
		rateWindow:     time.Minute,
		idleTimeout:    2 * time.Minute,
		taskRealert:    24 * time.Hour,
		now:            time.Now,

		events:     make(chan Message, eventQueueSize),
		pending:    map[coalesceKey]*pendingEvent{},
		recent:     map[string]time.Time{},
		workers:    map[uuid.UUID]*agentWorker{},
		taskAlerts: map[uuid.UUID]time.Time{},

		ctx:    ctx,
		cancel: cancel,
	}
	if idb, ok := opts.DB.(itemLibraryDB); ok {
		s.items = idb
	}
	for p := range s.httpClients {
		s.httpClients[p] = newHTTPClient(netPolicy(p), 15*time.Second)
	}
	s.clientFor = func(p netPolicy) *http.Client { return s.httpClients[p] }
	s.newContent = newNewContentBatcher(s, 2*time.Minute)
	s.wg.Add(1)
	go s.fanOutLoop()
	return s
}

// Close stops accepting events, abandons pending retries and waits for the
// workers to exit.
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for _, p := range s.pending {
		p.timer.Stop()
	}
	s.mu.Unlock()
	s.newContent.stop()
	s.cancel()
	s.wg.Wait()
	for _, c := range s.httpClients {
		c.CloseIdleConnections()
	}
}

// SecretContext binds an agent's ciphertext to its row: a secret copied onto
// another agent's row fails to decrypt instead of working there. Exported for
// cmd/rotate-key, which must re-seal with exactly the same context.
func SecretContext(id uuid.UUID) string { return "notification_agent:" + id.String() }

// ── API surface ──────────────────────────────────────────────────────────────

// List returns every agent, oldest first.
func (s *Service) List(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.ListNotificationAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notification agents: %w", err)
	}
	out := make([]Agent, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAgent(r))
	}
	return out, nil
}

// Get returns one agent.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Agent, error) {
	row, err := s.getRow(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	return toAgent(row), nil
}

func (s *Service) getRow(ctx context.Context, id uuid.UUID) (gen.NotificationAgent, error) {
	row, err := s.db.GetNotificationAgent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.NotificationAgent{}, ErrNotFound
	}
	if err != nil {
		return gen.NotificationAgent{}, fmt.Errorf("get notification agent: %w", err)
	}
	return row, nil
}

// Create validates and stores a new agent.
func (s *Service) Create(ctx context.Context, in CreateInput) (Agent, error) {
	if !in.Kind.Valid() {
		return Agent{}, invalid("kind must be one of discord, telegram, ntfy, gotify, email")
	}
	events := in.Events
	if events == nil {
		events = DefaultEvents(in.Kind)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	st := agentState{
		kind:         in.Kind,
		name:         in.Name,
		enabled:      enabled,
		events:       events,
		cfg:          in.Config,
		secret:       strings.TrimSpace(in.Secret),
		allowPrivate: in.AllowPrivateNetwork,
	}
	if err := s.validate(ctx, &st); err != nil {
		return Agent{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Agent{}, fmt.Errorf("new agent id: %w", err)
	}
	sealed, err := s.seal(id, st.secret)
	if err != nil {
		return Agent{}, err
	}
	cfgJSON, err := json.Marshal(st.cfg)
	if err != nil {
		return Agent{}, fmt.Errorf("marshal agent config: %w", err)
	}
	row, err := s.db.CreateNotificationAgent(ctx, gen.CreateNotificationAgentParams{
		ID:                  id,
		Kind:                string(st.kind),
		Name:                st.name,
		Enabled:             st.enabled,
		Events:              st.events,
		Config:              cfgJSON,
		Secret:              sealed,
		AllowPrivateNetwork: st.allowPrivate,
	})
	if err != nil {
		return Agent{}, fmt.Errorf("create notification agent: %w", err)
	}
	return toAgent(row), nil
}

// Update applies a PATCH. A kept secret must not follow its endpoint: moving
// an ntfy / Gotify agent to a different server URL without re-entering the
// token is refused, or a hijacked admin session could redirect the stored
// token to a host it controls.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (Agent, error) {
	row, err := s.getRow(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	kind := Kind(row.Kind)
	if in.Kind != nil && *in.Kind != kind {
		return Agent{}, invalid("an agent's kind can't be changed — create a new agent instead")
	}
	cur, err := parseConfig(row.Config)
	if err != nil {
		return Agent{}, err
	}
	st := agentState{
		kind:         kind,
		name:         row.Name,
		enabled:      row.Enabled,
		events:       row.Events,
		cfg:          cur,
		allowPrivate: row.AllowPrivateNetwork,
	}
	if in.Name != nil {
		st.name = *in.Name
	}
	if in.Enabled != nil {
		st.enabled = *in.Enabled
	}
	if in.Events != nil {
		st.events = in.Events
	}
	if in.Config != nil {
		st.cfg = *in.Config
	}
	if in.AllowPrivateNetwork != nil {
		st.allowPrivate = *in.AllowPrivateNetwork
	}
	newSecret := ""
	if in.Secret != nil {
		newSecret = strings.TrimSpace(*in.Secret)
	}
	storedSecret := row.Secret != nil && *row.Secret != ""
	keepSecret := storedSecret && newSecret == "" && !in.ClearSecret
	switch {
	case newSecret != "":
		st.secret = newSecret
	case keepSecret:
		st.secretKept = true
	}

	serverChanged := in.Config != nil && !sameEndpoint(cur.ServerURL, in.Config.ServerURL)
	if keepSecret && kind.selfHostable() && serverChanged {
		return Agent{}, invalid(secretLabel(kind) + " must be re-entered when changing the server URL")
	}
	st.skipHostCheck = !serverChanged && st.allowPrivate == row.AllowPrivateNetwork
	if err := s.validate(ctx, &st); err != nil {
		return Agent{}, err
	}

	secret := row.Secret
	switch {
	case st.secret != "":
		if secret, err = s.seal(id, st.secret); err != nil {
			return Agent{}, err
		}
	case !st.secretKept:
		secret = nil
	}
	cfgJSON, err := json.Marshal(st.cfg)
	if err != nil {
		return Agent{}, fmt.Errorf("marshal agent config: %w", err)
	}
	updated, err := s.db.UpdateNotificationAgent(ctx, gen.UpdateNotificationAgentParams{
		ID:                  id,
		Name:                st.name,
		Enabled:             st.enabled,
		Events:              st.events,
		Config:              cfgJSON,
		Secret:              secret,
		AllowPrivateNetwork: st.allowPrivate,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, fmt.Errorf("update notification agent: %w", err)
	}
	return toAgent(updated), nil
}

// Delete removes an agent. Messages already queued for it are dropped: the
// worker re-reads the row before every attempt.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	n, err := s.db.DeleteNotificationAgent(ctx, id)
	if err != nil {
		return fmt.Errorf("delete notification agent: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// sameEndpoint compares two server URLs the way the settings endpoint-move
// rule does: trimmed, trailing slash ignored, case-insensitive.
func sameEndpoint(a, b string) bool {
	norm := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
	return strings.EqualFold(norm(a), norm(b))
}

// secretLabel names a kind's secret in admin-facing messages.
func secretLabel(k Kind) string {
	switch k {
	case KindDiscord:
		return "the Discord webhook URL"
	case KindTelegram:
		return "the Telegram bot token"
	case KindNtfy:
		return "the ntfy access token"
	case KindGotify:
		return "the Gotify app token"
	case KindEmail:
	}
	return "the secret"
}

// agentState is a create/update after merging, before it is stored.
type agentState struct {
	kind         Kind
	name         string
	enabled      bool
	events       []string
	cfg          Config
	secret       string // a NEW plaintext secret, or ""
	secretKept   bool   // the stored secret stays
	allowPrivate bool
	// skipHostCheck skips the save-time DNS check when neither the server
	// URL nor the private-network opt-in changed (toggling an agent off must
	// work while its server is down). Delivery re-checks every dial anyway.
	skipHostCheck bool
}

func (st *agentState) hasSecret() bool { return st.secret != "" || st.secretKept }

// validate normalizes st in place and rejects what can't be stored.
func (s *Service) validate(ctx context.Context, st *agentState) error {
	st.name = strings.TrimSpace(st.name)
	if st.name == "" {
		return invalid("name is required")
	}
	if len([]rune(st.name)) > 100 {
		return invalid("name must be at most 100 characters")
	}
	if strings.ContainsFunc(st.name, unicode.IsControl) {
		return invalid("name must not contain control characters")
	}

	events := make([]string, 0, len(st.events))
	for _, e := range st.events {
		if !IsEvent(e) {
			return invalid("unknown event " + fmt.Sprintf("%q", e))
		}
		if !slices.Contains(events, e) {
			events = append(events, e)
		}
	}
	if len(events) == 0 {
		return invalid("select at least one event")
	}
	st.events = events

	if st.allowPrivate && !st.kind.selfHostable() {
		return invalid("\"Allow private network\" is only for a self-hosted ntfy or Gotify server")
	}

	cfg := Config{}
	switch st.kind {
	case KindDiscord:
		if !st.hasSecret() {
			return invalid("the Discord webhook URL is required")
		}
		if st.secret != "" {
			if err := validateDiscordWebhookURL(st.secret); err != nil {
				return err
			}
		}
	case KindTelegram:
		if !st.hasSecret() {
			return invalid("the Telegram bot token is required")
		}
		if st.secret != "" && !telegramToken.MatchString(st.secret) {
			return invalid("the Telegram bot token should look like 123456789:AA… (from @BotFather)")
		}
		cfg.ChatID = strings.TrimSpace(st.cfg.ChatID)
		if !telegramChatID.MatchString(cfg.ChatID) {
			return invalid("the Telegram chat id must be a number (groups start with -) or an @channel name")
		}
	case KindNtfy, KindGotify:
		server, err := normalizeServerURL(st.cfg.ServerURL, st.allowPrivate)
		if err != nil {
			return err
		}
		u, _ := url.Parse(server)
		if !st.skipHostCheck {
			policy, _ := agentNetPolicy(st.kind, st.allowPrivate, server)
			if err := checkResolvedHost(ctx, s.lookupHost, u.Hostname(), policy); err != nil {
				return err
			}
		}
		cfg.ServerURL = server
		if st.kind == KindNtfy {
			cfg.Topic = strings.TrimSpace(st.cfg.Topic)
			if !ntfyTopic.MatchString(cfg.Topic) {
				return invalid("the ntfy topic may use letters, digits, - and _ (at most 64)")
			}
		} else if !st.hasSecret() {
			return invalid("the Gotify app token is required")
		}
		if st.secret != "" && (len(st.secret) > 256 || strings.ContainsFunc(st.secret, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		})) {
			return invalid(secretLabel(st.kind) + " looks malformed")
		}
	case KindEmail:
		if st.secret != "" {
			return invalid("email agents have no secret — they use the server's SMTP settings")
		}
		if len(st.cfg.Recipients) == 0 {
			return invalid("add at least one recipient address")
		}
		if len(st.cfg.Recipients) > 10 {
			return invalid("at most 10 recipient addresses")
		}
		for _, r := range st.cfg.Recipients {
			addr, err := mail.ParseAddress(strings.TrimSpace(r))
			if err != nil || !strings.Contains(addr.Address, "@") {
				return invalid(fmt.Sprintf("%q is not a valid email address", r))
			}
			if !slices.Contains(cfg.Recipients, addr.Address) {
				cfg.Recipients = append(cfg.Recipients, addr.Address)
			}
		}
	}
	st.cfg = cfg
	return nil
}

// seal encrypts a new secret for row id; "" stores NULL.
func (s *Service) seal(id uuid.UUID, plain string) (*string, error) {
	if plain == "" {
		return nil, nil
	}
	if s.enc == nil {
		return nil, errors.New("notification agents: no encryptor configured; refusing to store a secret")
	}
	sealed, err := s.enc.EncryptContext(plain, SecretContext(id))
	if err != nil {
		return nil, fmt.Errorf("encrypt agent secret: %w", err)
	}
	return &sealed, nil
}

// open decrypts an agent's stored secret ("" when none).
func (s *Service) open(row gen.NotificationAgent) (string, error) {
	if row.Secret == nil || *row.Secret == "" {
		return "", nil
	}
	if s.enc == nil {
		return "", errors.New("no encryptor configured")
	}
	plain, err := s.enc.DecryptContext(*row.Secret, SecretContext(row.ID))
	if err != nil {
		// Never wrap the ciphertext or the crypto detail into last_error.
		return "", errors.New("the stored secret can't be decrypted (was SECRET_KEY changed?) — re-enter it")
	}
	return plain, nil
}

func parseConfig(raw []byte) (Config, error) {
	var c Config
	if len(raw) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("parse agent config: %w", err)
	}
	return c, nil
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// toAgent converts a row to the API view — without the secret.
func toAgent(r gen.NotificationAgent) Agent {
	cfg, _ := parseConfig(r.Config)
	events := r.Events
	if events == nil {
		events = []string{}
	}
	return Agent{
		ID:                  r.ID,
		Kind:                Kind(r.Kind),
		Name:                r.Name,
		Enabled:             r.Enabled,
		Events:              events,
		Config:              cfg,
		SecretConfigured:    r.Secret != nil && *r.Secret != "",
		AllowPrivateNetwork: r.AllowPrivateNetwork,
		LastSuccessAt:       tsPtr(r.LastSuccessAt),
		LastError:           r.LastError,
		LastErrorAt:         tsPtr(r.LastErrorAt),
		CreatedAt:           r.CreatedAt.Time,
		UpdatedAt:           r.UpdatedAt.Time,
	}
}
