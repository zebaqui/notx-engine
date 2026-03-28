package index

import (
	"encoding/json"
	"fmt"
	"strings"

	badger "github.com/dgraph-io/badger/v4"
)

// ─────────────────────────────────────────────────────────────────────────────
// Key schema
//
//  All keys are plain []byte strings with a structured prefix so that Badger's
//  prefix iterator can scan a namespace efficiently.
//
//  note:<urn>              → JSON-encoded IndexEntry
//  search:<token>:<urn>    → empty value (presence = match)
//  name:<urn>              → note name string (for fast header loads)
// ─────────────────────────────────────────────────────────────────────────────

const (
	prefixNote   = "note:"
	prefixSearch = "search:"
	prefixName   = "name:"
)

func noteKey(urn string) []byte {
	return []byte(prefixNote + urn)
}

func nameKey(urn string) []byte {
	return []byte(prefixName + urn)
}

func searchKey(token, urn string) []byte {
	return []byte(prefixSearch + token + ":" + urn)
}

// ─────────────────────────────────────────────────────────────────────────────
// IndexEntry — the metadata record stored per note
// ─────────────────────────────────────────────────────────────────────────────

// IndexEntry is the record the index stores for each note.
// It mirrors NoteHeader fields so callers can reconstruct list responses
// without touching the file layer.
type IndexEntry struct {
	URN        string `json:"urn"`
	Name       string `json:"name"`
	NoteType   string `json:"note_type"`   // "normal" | "secure"
	ProjectURN string `json:"project_urn"` // empty if absent
	FolderURN  string `json:"folder_urn"`  // empty if absent
	Deleted    bool   `json:"deleted"`
	CreatedAt  string `json:"created_at"` // RFC3339
	UpdatedAt  string `json:"updated_at"` // RFC3339
}

// ─────────────────────────────────────────────────────────────────────────────
// Index
// ─────────────────────────────────────────────────────────────────────────────

// Index is a Badger-backed persistent index for note metadata and full-text
// search over normal note content.
//
// Security guarantee: secure notes (NoteType == "secure") are NEVER indexed for
// search. Their IndexEntry is stored (so list operations work), but no search
// tokens are written for them. This is a hard rule enforced at every write path.
//
// Index is safe for concurrent use — Badger handles its own internal locking.
type Index struct {
	db *badger.DB
}

// Open opens (or creates) a Badger database at the given directory path and
// returns a ready-to-use Index. The caller must call Close when done.
func Open(dir string) (*Index, error) {
	opts := badger.DefaultOptions(dir)
	opts.Logger = nil // silence Badger's internal logger; integrate via structured log if needed

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("index: open badger at %q: %w", dir, err)
	}
	return &Index{db: db}, nil
}

