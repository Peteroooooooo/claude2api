package repository

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude2api/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAccountFilesBackfillUpdateAndDelete(t *testing.T) {
	authDir := filepath.Join(t.TempDir(), "auth")
	t.Setenv("CLAUDE2API_AUTH_DIR", authDir)
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "accounts.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	oldDB := db
	db = conn
	t.Cleanup(func() { _ = CloseDB(); db = oldDB })
	if err := db.AutoMigrate(&Account{}); err != nil {
		t.Fatal(err)
	}
	legacy := Account{Email: "legacy@example.test", Cookies: map[string]string{"sessionKey": "legacy-key"}, Status: "active"}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := SyncAuthFiles(); err != nil {
		t.Fatal(err)
	}
	checkFile := func(account Account) {
		t.Helper()
		data, err := os.ReadFile(accountFilePath(account.Email))
		if err != nil || strings.TrimSpace(string(data)) != string(AccountExportJSON(account)) {
			t.Fatalf("account file did not match export: %v", err)
		}
		if !json.Valid(data) {
			t.Fatal("invalid JSON file")
		}
	}
	checkFile(legacy)
	imported := Account{Email: "new@example.test", Cookies: map[string]string{"sessionKey": "new-key"}, Status: "active", ImportJSON: `{"sessionKey":"new-key","password":"keep","custom":{"value":9007199254740993}}`}
	if err := UpsertAccount(&imported); err != nil {
		t.Fatal(err)
	}
	checkFile(imported)
	imported.Cookies["sessionKey"] = "replacement-key"
	if err := UpsertAccount(&imported); err != nil {
		t.Fatal(err)
	}
	checkFile(imported)
	var replacement map[string]json.RawMessage
	if err := json.Unmarshal(AccountExportJSON(imported), &replacement); err != nil {
		t.Fatal(err)
	}
	if string(replacement["sessionKey"]) != `"replacement-key"` || string(replacement["password"]) != `"keep"` || !strings.Contains(string(replacement["custom"]), "9007199254740993") {
		t.Fatal("credential replacement lost source fields or kept stale key")
	}
	if !UpdateAccount(imported.Email, func(a *Account) { a.Status = "expired" }) {
		t.Fatal("update failed")
	}
	checkFile(*AccountByEmail(imported.Email))
	if !UpdateAccount(legacy.Email, func(a *Account) { a.Email = "renamed@example.test" }) {
		t.Fatal("rename failed")
	}
	if _, err := os.Stat(accountFilePath(legacy.Email)); !os.IsNotExist(err) {
		t.Fatal("old filename remains")
	}
	checkFile(*AccountByEmail("renamed@example.test"))
	if removed := DeleteAccountsByStatus([]string{"expired"}); len(removed) != 1 {
		t.Fatal("expired account not deleted")
	}
	if _, err := os.Stat(accountFilePath(imported.Email)); !os.IsNotExist(err) {
		t.Fatal("deleted credential remains on disk")
	}
	if count := DeleteAccount("renamed@example.test"); count != 1 {
		t.Fatal("account not deleted")
	}
	entries, err := os.ReadDir(authDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary or deleted files remain")
	}
	// A failed credential write must not report a successfully stored account.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE2API_AUTH_DIR", blocked)
	if err := UpsertAccount(&Account{Email: "failed@example.test", Cookies: map[string]string{"sessionKey": "failed"}}); err == nil {
		t.Fatal("ignored auth write failure")
	}
	if AccountByEmail("failed@example.test") != nil {
		t.Fatal("failed write committed to database")
	}
}

func TestAccountFilenamesStayInAuthDirectory(t *testing.T) {
	t.Setenv("CLAUDE2API_AUTH_DIR", t.TempDir())
	for _, email := range []string{"a@example.test", "../outside@example.test", `a\b:c?*@example.test`, "a%2Fb@example.test"} {
		path := accountFilePath(email)
		if filepath.Dir(path) != config.AuthDir() || strings.ContainsAny(filepath.Base(path), `\/:*?"<>|`) {
			t.Fatalf("unsafe account filename for %q", email)
		}
	}
	if filepath.Base(accountFilePath("a@example.test")) != "a@example.test.json" {
		t.Fatal("email filename is not readable")
	}
}
