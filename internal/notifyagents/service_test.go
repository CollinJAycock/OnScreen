package notifyagents

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/notification"
)

const (
	discordURL        = "https://discord.com/api/webhooks/123456/sEcReT-webhook-token"
	testTelegramToken = "123456789:AAFsecretTokenValue_0123456789abcdef"
)

func mustValidation(t *testing.T, err error, contains string) {
	t.Helper()
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError containing %q, got %v", contains, err)
	}
	if !strings.Contains(verr.Msg, contains) {
		t.Fatalf("validation message %q doesn't mention %q", verr.Msg, contains)
	}
}

// TestCreate_SecretEncryptedBoundAndNeverReturned: the stored secret is
// ciphertext bound to the row id (it won't decrypt on another row), and
// neither the API view nor its JSON carries it.
func TestCreate_SecretEncryptedBoundAndNeverReturned(t *testing.T) {
	db := newFakeDB()
	s := testService(t, db, "")
	a, err := s.Create(context.Background(), CreateInput{Kind: KindDiscord, Name: "Family", Secret: discordURL})
	if err != nil {
		t.Fatal(err)
	}
	if !a.SecretConfigured {
		t.Error("secret_configured = false")
	}
	row := db.agents[a.ID]
	if row.Secret == nil || strings.Contains(*row.Secret, "sEcReT") || *row.Secret == discordURL {
		t.Fatalf("secret stored in the clear: %v", row.Secret)
	}
	if plain, err := s.open(row); err != nil || plain != discordURL {
		t.Fatalf("open = %q, %v", plain, err)
	}
	moved := row
	moved.ID = uuid.New()
	if _, err := s.open(moved); err == nil {
		t.Error("ciphertext decrypted on another row")
	}

	body, _ := json.Marshal(a)
	list, _ := s.List(context.Background())
	listBody, _ := json.Marshal(list)
	for _, b := range [][]byte{body, listBody} {
		if strings.Contains(string(b), "sEcReT") || strings.Contains(string(b), *row.Secret) {
			t.Errorf("API view leaks the secret: %s", b)
		}
	}
	if !strings.Contains(string(body), `"secret_configured":true`) {
		t.Errorf("API view = %s", body)
	}
}

