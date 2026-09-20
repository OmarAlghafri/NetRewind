package notes

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func openTest(t *testing.T) *SQLite {
	t.Helper()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAnnotationRoundTripsAndUpsertKeepsCreatedAt(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	first := Annotation{
		IncidentID: "inc-1", Fingerprint: "fp-1", RuleID: "gateway-hijack",
		RootCauseKind: "l2.arp_binding_changed", RootCauseEntity: "10.99.0.1",
		OpenedAtNS: 1000, Outcome: OutcomeUnresolved, CauseNote: "still looking",
	}
	if err := s.PutAnnotation(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAnnotation(ctx, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected an annotation, got nil")
	}
	if got.Outcome != OutcomeUnresolved || got.CreatedAtMS == 0 || got.CreatedAtMS != got.UpdatedAtMS {
		t.Fatalf("first write = %+v, want Outcome=unresolved and CreatedAtMS==UpdatedAtMS", got)
	}
	firstCreated := got.CreatedAtMS

	// A second write to the same incident_id is an upsert: outcome changes,
	// but created_at_ms - the record's own identity-fixed-at-first-report
	// rule, same reasoning as internal/store/incident_sqlite.go's
	// AppendIncidents - must not move.
	second := first
	second.Outcome = OutcomeConfirmed
	second.CauseNote = "confirmed: rogue switch"
	if err := s.PutAnnotation(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetAnnotation(ctx, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != OutcomeConfirmed {
		t.Errorf("Outcome = %q, want confirmed after the second write", got.Outcome)
	}
	if got.CreatedAtMS != firstCreated {
		t.Errorf("CreatedAtMS = %d, want unchanged %d across the upsert", got.CreatedAtMS, firstCreated)
	}
	if got.UpdatedAtMS < firstCreated {
		t.Errorf("UpdatedAtMS = %d, want >= the original CreatedAtMS", got.UpdatedAtMS)
	}
}

func TestGetAnnotationForAnIncidentWithNoNoteReturnsNilNotError(t *testing.T) {
	s := openTest(t)
	got, err := s.GetAnnotation(context.Background(), "never-annotated")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestPutAnnotationRejectsAnInvalidOutcome(t *testing.T) {
	s := openTest(t)
	err := s.PutAnnotation(context.Background(), Annotation{IncidentID: "x", Outcome: "made-up"})
	if err == nil {
		t.Fatal("expected an error for an invalid outcome")
	}
}

func TestPutAnnotationClipsOverlongFields(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	long := strings.Repeat("a", MaxNoteFieldLen+500)
	if err := s.PutAnnotation(ctx, Annotation{IncidentID: "x", Outcome: OutcomeUnresolved, CauseNote: long}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAnnotation(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CauseNote) != MaxNoteFieldLen {
		t.Errorf("CauseNote length = %d, want clipped to %d", len(got.CauseNote), MaxNoteFieldLen)
	}
}

func TestDeleteAnnotationRemovesIt(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.PutAnnotation(ctx, Annotation{IncidentID: "x", Outcome: OutcomeUnresolved}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAnnotation(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAnnotation(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %+v after delete, want nil", got)
	}
}

func TestGetAnnotationsBulkLoadsByIDAndSkipsUnannotatedOnes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.PutAnnotation(ctx, Annotation{IncidentID: "a", Outcome: OutcomeConfirmed}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAnnotation(ctx, Annotation{IncidentID: "b", Outcome: OutcomeFalsePositive}); err != nil {
		t.Fatal(err)
	}
	// "c" is deliberately never annotated, and "z" is asked for but was
	// never even created - both must be silently absent from the result,
	// not an error.
	got, err := s.GetAnnotations(ctx, []string{"a", "b", "c", "z"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Annotation{}
	for _, a := range got {
		byID[a.IncidentID] = a
	}
	if len(got) != 2 {
		t.Fatalf("got %d annotations, want exactly 2 (a and b): %+v", len(got), got)
	}
	if byID["a"].Outcome != OutcomeConfirmed {
		t.Errorf("a.Outcome = %q, want confirmed", byID["a"].Outcome)
	}
	if byID["b"].Outcome != OutcomeFalsePositive {
		t.Errorf("b.Outcome = %q, want false_positive", byID["b"].Outcome)
	}
}

func TestGetAnnotationsReturnsNilForEmptyInputRatherThanQuerying(t *testing.T) {
	s := openTest(t)
	got, err := s.GetAnnotations(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

// TestSimilarOrdersSameEntityFirstThenSameKindThenNewestAndExcludesSelf is
// the exact ranking the approved plan specifies: tier 1 (same rule+kind+
// entity) before tier 2 (same rule+kind only), newest first within each
// tier, and the incident being analyzed right now never appears in its own
// history list.
func TestSimilarOrdersSameEntityFirstThenSameKindThenNewestAndExcludesSelf(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	put := func(id string, entity string, openedAt int64) {
		t.Helper()
		if err := s.PutAnnotation(ctx, Annotation{
			IncidentID: id, RuleID: "gateway-hijack", RootCauseKind: "l2.arp_binding_changed",
			RootCauseEntity: entity, OpenedAtNS: openedAt, Outcome: OutcomeConfirmed,
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("older-same-entity", "10.99.0.1", 1000)
	put("newer-same-entity", "10.99.0.1", 3000)
	put("same-kind-different-entity", "10.99.0.2", 5000) // newer in time, but a looser match
	put("target", "10.99.0.1", 9000)                     // the incident being analyzed - must be excluded

	got, err := s.Similar(ctx, "gateway-hijack", "l2.arp_binding_changed", "10.99.0.1", "target", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3 (self excluded): %+v", len(got), got)
	}
	if got[0].IncidentID != "newer-same-entity" {
		t.Errorf("got[0] = %s, want newer-same-entity (tier 1, newest)", got[0].IncidentID)
	}
	if got[1].IncidentID != "older-same-entity" {
		t.Errorf("got[1] = %s, want older-same-entity (tier 1, older)", got[1].IncidentID)
	}
	if got[2].IncidentID != "same-kind-different-entity" {
		t.Errorf("got[2] = %s, want same-kind-different-entity (tier 2, despite being newer in time)", got[2].IncidentID)
	}
}

func TestSimilarRespectsLimit(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.PutAnnotation(ctx, Annotation{
			IncidentID: string(rune('a' + i)), RuleID: "r", RootCauseKind: "k", RootCauseEntity: "e",
			OpenedAtNS: int64(i), Outcome: OutcomeConfirmed,
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Similar(ctx, "r", "k", "e", "none", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d results, want limit=2", len(got))
	}
}

func TestFeedbackIsCappedDroppingTheOldest(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	const total = MaxFeedback + 10
	for i := 0; i < total; i++ {
		f := Feedback{
			AnswerID: fmtID(i), IncidentID: "inc", Fingerprint: "fp", Profile: "balanced",
			ModelID: "m", Helpful: true, AtMS: int64(i),
		}
		if err := s.PutFeedback(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM answer_feedback`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != MaxFeedback {
		t.Errorf("feedback row count = %d, want capped at %d", count, MaxFeedback)
	}
	var minAt int64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(at_ms) FROM answer_feedback`).Scan(&minAt); err != nil {
		t.Fatal(err)
	}
	if minAt != 10 {
		t.Errorf("oldest surviving at_ms = %d, want 10 (the first 10 entries dropped)", minAt)
	}
}

func fmtID(i int) string { return "ans-" + strconv.Itoa(i) }

func TestHistoryOptInDefaultsToOff(t *testing.T) {
	s := openTest(t)
	on, err := s.HistoryOptIn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if on {
		t.Error("history opt-in defaulted to on; §13.2 requires off by default")
	}
}

func TestAppendThreadRefusesWhenHistoryIsOff(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	err := s.AppendThread(ctx, "inc", ThreadTurn{AtMS: 1, AnswerID: "a1", QuestionRedacted: "q", SummaryRedacted: "s"})
	if err != ErrHistoryDisabled {
		t.Errorf("err = %v, want ErrHistoryDisabled", err)
	}
	got, err := s.GetThread(ctx, "inc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("thread has %d turns, want 0 - nothing should have been stored", len(got))
	}
}

func TestAppendThreadWorksOnceOptedInAndCapsAtMaxTurns(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SetHistoryOptIn(ctx, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxThreadTurns+5; i++ {
		turn := ThreadTurn{AtMS: int64(i), AnswerID: fmtID(i), QuestionRedacted: "q", SummaryRedacted: "s"}
		if err := s.AppendThread(ctx, "inc", turn); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	got, err := s.GetThread(ctx, "inc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxThreadTurns {
		t.Fatalf("thread has %d turns, want capped at %d", len(got), MaxThreadTurns)
	}
	if got[0].AtMS != 5 {
		t.Errorf("oldest surviving turn AtMS = %d, want 5 (the first 5 dropped)", got[0].AtMS)
	}
}

// TestSetHistoryOptInFalseClearsStoredThreads pins "off means forgotten,
// not merely stop adding more" - the exact wording the approved plan uses.
func TestSetHistoryOptInFalseClearsStoredThreads(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SetHistoryOptIn(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendThread(ctx, "inc", ThreadTurn{AtMS: 1, AnswerID: "a", QuestionRedacted: "q", SummaryRedacted: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHistoryOptIn(ctx, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetThread(ctx, "inc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("thread has %d turns after opting out, want 0 (opting out must forget, not just stop appending)", len(got))
	}
}

func TestForgetAllClearsEverythingButNotTheOptInSetting(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.PutAnnotation(ctx, Annotation{IncidentID: "x", Outcome: OutcomeUnresolved}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFeedback(ctx, Feedback{AnswerID: "a", IncidentID: "x", Helpful: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHistoryOptIn(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendThread(ctx, "x", ThreadTurn{AtMS: 1, AnswerID: "a", QuestionRedacted: "q", SummaryRedacted: "s"}); err != nil {
		t.Fatal(err)
	}

	if err := s.ForgetAll(ctx); err != nil {
		t.Fatal(err)
	}

	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Annotations != 0 || stats.Feedback != 0 || stats.Threads != 0 {
		t.Errorf("stats after ForgetAll = %+v, want all zero", stats)
	}
	on, err := s.HistoryOptIn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Error("ForgetAll must not change the history opt-in setting itself")
	}
}

func TestStatsCountsAndReportsANonZeroFileSize(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.PutAnnotation(ctx, Annotation{IncidentID: "x", Outcome: OutcomeUnresolved}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Annotations != 1 {
		t.Errorf("Annotations = %d, want 1", stats.Annotations)
	}
	if stats.Bytes == 0 {
		t.Error("Bytes = 0, want the real on-disk file size")
	}
}
