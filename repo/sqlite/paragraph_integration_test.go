package sqlite

// This file is package sqlite (not sqlite_test) so it can call unexported
// helpers like processParagraphBatch directly.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zebaqui/notx-engine/core"
	"github.com/zebaqui/notx-engine/repo"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestParagraphRoleSystem_TwoAnimalNotes
//
// Scenario
// --------
// Two notes about animals are created in *different* projects and *different*
// folders, then the paragraph processing batch is run synchronously.
//
// Note 1 — "Lions" (Project: Wildlife Science, Folder: African Mammals)
//
//	Four paragraphs covering: definition, example, contrast, cause-effect.
//
// Note 2 — "Dolphins" (Project: Marine Biology, Folder: Marine Mammals)
//
//	Five paragraphs covering: definition, example, question, cause-effect, contrast.
//
// Shared vocabulary ("mammal", "social", "predator", "cooperative") is enough
// to produce concept-overlap signals in the relation scorer.
//
// After processing the test prints every paragraph with its role + concepts,
// every scored relation with its signals and score, and the current global
// scoring weights.
// ─────────────────────────────────────────────────────────────────────────────
func TestParagraphRoleSystem_TwoAnimalNotes(t *testing.T) {
	ctx := context.Background()

	// ── Provider ──────────────────────────────────────────────────────────────
	dir := t.TempDir()
	p, err := New(dir, nil)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { p.Close() })

	// ── URNs ──────────────────────────────────────────────────────────────────
	proj1URN := core.MustParseURN("urn:notx:proj:aaaaaaaa-aaaa-7aaa-8aaa-000000000001")
	proj2URN := core.MustParseURN("urn:notx:proj:bbbbbbbb-bbbb-7bbb-8bbb-000000000002")

	folder1URN := core.MustParseURN("urn:notx:folder:cccccccc-cccc-7ccc-8ccc-000000000001")
	folder2URN := core.MustParseURN("urn:notx:folder:dddddddd-dddd-7ddd-8ddd-000000000002")

	note1URN := core.MustParseURN("urn:notx:note:eeeeeeee-eeee-7eee-8eee-000000000001")
	note2URN := core.MustParseURN("urn:notx:note:ffffffff-ffff-7fff-8fff-000000000002")

	authorURN := core.MustParseURN("urn:notx:usr:00000000-0000-7000-8000-000000000099")

	now := time.Now().UTC()

	// ── Create projects ───────────────────────────────────────────────────────
	for _, proj := range []*core.Project{
		{URN: proj1URN, Name: "Wildlife Science", CreatedAt: now, UpdatedAt: now},
		{URN: proj2URN, Name: "Marine Biology", CreatedAt: now, UpdatedAt: now},
	} {
		if err := p.CreateProject(ctx, proj); err != nil {
			t.Fatalf("CreateProject %q: %v", proj.Name, err)
		}
	}

	// ── Create folders ────────────────────────────────────────────────────────
	for _, f := range []*core.Folder{
		{URN: folder1URN, ProjectURN: proj1URN, Name: "African Mammals", CreatedAt: now, UpdatedAt: now},
		{URN: folder2URN, ProjectURN: proj2URN, Name: "Marine Mammals", CreatedAt: now, UpdatedAt: now},
	} {
		if err := p.CreateFolder(ctx, f); err != nil {
			t.Fatalf("CreateFolder %q: %v", f.Name, err)
		}
	}

	// ── Helpers ───────────────────────────────────────────────────────────────

	createNote := func(urn core.URN, name string, projURN, folURN core.URN) {
		t.Helper()
		n := core.NewNote(urn, name, now)
		n.ProjectURN = &projURN
		n.FolderURN = &folURN
		if err := p.Create(ctx, n); err != nil {
			t.Fatalf("Create note %q: %v", name, err)
		}
	}

	appendContent := func(noteURN core.URN, seq int, lines []string) {
		t.Helper()
		entries := make([]core.LineEntry, len(lines))
		for i, l := range lines {
			entries[i] = core.LineEntry{
				Op:         core.LineOpSet,
				LineNumber: i + 1,
				Content:    l,
			}
		}
		ev := &core.Event{
			URN:       core.NewURN(core.ObjectTypeEvent),
			NoteURN:   noteURN,
			Sequence:  seq,
			AuthorURN: authorURN,
			CreatedAt: now.Add(time.Duration(seq) * time.Millisecond),
			Entries:   entries,
		}
		if err := p.AppendEvent(ctx, ev, repo.AppendEventOptions{}); err != nil {
			t.Fatalf("AppendEvent note=%s seq=%d: %v", noteURN, seq, err)
		}
	}

	// ── Note 1 content: Lions ─────────────────────────────────────────────────
	//
	// Four paragraphs (separated by blank lines):
	//   [0] definition  — "A lion is…"
	//   [1] example     — "For example, a pride…"
	//   [2] contrast    — "However, unlike most big cats…"
	//   [3] cause_effect — "Because lions are apex predators…"
	//
	lionsContent := []string{
		// paragraph 0 — definition
		"A lion is a large carnivorous mammal of the family Felidae.",
		"The lion is defined as a social animal that lives in groups called prides.",
		"Lions are the apex predators of the African savanna and grassland ecosystem.",
		"",
		// paragraph 1 — example
		"For example, a pride of lions in the Serengeti can consist of up to thirty individuals.",
		"Such as in the Masai Mara, lions cooperate to hunt large mammals like wildebeest and buffalo.",
		"",
		// paragraph 2 — contrast
		"However, unlike most big cats, lions are highly social mammals that live in cooperative groups.",
		"Whereas tigers and leopards lead solitary lives, lions share territory and raise cubs together.",
		"",
		// paragraph 3 — cause_effect
		"Because lions are apex predators, they regulate the populations of herbivore mammals.",
		"This leads to a balanced ecosystem where vegetation can recover and support diverse animal species.",
	}

	// ── Note 2 content: Dolphins ──────────────────────────────────────────────
	//
	// Five paragraphs:
	//   [0] definition  — "A dolphin is…"
	//   [1] example     — "For instance…"
	//   [2] question    — "How do dolphins…?"
	//   [3] cause_effect — "Because dolphins are social mammals…"
	//   [4] contrast    — "However, unlike land mammals…"
	//
	dolphinsContent := []string{
		// paragraph 0 — definition
		"A dolphin is a highly intelligent marine mammal belonging to the order Cetacea.",
		"The dolphin is defined as a warm-blooded air-breathing animal that evolved from land ancestors.",
		"",
		// paragraph 1 — example
		"For instance, the bottlenose dolphin is one of the most studied marine mammals.",
		"Such as in Shark Bay Australia, dolphins use sponges as tools while foraging on the seafloor.",
		"",
		// paragraph 2 — question
		"How do dolphins communicate with each other across large ocean distances?",
		"What mechanisms allow dolphins to coordinate cooperative hunting and social behavior?",
		"",
		// paragraph 3 — cause_effect
		"Because dolphins are social mammals, they form complex pods and exhibit cooperative behaviors.",
		"This leads to sophisticated communication systems including clicks, whistles, and echolocation.",
		"",
		// paragraph 4 — contrast
		"However, unlike land mammals such as lions, dolphins face unique threats from ocean pollution.",
		"Whereas lions are threatened by habitat loss, dolphins suffer from fishing nets and sonar.",
	}

	// ── Create notes and append content ──────────────────────────────────────
	createNote(note1URN, "Lions: Apex Predators of the Savanna", proj1URN, folder1URN)
	createNote(note2URN, "Dolphins: Intelligent Marine Mammals", proj2URN, folder2URN)

	appendContent(note1URN, 1, lionsContent)
	appendContent(note2URN, 1, dolphinsContent)

	// ── Run paragraph processing synchronously ────────────────────────────────
	cfg := ParagraphRunnerConfig{
		SameDocWindowSize: 3,
		CrossDocEnabled:   false,
		TopN:              5,
		MinScore:          0.30, // lower threshold so we see more relations in output
	}
	if err := processParagraphBatch(ctx, p.db, p.write, cfg); err != nil {
		t.Fatalf("processParagraphBatch: %v", err)
	}

	// ─────────────────────────────────────────────────────────────────────────
	// OUTPUT: Paragraphs
	// ─────────────────────────────────────────────────────────────────────────

	allParagraphs, _, err := p.ListParagraphs(ctx, repo.ParagraphListOptions{PageSize: 100})
	if err != nil {
		t.Fatalf("ListParagraphs: %v", err)
	}

	t.Logf("\n%s", strings.Repeat("═", 72))
	t.Logf("  PARAGRAPHS  (%d total)", len(allParagraphs))
	t.Logf("%s", strings.Repeat("═", 72))

	for _, pg := range allParagraphs {
		// Resolve human-readable note name
		noteName := "Note 1 (Lions)"
		if pg.NoteURN == note2URN.String() {
			noteName = "Note 2 (Dolphins)"
		}
		t.Logf("\n  ┌─ [pos %d] %s", pg.Position, noteName)
		t.Logf("  │  id          : %s", pg.ID)
		t.Logf("  │  project_urn : %s", pg.ProjectURN)
		t.Logf("  │  folder_urn  : %s", pg.FolderURN)
		t.Logf("  │  role        : %s", pg.Role)
		t.Logf("  │  main        : [%s]", strings.Join(pg.MainConcepts, ", "))
		t.Logf("  │  supporting  : [%s]", strings.Join(pg.SupportingConcepts, ", "))
		t.Logf("  │  families    : [%s]", strings.Join(pg.ConceptFamilies, ", "))
		// Truncate text for readability
		text := pg.Text
		if len(text) > 100 {
			text = text[:97] + "…"
		}
		t.Logf("  └─ text        : %q", text)
	}

	// ─────────────────────────────────────────────────────────────────────────
	// OUTPUT: Relations
	// ─────────────────────────────────────────────────────────────────────────

	allRelations, _, err := p.ListRelations(ctx, repo.ParagraphRelationListOptions{PageSize: 100})
	if err != nil {
		t.Fatalf("ListRelations: %v", err)
	}

	// Build paragraph id→label map for readable output
	pgLabel := make(map[string]string)
	for _, pg := range allParagraphs {
		noteName := "lions"
		if pg.NoteURN == note2URN.String() {
			noteName = "dolphins"
		}
		pgLabel[pg.ID] = fmt.Sprintf("%s[%d](%s)", noteName, pg.Position, pg.Role)
	}

	t.Logf("\n%s", strings.Repeat("═", 72))
	t.Logf("  RELATIONS  (%d total)", len(allRelations))
	t.Logf("%s", strings.Repeat("═", 72))

	if len(allRelations) == 0 {
		t.Logf("  (no relations scored above threshold)")
	}
	for _, rel := range allRelations {
		src := pgLabel[rel.SourceParagraphID]
		tgt := pgLabel[rel.TargetParagraphID]
		if src == "" {
			src = rel.SourceParagraphID[:8] + "…"
		}
		if tgt == "" {
			tgt = rel.TargetParagraphID[:8] + "…"
		}
		t.Logf("\n  ┌─ %s  ──[%s]──▶  %s", src, rel.RelationType, tgt)
		t.Logf("  │  score         : %.4f", rel.Score)
		t.Logf("  │  proximity     : %s", rel.ProximityTier)
		t.Logf("  │  pattern_hash  : %s", rel.PatternHash)
		t.Logf("  │  signals       : [%s]", strings.Join(rel.ReasonSignals, ", "))
		t.Logf("  └─ version       : %s", rel.Version)
	}

	// ─────────────────────────────────────────────────────────────────────────
	// OUTPUT: Global Weights
	// ─────────────────────────────────────────────────────────────────────────

	weights, err := p.GetWeights(ctx)
	if err != nil {
		t.Fatalf("GetWeights: %v", err)
	}

	t.Logf("\n%s", strings.Repeat("═", 72))
	t.Logf("  GLOBAL WEIGHTS")
	t.Logf("%s", strings.Repeat("═", 72))
	t.Logf("  Signal dimensions:")
	t.Logf("    w_proximity_tier  = %.2f", weights.WProximityTier)
	t.Logf("    w_role_pair       = %.2f", weights.WRolePair)
	t.Logf("    w_overlap         = %.2f", weights.WOverlap)
	t.Logf("    w_cue             = %.2f", weights.WCue)
	t.Logf("    w_pattern         = %.2f", weights.WPattern)
	t.Logf("  Tier multipliers:")
	t.Logf("    tier_same_doc     = %.2f", weights.TierSameDoc)
	t.Logf("    tier_same_folder  = %.2f", weights.TierSameFolder)
	t.Logf("    tier_same_project = %.2f", weights.TierSameProject)
	t.Logf("    tier_global       = %.2f", weights.TierGlobal)

	// ─────────────────────────────────────────────────────────────────────────
	// Assertions
	// ─────────────────────────────────────────────────────────────────────────

	// Both notes should have produced paragraphs
	note1Paragraphs := 0
	note2Paragraphs := 0
	for _, pg := range allParagraphs {
		if pg.NoteURN == note1URN.String() {
			note1Paragraphs++
		} else if pg.NoteURN == note2URN.String() {
			note2Paragraphs++
		}
	}

	if note1Paragraphs == 0 {
		t.Error("expected at least 1 paragraph for Note 1 (Lions), got 0")
	}
	if note2Paragraphs == 0 {
		t.Error("expected at least 1 paragraph for Note 2 (Dolphins), got 0")
	}

	// project and folder metadata must be stored on each paragraph
	for _, pg := range allParagraphs {
		if pg.ProjectURN == "" {
			t.Errorf("paragraph %s missing project_urn", pg.ID)
		}
		if pg.FolderURN == "" {
			t.Errorf("paragraph %s missing folder_urn", pg.ID)
		}
	}

	// Every relation must reference paragraphs that exist
	pgIDs := make(map[string]bool, len(allParagraphs))
	for _, pg := range allParagraphs {
		pgIDs[pg.ID] = true
	}
	for _, rel := range allRelations {
		if !pgIDs[rel.SourceParagraphID] {
			t.Errorf("relation %s references unknown source paragraph %s", rel.ID, rel.SourceParagraphID)
		}
		if !pgIDs[rel.TargetParagraphID] {
			t.Errorf("relation %s references unknown target paragraph %s", rel.ID, rel.TargetParagraphID)
		}
		if rel.Score < 0 || rel.Score > 1 {
			t.Errorf("relation %s has score %.4f outside [0,1]", rel.ID, rel.Score)
		}
	}

	t.Logf("\n%s", strings.Repeat("═", 72))
	t.Logf("  SUMMARY")
	t.Logf("%s", strings.Repeat("═", 72))
	t.Logf("  Note 1 (Lions)    — %d paragraphs", note1Paragraphs)
	t.Logf("  Note 2 (Dolphins) — %d paragraphs", note2Paragraphs)
	t.Logf("  Total paragraphs  — %d", len(allParagraphs))
	t.Logf("  Total relations   — %d", len(allRelations))
	t.Logf("%s", strings.Repeat("═", 72))
}
