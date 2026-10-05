package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Account 是一条账号记录。
type Account struct {
	ID         uint              `json:"-" gorm:"primaryKey"`
	Email      string            `json:"email" gorm:"uniqueIndex;not null"`
	OrgUUID    string            `json:"org_uuid" gorm:"column:org_uuid"`
	Cookies    map[string]string `json:"-" gorm:"serializer:json"`
	ImportJSON string            `json:"-" gorm:"type:text"`
	Status     string            `json:"status,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
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

// UpsertAccount 写入或更新账号。
func UpsertAccount(a *Account) error {
	accountFilesMu.Lock()
	defer accountFilesMu.Unlock()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "email"}},
			UpdateAll: true,
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
		if err := tx.Save(a).Error; err != nil {
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
