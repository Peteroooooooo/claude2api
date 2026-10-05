package handler

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"claude2api/internal/repository"
)

func TestAccountImportAndExportRoundTrip(t *testing.T) {
	original := `{"email":"a@example.test","sessionKey":"test-key-a","password":"saved-password","notes":{"tag":"原始字段","large":9007199254740993},"cookies":{"sessionKey":"test-key-a","extra":"keep-me"}}`
	items, err := parseAccountImport(`{"accounts":[` + original + `,{"session_key":"test-key-b"}]}`)
	if err != nil || len(items) != 2 {
		t.Fatalf("parse failed: %v, count=%d", err, len(items))
	}
	if items[0].JSON != original || items[0].Cookies["extra"] != "keep-me" || items[1].SessionKey != "test-key-b" {
		t.Fatal("original JSON or cookies lost")
	}
	accounts := []repository.Account{
		{Email: "a@example.test", Cookies: items[0].Cookies, ImportJSON: items[0].JSON},
		{Email: "legacy@example.test", OrgUUID: "org-old", Cookies: map[string]string{"sessionKey": "test-key-old"}},
	}
	rows := exportAccountJSON(accounts)
	if string(rows[0]) != original {
		t.Fatal("export changed original fields")
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := parseAccountImport(string(data))
	if err != nil || len(restored) != 2 || restored[1].SessionKey != "test-key-old" {
		t.Fatalf("export cannot be reimported: %v", err)
	}
	var before, after any
	// Decode with UseNumber so a large integer is checked without float rounding.
	for _, pair := range []struct {
		text string
		dest *any
	}{{original, &before}, {restored[0].JSON, &after}} {
		decoder := json.NewDecoder(strings.NewReader(pair.text))
		decoder.UseNumber()
		if err := decoder.Decode(pair.dest); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("round trip lost source fields")
	}
	public, _ := json.Marshal(accounts[0])
	if strings.Contains(string(public), "saved-password") || strings.Contains(string(public), "test-key-a") {
		t.Fatal("source credentials exposed in ordinary account JSON")
	}
	if empty, _ := json.Marshal(exportAccountJSON(nil)); string(empty) != "[]" {
		t.Fatalf("empty export=%s", empty)
	}
}

func TestAccountImportFormatsAndValidation(t *testing.T) {
	for _, text := range []string{
		"test-key-a\ntest-key-a test-key-b",
		`["test-key-a","test-key-b","test-key-a"]`,
		`[{"cookies":[{"name":"sessionKey","value":"test-key-a"}]},{"cookies":{"sessionKey":"test-key-b"}}]`,
		"\ufeff" + `[{"sessionKey":"test-key-a"},{"session_key":"test-key-b"}]`,
	} {
		items, err := parseAccountImport(text)
		if err != nil || len(items) != 2 || items[0].SessionKey != "test-key-a" || items[1].SessionKey != "test-key-b" {
			t.Errorf("unexpected parsing for %q: %v", text, err)
		}
	}
	items, err := parseAccountImport(`["test-key-a",{"sessionKey":"test-key-a","remark":"preserve"},"test-key-a"]`)
	if err != nil || len(items) != 1 || !strings.Contains(items[0].JSON, "preserve") {
		t.Fatal("dedup discarded source record")
	}
	for _, invalid := range []string{`[{`, `{"accounts":null}`, `[null]`, `[{"email":"a@example.test"}]`, `[""]`, `[{"cookies":42}]`} {
		if _, err := parseAccountImport(invalid); err == nil {
			t.Errorf("accepted invalid input %s", invalid)
		}
	}
}
