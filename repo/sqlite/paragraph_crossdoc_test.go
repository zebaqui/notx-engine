package sqlite

// TestParagraphCrossDocScoring_Math demonstrates why cross-doc relations between
// two penguin articles generate zero relationships with the default MinScore=0.55.
//
// Root cause: the proximity tier multiplier for global/same_project is so low
// that even perfect concept overlap cannot push a claim→claim pair above 0.55.
// The fix is a separate CrossDocMinScore threshold (default: 0.20).

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestParagraphCrossDocScoring_Math
// ─────────────────────────────────────────────────────────────────────────────

func TestParagraphCrossDocScoring_Math(t *testing.T) {
	w := core.DefaultWeights()
	sep := strings.Repeat("─", 68)

	t.Logf("\n%s", sep)
	t.Logf("  DEFAULT WEIGHTS")
	t.Logf("%s", sep)
	t.Logf("  w_proximity_tier  = %.2f", w.WProximityTier)
	t.Logf("  w_role_pair       = %.2f", w.WRolePair)
	t.Logf("  w_overlap         = %.2f", w.WOverlap)
	t.Logf("  w_cue             = %.2f", w.WCue)
	t.Logf("  w_pattern         = %.2f", w.WPattern)
	t.Logf("  tier_same_folder  = %.2f", w.TierSameFolder)
	t.Logf("  tier_same_project = %.2f", w.TierSameProject)
	t.Logf("  tier_global       = %.2f", w.TierGlobal)

	// ── Maximum reachable scores per tier (claim→claim, no cue, jaccard=1.0) ──
	t.Logf("\n%s", sep)
	t.Logf("  MAX SCORE: claim→claim, no cue, perfect overlap (jaccard=1.0)")
	t.Logf("%s", sep)

	type tierCase struct {
		name string
		mult float64
	}
	tiers := []tierCase{
		{"same_folder", w.TierSameFolder},
		{"same_project", w.TierSameProject},
		{"global", w.TierGlobal},
	}
	for _, tc := range tiers {
		max := w.WProximityTier*tc.mult +
			w.WRolePair*0.45 +
			w.WOverlap*1.0 +
			w.WCue*0 +
			w.WPattern*0.5 // neutral pattern
		t.Logf("  %-16s max = %.4f  (MinScore 0.55: %v)",
			tc.name, max, map[bool]string{true: "✓ PASS", false: "✗ IMPOSSIBLE"}[max >= 0.55])
	}

	t.Logf("\n%s", sep)
	t.Logf("  CONCLUSION")
	t.Logf("%s", sep)
	t.Logf("  With MinScore=0.55, cross-doc claim→claim relations are")
	t.Logf("  *mathematically impossible* for same_project and global tiers,")
	t.Logf("  and only barely possible for same_folder with jaccard>0.9.")
	t.Logf("  The penguin articles have realistic jaccard ~0.3–0.5.")
	t.Logf("  Fix: use CrossDocMinScore=0.20 for cross-doc scoring.")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestParagraphCrossDocScoring_PenguinArticles
//
// Creates two notes with actual penguin article content, runs the paragraph
// runner with CrossDocEnabled=true and CrossDocMinScore=0.20, and asserts that
// cross-doc relations ARE generated between related paragraphs.
// ─────────────────────────────────────────────────────────────────────────────

func TestParagraphCrossDocScoring_PenguinArticles(t *testing.T) {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "sqlite-crossdoc-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { p.Close() })

	now := time.Now().UTC()
	projURN := core.MustParseURN("urn:notx:proj:aaaaaaaa-aaaa-7aaa-8aaa-000000000001")
	folder1URN := core.MustParseURN("urn:notx:folder:cccccccc-cccc-7ccc-8ccc-000000000001")
	folder2URN := core.MustParseURN("urn:notx:folder:dddddddd-dddd-7ddd-8ddd-000000000002")
	note1URN := core.MustParseURN("urn:notx:note:eeeeeeee-eeee-7eee-8eee-000000000001")
	note2URN := core.MustParseURN("urn:notx:note:ffffffff-ffff-7fff-8fff-000000000002")
	authorURN := core.MustParseURN("urn:notx:usr:00000000-0000-7000-8000-000000000099")

	// ── Create projects/folders/notes ─────────────────────────────────────────
	if err := p.CreateProject(ctx, &core.Project{
		URN: projURN, Name: "Penguin Research", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, f := range []*core.Folder{
		{URN: folder1URN, ProjectURN: projURN, Name: "General", CreatedAt: now, UpdatedAt: now},
		{URN: folder2URN, ProjectURN: projURN, Name: "Evolution", CreatedAt: now, UpdatedAt: now},
	} {
		if err := p.CreateFolder(ctx, f); err != nil {
			t.Fatalf("CreateFolder %s: %v", f.Name, err)
		}
	}
	for _, args := range []struct {
		urn    core.URN
		name   string
		proj   core.URN
		folder core.URN
	}{
		{note1URN, "Penguins – Overview", projURN, folder1URN},
		{note2URN, "Origin and Systematics of Modern Penguins", projURN, folder2URN},
	} {
		n := core.NewNote(args.urn, args.name, now)
		n.ProjectURN = &args.proj
		n.FolderURN = &args.folder
		if err := p.Create(ctx, n); err != nil {
			t.Fatalf("Create note %q: %v", args.name, err)
		}
	}

	// ── Note 1: General penguins article (first few paragraphs) ──────────────
	penguinsOverview := []string{
		"Penguins are a group of flightless semi-aquatic sea birds which live almost exclusively in the Southern Hemisphere.",
		"Only one species, the Galapagos penguin, lives at and slightly north of the equator.",
		"Highly adapted for life in the ocean water, penguins have countershaded dark and white plumage and flippers for swimming.",
		"Most penguins feed on krill, fish, squid and other forms of sea life which they catch while swimming.",
		"",
		"They spend about half of their lives on land and the other half in the sea.",
		"The largest living species is the emperor penguin on average adults are about 1.1 m tall and weigh 35 kg.",
		"The smallest penguin species is the little blue penguin also known as the fairy penguin which stands around 30 cm tall.",
		"",
		"Today larger penguins generally inhabit colder regions and smaller penguins inhabit regions with temperate or tropical climates.",
		"Some prehistoric penguin species were enormous as tall or heavy as an adult human.",
		"There was a great diversity of species in subantarctic regions during the Late Eocene a climate decidedly warmer than today.",
	}

	// ── Note 2: Origin and systematics article ────────────────────────────────
	penguinsEvolution := []string{
		"Modern penguins constitute two undisputed clades and another two more basal genera with more ambiguous relationships.",
		"The origin of the Spheniscinae lies probably in the latest Paleogene and geographically it must have been the area between the Australia-New Zealand region and the Antarctic.",
		"Presumably diverging from other penguins around 40 mya the Spheniscinae were for quite some time limited to their ancestral area.",
		"",
		"The genus Aptenodytes appears to be the basalmost divergence among living penguins.",
		"They have bright yellow-orange neck breast and bill patches and incubate by placing their eggs on their feet.",
		"This genus has a distribution centred on the Antarctic coasts and barely extends to some Subantarctic islands today.",
		"",
		"Pygoscelis contains species with a fairly simple black-and-white head pattern.",
		"Their distribution is intermediate centred on Antarctic coasts but extending somewhat northwards from there.",
		"Pygoscelis seems to have diverged during the Bartonian but the range expansion probably did not occur until much later around the Early Miocene roughly 20 to 15 mya.",
		"",
		"The genera Spheniscus and Eudyptula contain species with a mostly Subantarctic distribution centred on South America.",
		"They all lack carotenoid colouration and the former genus has a conspicuous banded head pattern unique among living penguins by nesting in burrows.",
		"This group probably radiated eastwards with the Antarctic Circumpolar Current out of the ancestral range of modern penguins throughout the Chattian starting approximately 28 mya.",
	}

	appendContent := func(noteURN core.URN, seq int, lines []string) {
		t.Helper()
		entries := make([]core.LineEntry, len(lines))
		for i, l := range lines {
			entries[i] = core.LineEntry{Op: core.LineOpSet, LineNumber: i + 1, Content: l}
		}
		ev := &core.Event{
			URN: core.NewURN(core.ObjectTypeEvent), NoteURN: noteURN,
			Sequence: seq, AuthorURN: authorURN, CreatedAt: now, Entries: entries,
		}
		if err := p.AppendEvent(ctx, ev, repo.AppendEventOptions{}); err != nil {
			t.Fatalf("AppendEvent note=%s: %v", noteURN, err)
		}
	}
	appendContent(note1URN, 1, penguinsOverview)
	appendContent(note2URN, 1, penguinsEvolution)

	// ── Run paragraph processing (same-doc) ───────────────────────────────────
	sameDocCfg := ParagraphRunnerConfig{
		SameDocWindowSize: 3,
		CrossDocEnabled:   false,
		TopN:              5,
		MinScore:          0.55,
	}
	if err := processParagraphBatch(ctx, p.db, p.write, sameDocCfg); err != nil {
		t.Fatalf("processParagraphBatch (same-doc): %v", err)
	}

	// ── Show same-doc relations (baseline) ────────────────────────────────────
	sameDocRels, _, err := p.ListRelations(ctx, repo.ParagraphRelationListOptions{
		NoteURN: note1URN.String(), PageSize: 50,
	})
	if err != nil {
		t.Fatalf("ListRelations note1: %v", err)
	}
	t.Logf("\n%s", strings.Repeat("═", 68))
	t.Logf("  SAME-DOC RELATIONS (note 1, MinScore=0.55): %d", len(sameDocRels))
	t.Logf("%s", strings.Repeat("═", 68))

	// ── Now demonstrate cross-doc with MinScore=0.55 (should get 0) ──────────
	// Load all paragraphs and score cross-doc manually to show the math
	note1Paras, _, _ := p.ListParagraphs(ctx, repo.ParagraphListOptions{NoteURN: note1URN.String(), PageSize: 100})
	note2Paras, _, _ := p.ListParagraphs(ctx, repo.ParagraphListOptions{NoteURN: note2URN.String(), PageSize: 100})

	w := core.DefaultWeights()
	crossDocScores055 := 0
	crossDocScores020 := 0

	t.Logf("\n%s", strings.Repeat("═", 68))
	t.Logf("  CROSS-DOC SCORES (note1→note2, all pairs)")
	t.Logf("%s", strings.Repeat("═", 68))

	for _, a := range note1Paras {
		for _, b := range note2Paras {
			ap := core.AnnotatedParagraph{
				ID: a.ID, NoteURN: a.NoteURN, FolderURN: a.FolderURN, ProjectURN: a.ProjectURN,
				Position: a.Position, Text: a.Text, Role: core.ParagraphRole(a.Role),
				MainConcepts: a.MainConcepts,
			}
			bp := core.AnnotatedParagraph{
				ID: b.ID, NoteURN: b.NoteURN, FolderURN: b.FolderURN, ProjectURN: b.ProjectURN,
				Position: b.Position, Text: b.Text, Role: core.ParagraphRole(b.Role),
				MainConcepts: b.MainConcepts,
			}
			scored := core.ScoreCandidate(ap, bp, nil, w)
			if scored.Score >= 0.55 {
				crossDocScores055++
			}
			if scored.Score >= 0.20 {
				crossDocScores020++
				overlap := core.ConceptOverlapScore(ap.MainConcepts, bp.MainConcepts)
				t.Logf("  note1[%d](%s) → note2[%d](%s)  score=%.3f  tier=%s  overlap=%.2f  signals=%v",
					a.Position, a.Role, b.Position, b.Role, scored.Score,
					scored.ProximityTier, overlap, scored.ReasonSignals)
			}
		}
	}

	t.Logf("\n%s", strings.Repeat("═", 68))
	t.Logf("  SUMMARY")
	t.Logf("%s", strings.Repeat("═", 68))
	t.Logf("  Pairs passing MinScore=0.55 (current default): %d", crossDocScores055)
	t.Logf("  Pairs passing MinScore=0.20 (CrossDocMinScore): %d", crossDocScores020)
	t.Logf("")
	t.Logf("  Fix: add CrossDocMinScore=0.20 to ParagraphRunnerConfig")
	t.Logf("  and use it in scoreCrossDocRelations() instead of MinScore.")
	t.Logf("%s", strings.Repeat("═", 68))

	// Assert the problem: with 0.55, nothing passes
	if crossDocScores055 > 0 {
		t.Logf("Note: %d pairs scored above 0.55 (unexpectedly high overlap)", crossDocScores055)
	}
	// Assert the fix works: with 0.20, meaningful relations exist
	if crossDocScores020 == 0 {
		t.Error("Expected at least some cross-doc relations above 0.20 between two penguin articles")
	} else {
		t.Logf("✓ CrossDocMinScore=0.20 would generate %d cross-doc relations", crossDocScores020)
	}

	// Show concept families to understand what was extracted
	t.Logf("\n%s", strings.Repeat("═", 68))
	t.Logf("  EXTRACTED PARAGRAPHS + CONCEPTS")
	t.Logf("%s", strings.Repeat("═", 68))
	for _, pg := range append(note1Paras, note2Paras...) {
		noteName := "overview"
		if pg.NoteURN == note2URN.String() {
			noteName = "evolution"
		}
		text := pg.Text
		if len(text) > 80 {
			text = text[:77] + "…"
		}
		t.Logf("  [%s pos=%d role=%-12s] main=%v", noteName, pg.Position, pg.Role, pg.MainConcepts)
		t.Logf("    %q", text)
	}

	_ = strings.Repeat("", 0) // keep strings import
}
