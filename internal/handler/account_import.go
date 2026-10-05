package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"claude2api/internal/repository"

	"github.com/gin-gonic/gin"
)

type accountImport struct {
	SessionKey string
	Cookies    map[string]string
	JSON       string
}

// parseAccountImport accepts the existing key list and JSON account backups.
// Keep each original JSON object intact, including fields unknown to this app.
func parseAccountImport(text string) ([]accountImport, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	items := []accountImport{}
	if text == "" {
		return items, nil
	}
	if text[0] != '[' && text[0] != '{' && text[0] != '"' {
		for _, key := range strings.Fields(text) {
			items = append(items, accountImport{SessionKey: key})
		}
	} else {
		var root json.RawMessage
		if err := json.Unmarshal([]byte(text), &root); err != nil {
			return nil, fmt.Errorf("JSON 格式错误")
		}
		if root[0] == '{' {
			var object map[string]json.RawMessage
			_ = json.Unmarshal(root, &object)
			if accounts, ok := object["accounts"]; ok {
				root = accounts
				if len(root) == 0 || root[0] != '[' {
					return nil, fmt.Errorf("JSON 的 accounts 必须是账号数组")
				}
			}
		}
		rows := []json.RawMessage{root}
		if root[0] == '[' {
			if err := json.Unmarshal(root, &rows); err != nil {
				return nil, fmt.Errorf("JSON 账号数组格式错误")
			}
		}
		for index, row := range rows {
			var key string
			if json.Unmarshal(row, &key) == nil {
				if key = strings.TrimSpace(key); key == "" {
					return nil, fmt.Errorf("第 %d 个账号缺少 sessionKey", index+1)
				}
				items = append(items, accountImport{SessionKey: key})
				continue
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(row, &object); err != nil || object == nil {
				return nil, fmt.Errorf("第 %d 个账号必须是 JSON 对象或 sessionKey 字符串", index+1)
			}
			cookies := map[string]string{}
			if raw := object["cookies"]; len(raw) > 0 {
				if json.Unmarshal(raw, &cookies) != nil {
					var list []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					}
					cookies = map[string]string{}
					if err := json.Unmarshal(raw, &list); err != nil {
						return nil, fmt.Errorf("第 %d 个账号的 cookies 格式错误", index+1)
					}
					for _, cookie := range list {
						cookies[cookie.Name] = cookie.Value
					}
				}
			}
			for _, name := range []string{"sessionKey", "session_key"} {
				if json.Unmarshal(object[name], &key) == nil && strings.TrimSpace(key) != "" {
					break
				}
			}
			if strings.TrimSpace(key) == "" {
				key = cookies["sessionKey"]
			}
			if key = strings.TrimSpace(key); key == "" {
				return nil, fmt.Errorf("第 %d 个账号缺少 sessionKey", index+1)
			}
			items = append(items, accountImport{SessionKey: key, Cookies: cookies, JSON: string(row)})
		}
	}
	// Import each credential once; prefer a JSON record over a plain key.
	positions := map[string]int{}
	unique := []accountImport{}
	for _, item := range items {
		if index, ok := positions[item.SessionKey]; ok {
			if item.JSON != "" {
				unique[index] = item
			}
			continue
		}
		positions[item.SessionKey] = len(unique)
		unique = append(unique, item)
	}
	return unique, nil
}

func exportAccountJSON(accounts []repository.Account) []json.RawMessage {
	rows := make([]json.RawMessage, 0, len(accounts))
	for _, account := range accounts {
		rows = append(rows, repository.AccountExportJSON(account))
	}
	return rows
}

func AdminExportAccounts(c *gin.Context) {
	data, err := json.MarshalIndent(exportAccountJSON(repository.LoadAccounts()), "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "账号导出失败"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="claude2api-accounts-%s.json"`, time.Now().Format("20060102-150405")))
	c.Data(http.StatusOK, "application/json; charset=utf-8", data)
}
