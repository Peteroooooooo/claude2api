package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"claude2api/internal/config"
)

var accountFilesMu sync.Mutex

// AccountExportJSON is shared by the downloadable backup and auth files.
func AccountExportJSON(account Account) json.RawMessage {
	if account.ImportJSON != "" && json.Valid([]byte(account.ImportJSON)) {
		if key := account.Cookies["sessionKey"]; key != "" && key != importedSessionKey(account.ImportJSON) {
			// Keep source fields when a later key-only import replaces the login.
			// The canonical top-level credential takes precedence on reimport.
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(account.ImportJSON), &object) == nil && object != nil {
				object["sessionKey"], _ = json.Marshal(key)
				updated, _ := json.Marshal(object)
				return updated
			}
		}
		return json.RawMessage(account.ImportJSON)
	}
	row, _ := json.Marshal(struct {
		Email      string            `json:"email"`
		OrgUUID    string            `json:"org_uuid"`
		SessionKey string            `json:"sessionKey"`
		Cookies    map[string]string `json:"cookies"`
	}{account.Email, account.OrgUUID, account.Cookies["sessionKey"], account.Cookies})
	return row
}

func importedSessionKey(text string) string {
	var source struct {
		Key       string          `json:"sessionKey"`
		Alternate string          `json:"session_key"`
		Cookies   json.RawMessage `json:"cookies"`
	}
	if json.Unmarshal([]byte(text), &source) != nil {
		return ""
	}
	if strings.TrimSpace(source.Key) != "" {
		return strings.TrimSpace(source.Key)
	}
	if strings.TrimSpace(source.Alternate) != "" {
		return strings.TrimSpace(source.Alternate)
	}
	var cookies map[string]string
	if json.Unmarshal(source.Cookies, &cookies) == nil {
		return strings.TrimSpace(cookies["sessionKey"])
	}
	var list []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if json.Unmarshal(source.Cookies, &list) == nil {
		for _, cookie := range list {
			if cookie.Name == "sessionKey" {
				return strings.TrimSpace(cookie.Value)
			}
		}
	}
	return ""
}

func accountFilePath(email string) string {
	// Escape separators and Windows filename characters, keeping emails readable.
	name := strings.ReplaceAll(url.QueryEscape(email), "%40", "@") + ".json"
	return filepath.Join(config.AuthDir(), name)
}

func writeAccountFile(account *Account) error {
	if err := os.MkdirAll(config.AuthDir(), 0o700); err != nil {
		return fmt.Errorf("创建 auth 目录失败: %w", err)
	}
	data := AccountExportJSON(*account)
	file, err := os.CreateTemp(config.AuthDir(), ".account-*.tmp")
	if err != nil {
		return fmt.Errorf("创建账号文件失败: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入账号文件失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, accountFilePath(account.Email)); err != nil {
		return fmt.Errorf("保存账号文件失败: %w", err)
	}
	return nil
}

func removeAccountFile(email string) error {
	if err := os.Remove(accountFilePath(email)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除账号文件失败: %w", err)
	}
	return nil
}

// SyncAuthFiles backfills existing accounts when starting an upgraded server.
func SyncAuthFiles() error {
	accountFilesMu.Lock()
	defer accountFilesMu.Unlock()
	if err := os.MkdirAll(config.AuthDir(), 0o700); err != nil {
		return err
	}
	var accounts []Account
	if err := db.Order("id ASC").Find(&accounts).Error; err != nil {
		return err
	}
	for _, account := range accounts {
		if err := writeAccountFile(&account); err != nil {
			return err
		}
	}
	return nil
}