func TestCreate_DefaultsPerKind(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	a, err := s.Create(context.Background(), CreateInput{Kind: KindTelegram, Name: "Group", Secret: testTelegramToken, Config: Config{ChatID: "-100123"}})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Enabled || slices.Contains(a.Events, EventNewContent) || !slices.Contains(a.Events, EventRequestAvailable) {
		t.Errorf("telegram defaults = enabled %v events %v", a.Enabled, a.Events)
	}
	e, err := s.Create(context.Background(), CreateInput{Kind: KindEmail, Name: "Admins", Config: Config{Recipients: []string{"Admin <admin@example.com>", "admin@example.com", "ops@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(e.Events, EventRequestAvailable) || !slices.Contains(e.Events, EventRequestPending) {
		t.Errorf("email defaults = %v", e.Events)
	}
	if !slices.Equal(e.Config.Recipients, []string{"admin@example.com", "ops@example.com"}) || e.SecretConfigured {
		t.Errorf("email agent = %+v", e)
	}
}

func TestCreate_Validation(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	ctx := context.Background()
	cases := []struct {
		in   CreateInput
		want string
	}{
		{CreateInput{Kind: "slack", Name: "x"}, "kind"},
		{CreateInput{Kind: KindDiscord, Name: " ", Secret: discordURL}, "name"},
		{CreateInput{Kind: KindDiscord, Name: "x"}, "webhook URL is required"},
		{CreateInput{Kind: KindDiscord, Name: "x", Secret: "https://evil.example/api/webhooks/1/x"}, "discord.com"},
		{CreateInput{Kind: KindDiscord, Name: "x", Secret: discordURL, AllowPrivateNetwork: true}, "self-hosted"},
		{CreateInput{Kind: KindDiscord, Name: "x", Secret: discordURL, Events: []string{"media.play"}}, "unknown event"},
		{CreateInput{Kind: KindDiscord, Name: "x", Secret: discordURL, Events: []string{}}, "at least one event"},
		{CreateInput{Kind: KindDiscord, Name: "x", Secret: discordURL, Events: []string{EventTest}}, "unknown event"},
		{CreateInput{Kind: KindTelegram, Name: "x", Secret: "nope", Config: Config{ChatID: "1"}}, "bot token"},
		{CreateInput{Kind: KindTelegram, Name: "x", Secret: testTelegramToken, Config: Config{ChatID: "chat"}}, "chat id"},
		{CreateInput{Kind: KindNtfy, Name: "x", Config: Config{ServerURL: "https://ntfy.sh", Topic: "bad topic"}}, "topic"},
		{CreateInput{Kind: KindNtfy, Name: "x", Config: Config{ServerURL: "http://ntfy.sh", Topic: "t"}}, "https"},
		{CreateInput{Kind: KindGotify, Name: "x", Config: Config{ServerURL: "https://gotify.example.com"}}, "app token is required"},
		{CreateInput{Kind: KindEmail, Name: "x"}, "recipient"},
		{CreateInput{Kind: KindEmail, Name: "x", Config: Config{Recipients: []string{"not-an-address"}}}, "not a valid email"},
		{CreateInput{Kind: KindEmail, Name: "x", Secret: "pw", Config: Config{Recipients: []string{"a@b.c"}}}, "no secret"},
	}
	for _, c := range cases {
		_, err := s.Create(ctx, c.in)
		mustValidation(t, err, c.want)
	}
}

// TestCreate_PrivateNetworkNeedsOptIn: a self-hosted server that resolves to
// a LAN address is refused until allow_private_network is set.
func TestCreate_PrivateNetworkNeedsOptIn(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	s.lookupHost = func(context.Context, string) ([]string, error) { return []string{"192.168.1.20"}, nil }
	in := CreateInput{Kind: KindGotify, Name: "LAN", Secret: "AppTok3n", Config: Config{ServerURL: "http://gotify.lan"}}
	_, err := s.Create(context.Background(), in)
	mustValidation(t, err, "https")
	in.Config.ServerURL = "https://gotify.lan"
	_, err = s.Create(context.Background(), in)
	mustValidation(t, err, "Allow private network")
	in.Config.ServerURL = "http://gotify.lan"
	in.AllowPrivateNetwork = true
	a, err := s.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("LAN gotify with opt-in: %v", err)
	}
	if !a.AllowPrivateNetwork || a.Config.ServerURL != "http://gotify.lan" {
		t.Errorf("agent = %+v", a)
	}
}

// TestUpdate_SecretDoesNotFollowEndpoint: moving an ntfy agent to another
// server keeps working only when the token is re-entered (422 otherwise);
// a blank secret keeps the stored one; the kind can't change.
func TestUpdate_SecretDoesNotFollowEndpoint(t *testing.T) {
	db := newFakeDB()
	s := testService(t, db, "")
	ctx := context.Background()
	a, err := s.Create(ctx, CreateInput{Kind: KindNtfy, Name: "Phones", Secret: "tk_original", Config: Config{ServerURL: "https://ntfy.example.com", Topic: "house"}})
	if err != nil {
		t.Fatal(err)
	}
	before := *db.agents[a.ID].Secret

	moved := Config{ServerURL: "https://attacker.example.net", Topic: "house"}
	_, err = s.Update(ctx, a.ID, UpdateInput{Config: &moved})
	mustValidation(t, err, "re-entered")
	blank := ""
	_, err = s.Update(ctx, a.ID, UpdateInput{Config: &moved, Secret: &blank})
	mustValidation(t, err, "re-entered")
	if *db.agents[a.ID].Secret != before {
		t.Fatal("refused update changed the stored secret")
	}

	// Same server (trailing slash, case) with a new topic keeps the secret.
	same := Config{ServerURL: "https://NTFY.example.com/", Topic: "house2"}
	got, err := s.Update(ctx, a.ID, UpdateInput{Config: &same})
	if err != nil {
		t.Fatalf("same-endpoint update: %v", err)
	}
	if !got.SecretConfigured || *db.agents[a.ID].Secret != before || got.Config.Topic != "house2" {
		t.Errorf("secret not kept: %+v", got)
	}

	// Re-entering the token allows the move.
	newTok := "tk_new"
	got, err = s.Update(ctx, a.ID, UpdateInput{Config: &moved, Secret: &newTok})
	if err != nil {
		t.Fatalf("move with re-entered token: %v", err)
	}
	if plain, _ := s.open(db.agents[a.ID]); plain != "tk_new" || got.Config.ServerURL != "https://attacker.example.net" {
		t.Errorf("after move: secret %q config %+v", plain, got.Config)
	}

	// Clearing the optional ntfy token.
	got, err = s.Update(ctx, a.ID, UpdateInput{ClearSecret: true})
	if err != nil || got.SecretConfigured || db.agents[a.ID].Secret != nil {
		t.Errorf("clear secret: %+v, %v", got, err)
	}

	gotify := KindGotify
	_, err = s.Update(ctx, a.ID, UpdateInput{Kind: &gotify})
	mustValidation(t, err, "kind")
}

func TestUpdate_TogglesAndNotFound(t *testing.T) {
	db := newFakeDB()
	s := testService(t, db, "")
	ctx := context.Background()
	a, _ := s.Create(ctx, CreateInput{Kind: KindDiscord, Name: "D", Secret: discordURL})
	off := false
	got, err := s.Update(ctx, a.ID, UpdateInput{Enabled: &off, Events: []string{EventNewContent, EventNewContent}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || !slices.Equal(got.Events, []string{EventNewContent}) || !got.SecretConfigured {
		t.Errorf("toggle update = %+v", got)
	}
	if _, err := s.Update(ctx, uuid.New(), UpdateInput{Enabled: &off}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if err := s.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

// TestUpdate_DiscordSecretReplaceValidated: a new Discord webhook URL is
// checked against the allow-list like on create.
func TestUpdate_DiscordSecretReplaceValidated(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	a, _ := s.Create(context.Background(), CreateInput{Kind: KindDiscord, Name: "D", Secret: discordURL})
	evil := "https://discord.com.evil.example/api/webhooks/1/x"
	_, err := s.Update(context.Background(), a.ID, UpdateInput{Secret: &evil})
	mustValidation(t, err, "discord.com")
}

func TestEventsCatalog(t *testing.T) {
	ev := Events()
	if len(ev) != len(catalog) {
		t.Fatalf("events = %d", len(ev))
	}
	seen := map[string]bool{}
	for _, e := range ev {
		if e.Key == "" || e.Label == "" || e.Group == "" || e.DefaultFor == nil {
			t.Errorf("incomplete event %+v", e)
		}
		if seen[e.Key] {
			t.Errorf("duplicate event %s", e.Key)
		}
		seen[e.Key] = true
		if e.Key == EventTest {
			t.Error("the test event must not be subscribable")
		}
	}
	for _, k := range Kinds {
		if slices.Contains(DefaultEvents(k), EventNewContent) {
			t.Errorf("%s subscribes to new_content by default", k)
		}
	}
}

// TestForwardedTypesAreNotificationTypes: every in-app type the agents take
// is a declared notification type and a subscribable event — a rename on
// either side fails here instead of silently dropping the event.
func TestForwardedTypesAreNotificationTypes(t *testing.T) {
	for typ := range forwardedScope {
		if !notification.IsKnownType(typ) {
			t.Errorf("forwarded type %q is not declared in notification/types.go", typ)
		}
		if !IsEvent(typ) {
			t.Errorf("forwarded type %q is not a subscribable event", typ)
		}
	}
}

// TestUpdate_HostCheckOnlyWhenEndpointChanges: toggling an agent whose server
// no longer resolves still works; moving it re-runs the save-time check.
func TestUpdate_HostCheckOnlyWhenEndpointChanges(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	ctx := context.Background()
	a, err := s.Create(ctx, CreateInput{Kind: KindNtfy, Name: "N", Config: Config{ServerURL: "https://ntfy.example.com", Topic: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	s.lookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("nxdomain") }
	off := false
	same := Config{ServerURL: "https://ntfy.example.com/", Topic: "t"}
	if _, err := s.Update(ctx, a.ID, UpdateInput{Enabled: &off, Config: &same}); err != nil {
		t.Fatalf("disable with an unresolvable host: %v", err)
	}
	moved := Config{ServerURL: "https://other.example.com", Topic: "t"}
	_, err = s.Update(ctx, a.ID, UpdateInput{Config: &moved})
	mustValidation(t, err, "cannot resolve")
	s.lookupHost = func(context.Context, string) ([]string, error) { return []string{"10.1.1.1"}, nil }
	_, err = s.Update(ctx, a.ID, UpdateInput{Config: &moved})
	mustValidation(t, err, "private address")
}
