package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
	"github.com/zebaqui/notx-engine/repo/file"
	"github.com/zebaqui/notx-engine/repo/memory"
)

// ─────────────────────────────────────────────────────────────────────────────
// OTel bootstrap
// ─────────────────────────────────────────────────────────────────────────────

// initTracer sets up a file-backed OTel trace exporter that writes spans as
// pretty-printed JSON to traceFile. Uses SimpleSpanProcessor so every span is
// flushed synchronously — no spans are lost when the test exits.
// Returns a shutdown function the caller must defer.
func initTracer(t testing.TB, traceFile string) func() {
	t.Helper()

	f, err := os.Create(traceFile)
	if err != nil {
		t.Fatalf("create trace file %q: %v", traceFile, err)
	}

	exp, err := stdouttrace.New(
		stdouttrace.WithWriter(f),
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		f.Close()
		t.Fatalf("create stdouttrace exporter: %v", err)
	}

	// SimpleSpanProcessor flushes every span synchronously on End() so nothing
	// is buffered in memory when the test finishes.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	return func() {
		// Shutdown flushes and closes the exporter; close the file afterwards.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			t.Logf("tracer shutdown: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Logf("trace file close: %v", err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared workload
// ─────────────────────────────────────────────────────────────────────────────

// runWorkload exercises the full read/write path against the given provider:
//   - creates N notes
//   - appends M events per note (each with several line entries)
//   - reads every note back (Get)
//   - lists all notes
//   - fetches the event stream for every note
//
// It is used by both the benchmark functions and the profiled smoke run so the
// exact same code path is measured regardless of how the test is invoked.
func runWorkload(ctx context.Context, r repo.NoteRepository, notes, eventsPerNote int) error {
	urns := make([]string, notes)
	for i := 0; i < notes; i++ {
		urn := fmt.Sprintf("urn:notx:note:%08d-0000-0000-0000-000000000000", i)
		urns[i] = urn
		noteURN, err := core.ParseURN(urn)
		if err != nil {
			return fmt.Errorf("parse note urn: %w", err)
		}
		n := core.NewNote(noteURN, fmt.Sprintf("Bench Note %d", i), time.Now().UTC())

		if err := r.Create(ctx, n); err != nil {
			return fmt.Errorf("create note %d: %w", i, err)
		}
	}

	authorURN := core.AnonURN()

	for i, urn := range urns {
		noteURN, _ := core.ParseURN(urn)
		for seq := 1; seq <= eventsPerNote; seq++ {
			entries := []core.LineEntry{
				{Op: core.LineOpSet, LineNumber: 1, Content: fmt.Sprintf("# Note %d", i)},
				{Op: core.LineOpSet, LineNumber: 2, Content: fmt.Sprintf("Event sequence %d", seq)},
				{Op: core.LineOpSet, LineNumber: 3, Content: "Some body text for search indexing purposes."},
				{Op: core.LineOpSetEmpty, LineNumber: 4},
				{Op: core.LineOpSet, LineNumber: 5, Content: fmt.Sprintf("Updated at %s", time.Now().UTC().Format(time.RFC3339))},
			}
			event := &core.Event{
				NoteURN:   noteURN,
				Sequence:  seq,
				AuthorURN: authorURN,
				CreatedAt: time.Now().UTC(),
				Entries:   entries,
			}
			if err := r.AppendEvent(ctx, event, repo.AppendEventOptions{}); err != nil {
				return fmt.Errorf("append event note=%d seq=%d: %w", i, seq, err)
			}
		}
	}

	// Get every note (exercises file parse + journal replay).
	for _, urn := range urns {
		if _, err := r.Get(ctx, urn); err != nil {
			return fmt.Errorf("get %s: %w", urn, err)
		}
	}

	// List all notes (exercises Badger / in-memory index).
	if _, err := r.List(ctx, repo.ListOptions{PageSize: notes + 10}); err != nil {
		return fmt.Errorf("list: %w", err)
	}

	// Fetch event streams.
	for _, urn := range urns {
		if _, err := r.Events(ctx, urn, 1); err != nil {
			return fmt.Errorf("events %s: %w", urn, err)
		}
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Profiled smoke run  (go test -run TestBenchSmoke -v)
// ─────────────────────────────────────────────────────────────────────────────

// TestBenchSmoke runs the workload against both providers, emits a CPU profile
// to testdata/cpu_<provider>.prof and a trace to testdata/trace_<provider>.json
// so you can inspect them with:
//
//	go tool pprof -http=:6060 tests/smoke/testdata/cpu_file.prof
//	go tool pprof -http=:6060 tests/smoke/testdata/cpu_memory.prof
func TestBenchSmoke(t *testing.T) {
	const (
		notes         = 20
		eventsPerNote = 5
	)

	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}

	cases := []struct {
		name    string
		factory func(t *testing.T) (repo.NoteRepository, func())
	}{
		{
			name: "memory",
			factory: func(t *testing.T) (repo.NoteRepository, func()) {
				t.Helper()
				return memory.New(), func() {}
			},
		},
		{
			name: "file",
			factory: func(t *testing.T) (repo.NoteRepository, func()) {
				t.Helper()
				dir := t.TempDir()
				p, err := file.New(dir)
				if err != nil {
					t.Fatalf("file.New: %v", err)
				}
				return p, func() { p.Close() }
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			traceFile := filepath.Join("testdata", fmt.Sprintf("trace_%s.json", tc.name))
			shutdown := initTracer(t, traceFile)
			defer shutdown()

			r, cleanup := tc.factory(t)
			defer cleanup()

			// ── CPU profile ──────────────────────────────────────────────────
			profFile := filepath.Join("testdata", fmt.Sprintf("cpu_%s.prof", tc.name))
			pf, err := os.Create(profFile)
			if err != nil {
				t.Fatalf("create pprof file: %v", err)
			}
			defer pf.Close()

			if err := pprof.StartCPUProfile(pf); err != nil {
				t.Fatalf("start cpu profile: %v", err)
			}

			ctx := context.Background()
			start := time.Now()

			if err := runWorkload(ctx, r, notes, eventsPerNote); err != nil {
				pprof.StopCPUProfile()
				t.Fatalf("workload: %v", err)
			}

			pprof.StopCPUProfile()
			elapsed := time.Since(start)

			t.Logf("provider=%-8s  notes=%d  events/note=%d  total=%s",
				tc.name, notes, eventsPerNote, elapsed.Round(time.Microsecond))
			t.Logf("  cpu profile  → %s", profFile)
			t.Logf("  otel traces  → %s", traceFile)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Go benchmark functions  (go test -bench=. -benchmem)
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkProvider_Memory(b *testing.B) {
	benchProvider(b, func(b *testing.B) (repo.NoteRepository, func()) {
		b.Helper()
		return memory.New(), func() {}
	})
}

func BenchmarkProvider_File(b *testing.B) {
	benchProvider(b, func(b *testing.B) (repo.NoteRepository, func()) {
		b.Helper()
		dir := b.TempDir()
		p, err := file.New(dir)
		if err != nil {
			b.Fatalf("file.New: %v", err)
		}
		return p, func() { p.Close() }
	})
}

func benchProvider(b *testing.B, factory func(b *testing.B) (repo.NoteRepository, func())) {
	b.Helper()

	// Warm-up run outside the timer.
	{
		r, cleanup := factory(b)
		_ = runWorkload(context.Background(), r, 3, 2)
		cleanup()
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		r, cleanup := factory(b)
		b.StartTimer()

		if err := runWorkload(context.Background(), r, 5, 3); err != nil {
			b.Fatalf("workload: %v", err)
		}

		b.StopTimer()
		cleanup()
		b.StartTimer()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Focused micro-benchmarks
// ─────────────────────────────────────────────────────────────────────────────

// BenchmarkCreate isolates note creation (includes Badger write for file provider).
func BenchmarkCreate_Memory(b *testing.B) { benchCreate(b, newMemory(b)) }
func BenchmarkCreate_File(b *testing.B)   { benchCreate(b, newFile(b)) }

func benchCreate(b *testing.B, r repo.NoteRepository) {
	b.Helper()
	ns := "notx"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		urn := fmt.Sprintf("%s:note:%08d-0000-0000-0001-000000000000", ns, i)
		noteURN, _ := core.ParseURN(urn)
		n := core.NewNote(noteURN, fmt.Sprintf("Note %d", i), time.Now().UTC())
		if err := r.Create(context.Background(), n); err != nil {
			b.Fatalf("create: %v", err)
		}
	}
}

// BenchmarkAppendEvent isolates the hot event-append path.
func BenchmarkAppendEvent_Memory(b *testing.B) { benchAppendEvent(b, newMemory(b)) }
func BenchmarkAppendEvent_File(b *testing.B)   { benchAppendEvent(b, newFile(b)) }

func benchAppendEvent(b *testing.B, r repo.NoteRepository) {
	b.Helper()
	ns := "notx"
	urn := fmt.Sprintf("%s:note:aaaaaaaa-0000-0000-0000-000000000000", ns)
	noteURN, _ := core.ParseURN(urn)
	authorURN, _ := core.ParseURN(fmt.Sprintf("%s:usr:anon", ns))
	n := core.NewNote(noteURN, "Bench Note", time.Now().UTC())
	if err := r.Create(context.Background(), n); err != nil {
		b.Fatalf("create: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event := &core.Event{
			NoteURN:   noteURN,
			Sequence:  i + 1,
			AuthorURN: authorURN,
			CreatedAt: time.Now().UTC(),
			Entries: []core.LineEntry{
				{Op: core.LineOpSet, LineNumber: 1, Content: fmt.Sprintf("line %d", i)},
			},
		}
		if err := r.AppendEvent(context.Background(), event, repo.AppendEventOptions{}); err != nil {
			b.Fatalf("append event i=%d: %v", i, err)
		}
	}
}

// BenchmarkGet isolates the read path (parse + replay for file, map lookup for memory).
func BenchmarkGet_Memory(b *testing.B) { benchGet(b, newMemory(b)) }
func BenchmarkGet_File(b *testing.B)   { benchGet(b, newFile(b)) }

func benchGet(b *testing.B, r repo.NoteRepository) {
	b.Helper()
	ns := "notx"
	urn := fmt.Sprintf("%s:note:bbbbbbbb-0000-0000-0000-000000000000", ns)
	noteURN, _ := core.ParseURN(urn)
	authorURN, _ := core.ParseURN(fmt.Sprintf("%s:usr:anon", ns))

	n := core.NewNote(noteURN, "Bench Get Note", time.Now().UTC())
	if err := r.Create(context.Background(), n); err != nil {
		b.Fatalf("create: %v", err)
	}
	// Pre-load with 10 events so replay has meaningful work to do.
	for seq := 1; seq <= 10; seq++ {
		ev := &core.Event{
			NoteURN:   noteURN,
			Sequence:  seq,
			AuthorURN: authorURN,
			CreatedAt: time.Now().UTC(),
			Entries: []core.LineEntry{
				{Op: core.LineOpSet, LineNumber: seq, Content: fmt.Sprintf("content line %d", seq)},
			},
		}
		if err := r.AppendEvent(context.Background(), ev, repo.AppendEventOptions{}); err != nil {
			b.Fatalf("append event seq=%d: %v", seq, err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Get(context.Background(), urn); err != nil {
			b.Fatalf("get: %v", err)
		}
	}
}

// BenchmarkList isolates the list path (Badger scan vs in-memory map).
func BenchmarkList_Memory(b *testing.B) { benchList(b, newMemory(b)) }
func BenchmarkList_File(b *testing.B)   { benchList(b, newFile(b)) }

func benchList(b *testing.B, r repo.NoteRepository) {
	b.Helper()
	ns := "notx"
	const listSize = 50
	for i := 0; i < listSize; i++ {
		urn := fmt.Sprintf("%s:note:cccccccc-%04d-0000-0000-000000000000", ns, i)
		noteURN, _ := core.ParseURN(urn)
		n := core.NewNote(noteURN, fmt.Sprintf("List Note %d", i), time.Now().UTC())
		if err := r.Create(context.Background(), n); err != nil {
			b.Fatalf("create: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.List(context.Background(), repo.ListOptions{PageSize: listSize}); err != nil {
			b.Fatalf("list: %v", err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func newMemory(b interface {
	Helper()
	Fatalf(string, ...any)
}) repo.NoteRepository {
	b.Helper()
	return memory.New()
}

func newFile(b interface {
	Helper()
	Fatalf(string, ...any)
	TempDir() string
}) repo.NoteRepository {
	b.Helper()
	dir := b.TempDir()
	p, err := file.New(dir)
	if err != nil {
		b.Fatalf("file.New: %v", err)
	}
	return p
}

// ─────────────────────────────────────────────────────────────────────────────
// Trace summary printer  (go test -run TestPrintTraceSummary -v)
// ─────────────────────────────────────────────────────────────────────────────

// TestPrintTraceSummary reads the trace JSON files produced by TestBenchSmoke
// and prints a flat duration table so you can quickly see where time went
// without opening a UI.
//
// Run after TestBenchSmoke:
//
//	go test ./tests/smoke/ -run TestBenchSmoke     -v
//	go test ./tests/smoke/ -run TestPrintTraceSummary -v
func TestPrintTraceSummary(t *testing.T) {
	for _, provider := range []string{"memory", "file"} {
		path := filepath.Join("testdata", fmt.Sprintf("trace_%s.json", provider))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Logf("skip %s: %v", path, err)
			continue
		}

		type spanJSON struct {
			Name      string `json:"Name"`
			StartTime string `json:"StartTime"`
			EndTime   string `json:"EndTime"`
		}

		// The stdout exporter writes one JSON object per span separated by
		// newlines (with pretty-print it's multi-line per span, but each span
		// is a complete top-level object). Split on the object boundaries by
		// scanning for lines that start with '{' to find span starts.
		type row struct {
			name     string
			duration time.Duration
		}
		var rows []row

		lines := strings.Split(string(data), "\n")
		var buf strings.Builder
		depth := 0
		for _, line := range lines {
			for _, ch := range line {
				if ch == '{' {
					depth++
				} else if ch == '}' {
					depth--
				}
			}
			buf.WriteString(line)
			buf.WriteByte('\n')
			if depth == 0 && buf.Len() > 2 {
				var s spanJSON
				if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &s); err == nil && s.Name != "" {
					start, err1 := time.Parse(time.RFC3339Nano, s.StartTime)
					end, err2 := time.Parse(time.RFC3339Nano, s.EndTime)
					if err1 == nil && err2 == nil {
						rows = append(rows, row{name: s.Name, duration: end.Sub(start)})
					}
				}
				buf.Reset()
			}
		}

		if len(rows) == 0 {
			t.Logf("[%s] no spans parsed — run TestBenchSmoke first", provider)
			continue
		}

		// Aggregate by span name.
		type agg struct {
			count int
			total time.Duration
			max   time.Duration
		}
		aggMap := make(map[string]*agg)
		for _, r := range rows {
			a := aggMap[r.name]
			if a == nil {
				a = &agg{}
				aggMap[r.name] = a
			}
			a.count++
			a.total += r.duration
			if r.duration > a.max {
				a.max = r.duration
			}
		}

		t.Logf("\n── Trace summary: %s provider ──────────────────────────────", provider)
		t.Logf("  %-45s  %5s  %12s  %12s", "span", "calls", "total", "max")
		t.Logf("  %s", strings.Repeat("-", 80))
		for name, a := range aggMap {
			t.Logf("  %-45s  %5d  %12s  %12s",
				name, a.count,
				a.total.Round(time.Microsecond),
				a.max.Round(time.Microsecond),
			)
		}
	}
}