// Close releases all resources held by the index.
func (idx *Index) Close() error {
	if err := idx.db.Close(); err != nil {
		return fmt.Errorf("index: close: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Write operations
// ─────────────────────────────────────────────────────────────────────────────

// Upsert stores or updates the IndexEntry for a note.
//
// For normal notes, content (if non-empty) is tokenised and written as search
// keys so subsequent calls to Search return the note.
//
// For secure notes, only the IndexEntry metadata record is written. No search
// tokens are ever written regardless of what content contains.
func (idx *Index) Upsert(entry IndexEntry, content string) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("index: marshal entry for %q: %w", entry.URN, err)
	}

	return idx.db.Update(func(txn *badger.Txn) error {
		// Always write the metadata record.
		if err := txn.Set(noteKey(entry.URN), data); err != nil {
			return fmt.Errorf("index: write note key: %w", err)
		}
		// Always write the name record for fast header lookups.
		if err := txn.Set(nameKey(entry.URN), []byte(entry.Name)); err != nil {
			return fmt.Errorf("index: write name key: %w", err)
		}

		// Security: NEVER index content for secure notes.
		if entry.NoteType == "secure" {
			return nil
		}

		// Normal notes: tokenise and write search keys.
		for _, token := range tokenise(content) {
			if err := txn.Set(searchKey(token, entry.URN), []byte{}); err != nil {
				return fmt.Errorf("index: write search key %q: %w", token, err)
			}
		}
		return nil
	})
}

// Delete removes all index records associated with the given note URN.
// This includes the metadata record, name record, and any search tokens.
// It is safe to call Delete on a URN that does not exist.
func (idx *Index) Delete(urn string) error {
	return idx.db.Update(func(txn *badger.Txn) error {
		// Remove the metadata and name records.
		_ = txn.Delete(noteKey(urn))
		_ = txn.Delete(nameKey(urn))

		// Collect and remove all search keys for this note.
		searchKeys, err := collectSearchKeysForURN(txn, urn)
		if err != nil {
			return err
		}
		for _, k := range searchKeys {
			if err := txn.Delete(k); err != nil {
				return fmt.Errorf("index: delete search key: %w", err)
			}
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Read operations
// ─────────────────────────────────────────────────────────────────────────────

// Get returns the IndexEntry for the given note URN.
// Returns (nil, nil) if the note is not in the index.
func (idx *Index) Get(urn string) (*IndexEntry, error) {
	var entry IndexEntry
	found := false

	err := idx.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(noteKey(urn))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return fmt.Errorf("index: get note key: %w", err)
		}
		return item.Value(func(val []byte) error {
			found = true
			return json.Unmarshal(val, &entry)
		})
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &entry, nil
}

// ListOptions controls filtering and pagination for List.
type ListOptions struct {
	// ProjectURN filters by project; empty = no filter.
	ProjectURN string
	// FolderURN filters by folder; empty = no filter.
	FolderURN string
	// NoteType filters by type ("normal", "secure", or "" for all).
	NoteType string
	// IncludeDeleted includes soft-deleted notes when true.
	IncludeDeleted bool
	// PageSize is the maximum number of results to return. 0 = no limit.
	PageSize int
	// PageToken is an opaque cursor returned by a previous List call.
	PageToken string
}

// List returns all IndexEntries that match the given options, ordered by URN.
// Returns a next-page token if there are more results; empty string on last page.
func (idx *Index) List(opts ListOptions) ([]IndexEntry, string, error) {
	var results []IndexEntry

	err := idx.db.View(func(txn *badger.Txn) error {
		iterOpts := badger.DefaultIteratorOptions
		iterOpts.Prefix = []byte(prefixNote)

		it := txn.NewIterator(iterOpts)
		defer it.Close()

		// Seek past the page token if provided.
		startKey := []byte(prefixNote)
		if opts.PageToken != "" {
			startKey = []byte(prefixNote + opts.PageToken)
		}

		for it.Seek(startKey); it.Valid(); it.Next() {
			item := it.Item()
			var entry IndexEntry
			if err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &entry)
			}); err != nil {
				return fmt.Errorf("index: unmarshal entry: %w", err)
			}

			// Apply filters.
			if !opts.IncludeDeleted && entry.Deleted {
				continue
			}
			if opts.ProjectURN != "" && entry.ProjectURN != opts.ProjectURN {
				continue
			}
			if opts.FolderURN != "" && entry.FolderURN != opts.FolderURN {
				continue
			}
			if opts.NoteType != "" && entry.NoteType != opts.NoteType {
				continue
			}

			// Pagination: skip the token entry itself (already seen).
			if opts.PageToken != "" && entry.URN == opts.PageToken {
				continue
			}

			results = append(results, entry)

			if opts.PageSize > 0 && len(results) >= opts.PageSize {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	// Determine the next page token.
	nextToken := ""
	if opts.PageSize > 0 && len(results) == opts.PageSize {
		nextToken = results[len(results)-1].URN
	}
	return results, nextToken, nil
}

// Search performs a tokenised full-text search over normal note content.
//
// Secure notes are structurally excluded from the search index, so they can
// never appear in results regardless of the query.
//
// It returns at most maxResults matching URNs. Pass 0 for no limit.
func (idx *Index) Search(query string, maxResults int) ([]string, error) {
	tokens := tokenise(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	// For multi-token queries we collect the candidate set from the first token
	// then intersect with each subsequent token.
	sets := make([]map[string]struct{}, 0, len(tokens))

	err := idx.db.View(func(txn *badger.Txn) error {
		for _, token := range tokens {
			set := make(map[string]struct{})
			prefix := []byte(prefixSearch + token + ":")

			iterOpts := badger.DefaultIteratorOptions
			iterOpts.Prefix = prefix
			iterOpts.PrefetchValues = false // keys only

			it := txn.NewIterator(iterOpts)
			for it.Rewind(); it.Valid(); it.Next() {
				// Key format: search:<token>:<urn>
				key := string(it.Item().KeyCopy(nil))
				parts := strings.SplitN(key, ":", 3) // ["search", token, urn]
				if len(parts) == 3 {
					set[parts[2]] = struct{}{}
				}
			}
			it.Close()

			sets = append(sets, set)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("index: search: %w", err)
	}

	// Intersect all token sets.
	result := sets[0]
	for _, s := range sets[1:] {
		intersected := make(map[string]struct{})
		for urn := range result {
			if _, ok := s[urn]; ok {
				intersected[urn] = struct{}{}
			}
		}
		result = intersected
	}

	// Collect results, respecting maxResults.
	urns := make([]string, 0, len(result))
	for urn := range result {
		urns = append(urns, urn)
		if maxResults > 0 && len(urns) >= maxResults {
			break
		}
	}
	return urns, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Tokenisation
// ─────────────────────────────────────────────────────────────────────────────

// tokenise splits text into lowercase, de-duplicated, non-empty tokens.
// It strips all non-alphanumeric runes and ignores tokens shorter than 2 chars
// to avoid extremely common single-character noise terms.
func tokenise(text string) []string {
	// Normalise to lowercase and replace non-alphanumeric runs with spaces.
	var sb strings.Builder
	for _, r := range strings.ToLower(text) {
		if isAlphaNum(r) {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ')
		}
	}

	words := strings.Fields(sb.String())
	seen := make(map[string]struct{}, len(words))
	out := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 2 {
			continue
		}
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9')
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// collectSearchKeysForURN scans all search:*:<urn> keys and returns them.
// Used during Delete to clean up all search tokens for a note.
func collectSearchKeysForURN(txn *badger.Txn, urn string) ([][]byte, error) {
	suffix := ":" + urn
	prefix := []byte(prefixSearch)

	iterOpts := badger.DefaultIteratorOptions
	iterOpts.Prefix = prefix
	iterOpts.PrefetchValues = false

	it := txn.NewIterator(iterOpts)
	defer it.Close()

	var keys [][]byte
	for it.Rewind(); it.Valid(); it.Next() {
		k := it.Item().KeyCopy(nil)
		if strings.HasSuffix(string(k), suffix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}
