package staticabr

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

type fakePopular struct {
	items []Popular
	err   error
}

func (f fakePopular) TopPlayed(context.Context) ([]Popular, error) { return f.items, f.err }

// fakeResolver maps item→source; items absent from the map resolve ok=false.
type fakeResolver struct {
	byItem map[uuid.UUID]Source
}

func (f fakeResolver) Resolve(_ context.Context, itemID uuid.UUID) (Source, bool, error) {
	s, ok := f.byItem[itemID]
	return s, ok, nil
}

// recordEncoder records which files it was asked to encode.
type recordEncoder struct {
	encoded []uuid.UUID
	failOn  uuid.UUID
}

func (e *recordEncoder) Encode(_ context.Context, src Source) error {
	if src.FileID == e.failOn {
		return errors.New("encode boom")
	}
	e.encoded = append(e.encoded, src.FileID)
	return nil
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestService_RunOnce_EncodesPlanned(t *testing.T) {
	popItem, encodedItem, coldItem, unresolvable := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	popFile, encodedFile, coldFile := uuid.New(), uuid.New(), uuid.New()

	popular := fakePopular{items: []Popular{
		{ItemID: popItem, PlayCount: 100},     // resolvable, not encoded → encode
		{ItemID: encodedItem, PlayCount: 90},  // already encoded (hash match) → skip
		{ItemID: coldItem, PlayCount: 1},      // below minPlays → skip
		{ItemID: unresolvable, PlayCount: 80}, // no encodable file → skip
	}}
	resolver := fakeResolver{byItem: map[uuid.UUID]Source{
		popItem:     {FileID: popFile, Hash: "h1", Width: 1920, Height: 1080, BitrateKbps: 8000, Codec: "h264"},
		encodedItem: {FileID: encodedFile, Hash: "h2"},
		coldItem:    {FileID: coldFile, Hash: "h3"},
		// unresolvable intentionally absent → ok=false
	}}
	checker := fakeChecker{encoded: map[uuid.UUID]string{encodedFile: "h2"}}
	enc := &recordEncoder{}

	svc := NewService(popular, resolver, checker, enc, 10 /*minPlays*/, 0 /*no limit*/, discard())
	summary, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(enc.encoded) != 1 || enc.encoded[0] != popFile {
		t.Fatalf("encoded %v, want only %s", enc.encoded, popFile)
	}
	if summary == "" {
		t.Error("expected a non-empty summary")
	}
}

func TestService_RunOnce_EncodeFailureSkipsButContinues(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	fa, fb := uuid.New(), uuid.New()
	popular := fakePopular{items: []Popular{{ItemID: a, PlayCount: 50}, {ItemID: b, PlayCount: 40}}}
	resolver := fakeResolver{byItem: map[uuid.UUID]Source{
		a: {FileID: fa, Hash: "h"},
		b: {FileID: fb, Hash: "h"},
	}}
	enc := &recordEncoder{failOn: fa} // first encode fails

	svc := NewService(popular, resolver, fakeChecker{}, enc, 1, 0, discard())
	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce should not fail the pass on one encode error: %v", err)
	}
	// fa failed, fb still encoded.
	if len(enc.encoded) != 1 || enc.encoded[0] != fb {
		t.Errorf("encoded %v, want only %s (failure on the other shouldn't abort)", enc.encoded, fb)
	}
}

func TestService_RunOnce_PropagatesPopularityError(t *testing.T) {
	svc := NewService(fakePopular{err: errors.New("db down")}, fakeResolver{}, fakeChecker{}, &recordEncoder{}, 1, 0, discard())
	if _, err := svc.RunOnce(context.Background()); err == nil {
		t.Error("expected RunOnce to surface the popularity-source error")
	}
}

func TestService_RunOnce_RespectsLimit(t *testing.T) {
	var items []Popular
	byItem := map[uuid.UUID]Source{}
	for i := 0; i < 5; i++ {
		it, fi := uuid.New(), uuid.New()
		items = append(items, Popular{ItemID: it, PlayCount: 100})
		byItem[it] = Source{FileID: fi, Hash: "h"}
	}
	enc := &recordEncoder{}
	svc := NewService(fakePopular{items: items}, fakeResolver{byItem: byItem}, fakeChecker{}, enc, 1, 2 /*limit*/, discard())
	if _, err := svc.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(enc.encoded) != 2 {
		t.Errorf("encoded %d, want 2 (limit)", len(enc.encoded))
	}
}
