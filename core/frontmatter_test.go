package core

import (
	"testing"
	"time"
)

func TestHasFrontMatter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "valid front matter",
			content: `---
title: Hello
---
# Body`,
			want: true,
		},
		{
			name:    "no front matter",
			content: "# Just a markdown heading\n\nSome content.",
			want:    false,
		},
		{
			name:    "empty content",
			content: "",
			want:    false,
		},
		{
			name:    "only dashes but not three",
			content: "--\ntitle: Hello\n--\n",
			want:    false,
		},
		{
			name:    "leading blank lines then front matter",
			content: "\n\n---\ntitle: Hello\n---\n",
			want:    true,
		},
		{
			name:    "four dashes is not front matter",
			content: "----\ntitle: Hello\n----\n",
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HasFrontMatter(tc.content)
			if got != tc.want {
				t.Errorf("HasFrontMatter() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseFrontMatter_NoFrontMatter(t *testing.T) {
	content := "# Hello\n\nJust some markdown."
	fm, body, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm != nil {
		t.Fatalf("expected nil FrontMatter, got %+v", fm)
	}
	if body != content {
		t.Errorf("body mismatch\ngot:  %q\nwant: %q", body, content)
	}
}

func TestParseFrontMatter_FullBlock(t *testing.T) {
	content := `---
id: post-001
title: Some Title
slug: some-title
description: Short summary
date: 2026-04-25
updated: 2026-04-26
author: Julio
tags:
  - engineering
  - go
status: published
draft: false
---

# Body heading

Some paragraph.`

	fm, body, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm == nil {
		t.Fatal("expected non-nil FrontMatter")
	}

	assertEqual(t, "ID", fm.ID, "post-001")
	assertEqual(t, "Title", fm.Title, "Some Title")
	assertEqual(t, "Slug", fm.Slug, "some-title")
	assertEqual(t, "Description", fm.Description, "Short summary")
	assertEqual(t, "Author", fm.Author, "Julio")
	assertEqual(t, "Status", fm.Status, "published")

	if fm.Draft {
		t.Error("Draft should be false")
	}

	wantDate := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if !fm.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", fm.Date, wantDate)
	}

	wantUpdated := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	if !fm.Updated.Equal(wantUpdated) {
		t.Errorf("Updated = %v, want %v", fm.Updated, wantUpdated)
	}

	wantTags := []string{"engineering", "go"}
	if len(fm.Tags) != len(wantTags) {
		t.Fatalf("Tags length = %d, want %d", len(fm.Tags), len(wantTags))
	}
	for i, tag := range wantTags {
		if fm.Tags[i] != tag {
			t.Errorf("Tags[%d] = %q, want %q", i, fm.Tags[i], tag)
		}
	}

	wantBody := "# Body heading\n\nSome paragraph."
	if body != wantBody {
		t.Errorf("body mismatch\ngot:  %q\nwant: %q", body, wantBody)
	}
}

func TestParseFrontMatter_DraftTrue(t *testing.T) {
	content := "---\ndraft: true\n---\n"
	fm, _, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm == nil {
		t.Fatal("expected non-nil FrontMatter")
	}
	if !fm.Draft {
		t.Error("Draft should be true")
	}
}

func TestParseFrontMatter_QuotedValues(t *testing.T) {
	content := "---\ntitle: \"Quoted Title\"\nauthor: 'Single Quoted'\n---\n"
	fm, _, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm == nil {
		t.Fatal("expected non-nil FrontMatter")
	}
	assertEqual(t, "Title", fm.Title, "Quoted Title")
	assertEqual(t, "Author", fm.Author, "Single Quoted")
}

func TestParseFrontMatter_ExtraFields(t *testing.T) {
	content := "---\ntitle: Hello\ncustom_field: my-value\n---\n"
	fm, _, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm == nil {
		t.Fatal("expected non-nil FrontMatter")
	}
	assertEqual(t, "Title", fm.Title, "Hello")
	if fm.Extra["custom_field"] != "my-value" {
		t.Errorf("Extra[custom_field] = %q, want %q", fm.Extra["custom_field"], "my-value")
	}
}

func TestParseFrontMatter_EmptyBody(t *testing.T) {
	content := "---\ntitle: No Body\n---\n"
	fm, body, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm == nil {
		t.Fatal("expected non-nil FrontMatter")
	}
	assertEqual(t, "Title", fm.Title, "No Body")
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestParseFrontMatter_OnlyTitle(t *testing.T) {
	content := "---\ntitle: Minimal\n---\nBody text."
	fm, body, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm == nil {
		t.Fatal("expected non-nil FrontMatter")
	}
	assertEqual(t, "Title", fm.Title, "Minimal")
	assertEqual(t, "body", body, "Body text.")
}

func TestParseFrontMatter_MultipleTags(t *testing.T) {
	content := "---\ntags:\n  - alpha\n  - beta\n  - gamma\n---\n"
	fm, _, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm == nil {
		t.Fatal("expected non-nil FrontMatter")
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(fm.Tags) != len(want) {
		t.Fatalf("Tags len = %d, want %d; tags = %v", len(fm.Tags), len(want), fm.Tags)
	}
	for i, tag := range want {
		if fm.Tags[i] != tag {
			t.Errorf("Tags[%d] = %q, want %q", i, fm.Tags[i], tag)
		}
	}
}

func TestParseFrontMatter_DateFormats(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Time
	}{
		{
			name:  "YYYY-MM-DD",
			input: "---\ndate: 2024-01-15\n---\n",
			want:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "RFC3339",
			input: "---\ndate: 2024-01-15T10:30:00Z\n---\n",
			want:  time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fm, _, err := ParseFrontMatter(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fm == nil {
				t.Fatal("expected non-nil FrontMatter")
			}
			if !fm.Date.Equal(tc.want) {
				t.Errorf("Date = %v, want %v", fm.Date, tc.want)
			}
		})
	}
}

func TestParseFrontMatter_UnclosedBlock(t *testing.T) {
	// When the closing "---" is missing, the whole content is treated as
	// front matter and the body is empty.
	content := "---\ntitle: Unclosed\nauthor: Someone\n"
	fm, body, err := ParseFrontMatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm == nil {
		t.Fatal("expected non-nil FrontMatter")
	}
	assertEqual(t, "Title", fm.Title, "Unclosed")
	assertEqual(t, "Author", fm.Author, "Someone")
	if body != "" {
		t.Errorf("expected empty body for unclosed block, got %q", body)
	}
}

// assertEqual is a small helper for string comparisons.
func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}
