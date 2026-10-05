package repository

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

type ChatSession struct {
	Key            string `gorm:"primaryKey"`
	Account        string
	OrgUUID        string
	ConversationID string
	ParentUUID     string
	Model          string
	Policy         string          `gorm:"type:text"`
	History        json.RawMessage `gorm:"type:text;serializer:json"`
	ReplyKey       string          `gorm:"type:text"`
	LastTurn       string
	UpdatedAt      time.Time `gorm:"index"`
}

type ChatTurn struct {
	Key            string `gorm:"primaryKey"`
	SessionKey     string `gorm:"index"`
	Status         string
	Account        string
	ConversationID string
	OrgUUID        string
	MessageUUID    string
	ParentUUID     string
	Response       string `gorm:"type:text"`
	Error          string `gorm:"type:text"`
	StatusCode     int
	InputBytes     int
	UpdatedAt      time.Time `gorm:"index"`
}

type AccountCooldown struct {
	Key     string `gorm:"primaryKey"`
	Account string
	Model   string
	Until   time.Time
	Reason  string
}

type ChatConversation struct {
	ID         string `gorm:"primaryKey"`
	SessionKey string `gorm:"index"`
	Account    string
	OrgUUID    string
	CreatedAt  time.Time
}

func SaveChatConversation(row ChatConversation) error { return db.Create(&row).Error }

func LoadChatSession(key string) (ChatSession, error) {
	var row ChatSession
	if db == nil {
		return row, errors.New("会话数据库未初始化")
	}
	err := db.First(&row, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ChatSession{Key: key}, nil
	}
	return row, err
}

func SaveChatSession(row *ChatSession) error {
	row.UpdatedAt = time.Now().UTC()
	return db.Save(row).Error
}

func LoadChatTurn(key string) (ChatTurn, bool, error) {
	var row ChatTurn
	err := db.First(&row, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

func SaveChatTurn(row *ChatTurn) error {
	row.UpdatedAt = time.Now().UTC()
	return db.Save(row).Error
}

// CommitChatTurn advances the parent and saves the replay response atomically.
func CommitChatTurn(session *ChatSession, turn *ChatTurn) error {
	session.UpdatedAt, turn.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(session).Error; err != nil {
			return err
		}
		return tx.Save(turn).Error
	})
}

func IdleChatSessions(before time.Time) ([]ChatSession, error) {
	var rows []ChatSession
	err := db.Where("updated_at < ?", before).Limit(100).Find(&rows).Error
	return rows, err
}

func DeleteChatSession(key string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_key = ?", key).Delete(&ChatTurn{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_key = ?", key).Delete(&ChatConversation{}).Error; err != nil {
			return err
		}
		return tx.Delete(&ChatSession{}, "key = ?", key).Error
	})
}

func GetAccountCooldown(account, model string) AccountCooldown {
	var row AccountCooldown
	_ = db.First(&row, "key = ?", account+"\n"+model).Error
	return row
}

func SetAccountCooldown(account, model, reason string, until time.Time) error {
	row := AccountCooldown{Key: account + "\n" + model, Account: account, Model: model, Reason: reason, Until: until}
	return db.Save(&row).Error
}

func SessionConversations(key string) ([]ChatConversation, error) {
	var rows []ChatConversation
	err := db.Where("session_key = ?", key).Find(&rows).Error
	return rows, err
}
