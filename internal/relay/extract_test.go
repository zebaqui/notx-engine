package relay

import (
	"testing"
)

func TestApplyExtractions_StatusCode(t *testing.T) {
	resp := &ResponseData{Status: 201}
	vars := map[string]string{}
	if err := ApplyExtractions(map[string]string{"code": "response.status"}, resp, vars); err != nil {
		t.Fatal(err)
	}
	if vars["code"] != "201" {
		t.Errorf("expected 201, got %q", vars["code"])
	}
}

func TestApplyExtractions_Header(t *testing.T) {
	resp := &ResponseData{
		Status:  200,
		Headers: map[string]string{"content-type": "application/json"},
	}
	vars := map[string]string{}
	err := ApplyExtractions(map[string]string{"ct": "response.headers.content-type"}, resp, vars)
	if err != nil {
		t.Fatal(err)
	}
	if vars["ct"] != "application/json" {
		t.Errorf("expected application/json, got %q", vars["ct"])
	}
}

func TestApplyExtractions_JSONBodyTopLevel(t *testing.T) {
	resp := &ResponseData{
		Status: 200,
		Body:   []byte(`{"access_token":"tok-xyz","expires_in":3600}`),
	}
	vars := map[string]string{}
	err := ApplyExtractions(map[string]string{
		"token":   "response.body.access_token",
		"expires": "response.body.expires_in",
	}, resp, vars)
	if err != nil {
		t.Fatal(err)
	}
	if vars["token"] != "tok-xyz" {
		t.Errorf("expected tok-xyz, got %q", vars["token"])
	}
	if vars["expires"] != "3600" {
		t.Errorf("expected 3600, got %q", vars["expires"])
	}
}

func TestApplyExtractions_NestedJSON(t *testing.T) {
	resp := &ResponseData{
		Status: 200,
		Body:   []byte(`{"data":{"user":{"id":"u-99"}}}`),
	}
	vars := map[string]string{}
	err := ApplyExtractions(map[string]string{"uid": "response.body.data.user.id"}, resp, vars)
	if err != nil {
		t.Fatal(err)
	}
	if vars["uid"] != "u-99" {
		t.Errorf("expected u-99, got %q", vars["uid"])
	}
}

func TestApplyExtractions_MissingKey(t *testing.T) {
	resp := &ResponseData{
		Status: 200,
		Body:   []byte(`{"foo":"bar"}`),
	}
	vars := map[string]string{}
	err := ApplyExtractions(map[string]string{"x": "response.body.missing"}, resp, vars)
	if err == nil {
		t.Error("expected error for missing key, got nil")
	}
	// Variable should be set to empty string on failure.
	if vars["x"] != "" {
		t.Errorf("expected empty string for failed extraction, got %q", vars["x"])
	}
}

func TestApplyExtractions_NonJSONBody(t *testing.T) {
	resp := &ResponseData{
		Status: 200,
		Body:   []byte(`not json`),
	}
	vars := map[string]string{}
	err := ApplyExtractions(map[string]string{"x": "response.body.key"}, resp, vars)
	if err == nil {
		t.Error("expected error for non-JSON body")
	}
}

func TestApplyExtractions_BoolValue(t *testing.T) {
	resp := &ResponseData{
		Status: 200,
		Body:   []byte(`{"ok":true}`),
	}
	vars := map[string]string{}
	if err := ApplyExtractions(map[string]string{"ok": "response.body.ok"}, resp, vars); err != nil {
		t.Fatal(err)
	}
	if vars["ok"] != "true" {
		t.Errorf("expected true, got %q", vars["ok"])
	}
}
