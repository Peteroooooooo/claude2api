package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Account 是一条账号记录。
type Account struct {
	ID               uint              `json:"-" gorm:"primaryKey"`
	Email            string            `json:"email" gorm:"uniqueIndex;not null"`
	OrgUUID          string            `json:"org_uuid" gorm:"column:org_uuid"`
	Cookies          map[string]string `json:"-" gorm:"serializer:json"`
	ImportJSON       string            `json:"-" gorm:"type:text"`
	Status           string            `json:"status,omitempty"`
	SubscriptionPlan string            `json:"subscription_plan"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Usage            *AccountUsage     `json:"usage" gorm:"serializer:json;type:text"`
	UsageUpdatedAt   *time.Time        `json:"usage_updated_at"`
	Models           []AccountModel    `json:"models" gorm:"serializer:json;type:text"`
	ModelsUpdatedAt  *time.Time        `json:"models_updated_at"`
}

type AccountModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Thinking      bool   `json:"thinking"`
	ContextWindow int    `json:"context_window"`
}

// AccountUsage 保存最近一次对话返回的额度窗口。
type AccountUsage struct {
	Windows map[string]UsageWindow `json:"windows"`
}

type UsageWindow struct {
	Status      string  `json:"status"`
	ResetsAt    int64   `json:"resets_at"`
	Utilization float64 `json:"utilization"`
}

// LoadAccounts 读取全部账号。
func LoadAccounts() []Account {
	var out []Account
	if err := db.Order("id ASC").Find(&out).Error; err != nil {
		return []Account{}
	}
	return out
}

// AccountByEmail 按邮箱取账号。
func AccountByEmail(email string) *Account {
	if email == "" {
		return nil
	}
	var a Account
	if db.Where("email = ?", email).First(&a).Error != nil {
		return nil
	}
	return &a
}

// UpsertAccount 写入或更新账号；额度字段由 UpdateAccountUsage 单独维护。
func UpsertAccount(a *Account) error {
	accountFilesMu.Lock()
	defer accountFilesMu.Unlock()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "email"}},
			DoUpdates: clause.AssignmentColumns([]string{"org_uuid", "cookies", "import_json", "status", "subscription_plan", "updated_at", "models", "models_updated_at"}),
		}).Create(a).Error; err != nil {
			return err
		}
		return writeAccountFile(a)
	})
}

// UpdateAccount 修改指定账号。
func UpdateAccount(email string, mutate func(*Account)) bool {
	accountFilesMu.Lock()
	defer accountFilesMu.Unlock()
	a := AccountByEmail(email)
	if a == nil {
		return false
	}
	mutate(a)
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Usage", "UsageUpdatedAt").Save(a).Error; err != nil {
			return err
		}
		if err := writeAccountFile(a); err != nil {
			return err
		}
		if a.Email != email {
			return removeAccountFile(email)
		}
		return nil
	}) == nil
}

// UpdateAccountUsage 只更新额度，避免与账号刷新互相覆盖。
func UpdateAccountUsage(email string, usage *AccountUsage, capturedAt time.Time) error {
	return db.Model(&Account{}).
		Where("email = ? AND (usage_updated_at IS NULL OR usage_updated_at < ?)", email, capturedAt).
		Select("Usage", "UsageUpdatedAt").
		UpdateColumns(&Account{Usage: usage, UsageUpdatedAt: &capturedAt}).Error
}

// DeleteAccount 删除账号。
func DeleteAccount(email string) int {
	accountFilesMu.Lock()
	defer accountFilesMu.Unlock()
	var removed int64
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("email = ?", email).Delete(&Account{})
		if res.Error != nil {
			return res.Error
		}
		removed = res.RowsAffected
		if removed > 0 {
			return removeAccountFile(email)
		}
		return nil
	})
	if err != nil {
		return 0
	}
	return int(removed)
}

// DeleteAccountsByStatus 按状态删除账号。
func DeleteAccountsByStatus(statuses []string) []string {
	removed := make([]string, 0)
	for _, a := range LoadAccounts() {
		for _, s := range statuses {
			if a.Status == s {
				if DeleteAccount(a.Email) > 0 {
					removed = append(removed, a.Email)
				}
				break
			}
		}
	}
	return removed
}
