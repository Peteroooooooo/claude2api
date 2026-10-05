package repository

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAccountJSONMigrationAndPersistence(t *testing.T) {
	t.Setenv("CLAUDE2API_AUTH_DIR", t.TempDir())
	dsn := filepath.Join(t.TempDir(), "accounts.db")
	open := func() *gorm.DB {
		t.Helper()
		conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	oldDB := db
	db = open()
	t.Cleanup(func() { _ = CloseDB(); db = oldDB })
	// Existing databases do not contain import_json.
	if err := db.Exec(`CREATE TABLE accounts (id integer PRIMARY KEY AUTOINCREMENT, email text NOT NULL, org_uuid text, cookies text, status text, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO accounts (email, org_uuid, cookies, status) VALUES (?, ?, ?, ?)`, "a@example.test", "org-test", `{"sessionKey":"original-key"}`, "active").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Account{}); err != nil {
		t.Fatal(err)
	}
	a := AccountByEmail("a@example.test")
	if a == nil || a.Cookies["sessionKey"] != "original-key" || a.ImportJSON != "" {
		t.Fatal("migration lost existing account")
	}
	a.ImportJSON = `{"sessionKey":"original-key","password":"preserved","custom":{"x":1}}`
	if err := UpsertAccount(a); err != nil {
		t.Fatal(err)
	}
	if !UpdateAccount(a.Email, func(account *Account) { account.Status = "expired" }) {
		t.Fatal("refresh update failed")
	}
	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}
	db = open()
	restored := AccountByEmail(a.Email)
	if restored == nil || restored.ImportJSON != a.ImportJSON || restored.Cookies["sessionKey"] != "original-key" || restored.Status != "expired" {
		t.Fatal("source JSON did not survive refresh and reopen")
	}
}
