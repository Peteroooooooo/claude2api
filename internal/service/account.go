package service

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"
)

// PublicAccount 是前端账号视图。
type PublicAccount struct {
	Email            string                    `json:"email"`
	OrgUUID          string                    `json:"org_uuid"`
	Status           string                    `json:"status,omitempty"`
	SubscriptionPlan string                    `json:"subscription_plan"`
	CreatedAt        time.Time                 `json:"created_at"`
	UpdatedAt        time.Time                 `json:"updated_at"`
	HasSession       bool                      `json:"has_session"`
	Usage            *repository.AccountUsage  `json:"usage"`
	UsageUpdatedAt   *time.Time                `json:"usage_updated_at"`
	Models           []repository.AccountModel `json:"models"`
	PlanModels       []repository.AccountModel `json:"plan_models"`
	DisabledModels   []string                  `json:"disabled_models"`
	ModelsUpdatedAt  *time.Time                `json:"models_updated_at"`
}

func SessionKey(account *repository.Account) string {
	if account == nil || account.Cookies == nil {
		return ""
	}
	return account.Cookies["sessionKey"]
}

func AccountUsable(account *repository.Account) bool {
	return SessionKey(account) != "" && account.Status != "expired"
}

func AccountByEmail(email string) *repository.Account {
	account := repository.AccountByEmail(email)
	if !AccountUsable(account) {
		return nil
	}
	return account
}

func PublicAccountView(account *repository.Account) *PublicAccount {
	if account == nil {
		return nil
	}
	return &PublicAccount{
		Email:            account.Email,
		OrgUUID:          account.OrgUUID,
		Status:           account.Status,
		SubscriptionPlan: account.SubscriptionPlan,
		CreatedAt:        account.CreatedAt,
		UpdatedAt:        account.UpdatedAt,
		HasSession:       SessionKey(account) != "",
		Usage:            account.Usage,
		UsageUpdatedAt:   account.UsageUpdatedAt,
		Models:           AccountModels(account),
		PlanModels:       PlanModels(account),
		DisabledModels:   append([]string{}, account.DisabledModels...),
		ModelsUpdatedAt:  account.ModelsUpdatedAt,
	}
}

// PublicAccounts 返回前端账号列表。
func PublicAccounts() []PublicAccount {
	src := repository.LoadAccounts()
	out := make([]PublicAccount, 0, len(src))
	for i := range src {
		out = append(out, *PublicAccountView(&src[i]))
	}
	return out
}

func StartAccountStatusMonitor() {
	go backfillAccountModels()
	go func() {
		for {
			time.Sleep(time.Duration(config.Get().StatusCheckIntervalSeconds) * time.Second)
			checkAccountStatuses()
		}
	}()
}

func RefreshAccount(email string) (*repository.Account, bool) {
	account := repository.AccountByEmail(email)
	sessionKey := SessionKey(account)
	if sessionKey == "" {
		return nil, false
	}

	client := NewClaudeAI(sessionKey, config.Get().Proxy, email)
	info, err := client.GetUserInfo()
	if repository.AccountByEmail(account.Email) == nil {
		return nil, false
	}
	if err != nil || info == nil || info.Email == "" {
		if err != nil && strings.Contains(err.Error(), "account_session_invalid") {
			if config.Get().RemoveInvalidAccount {
				repository.DeleteAccount(account.Email)
				slog.Warn("[账号刷新] 会话失效，已移除账号", "email", account.Email)
				return nil, true
			}
			repository.UpdateAccount(account.Email, func(a *repository.Account) { a.Status = "expired" })
			return repository.AccountByEmail(account.Email), false
		}
		repository.UpdateAccount(account.Email, func(a *repository.Account) { a.Status = "error" })
		slog.Warn("[账号刷新] 查询失败，保留账号", "email", account.Email, "err", err)
		return repository.AccountByEmail(account.Email), false
	}

	repository.UpdateAccount(account.Email, func(a *repository.Account) {
		a.Email, a.OrgUUID, a.Status = info.Email, info.OrgUUID, "active"
		a.SubscriptionPlan = info.SubscriptionPlan
		now := time.Now().UTC()
		a.Models, a.ModelsUpdatedAt = info.Models, &now
	})
	return repository.AccountByEmail(info.Email), false
}

// AccountModels 按套餐筛选目录并去掉管理员停用的模型；Free 仅开放 Sonnet 和 Haiku。
// 调度、/v1/models 和账号页都以此为准。
func AccountModels(account *repository.Account) []repository.AccountModel {
	models := []repository.AccountModel{}
	for _, model := range PlanModels(account) {
		if !slices.Contains(account.DisabledModels, model.ID) {
			models = append(models, model)
		}
	}
	return models
}

// PlanModels 返回套餐允许的完整目录，不考虑手动停用，供管理页编辑。
func PlanModels(account *repository.Account) []repository.AccountModel {
	models := []repository.AccountModel{}
	if account.SubscriptionPlan == "" {
		return models
	}
	for _, model := range account.Models {
		if account.SubscriptionPlan == "claude_free" && !strings.HasPrefix(model.ID, "claude-sonnet-") && !strings.HasPrefix(model.ID, "claude-haiku-") {
			continue
		}
		models = append(models, model)
	}
	return models
}

// SetDisabledModels 保存手动停用列表，只接受套餐目录内的模型 ID。
func SetDisabledModels(email string, disabled []string) (*repository.Account, error) {
	account := repository.AccountByEmail(email)
	if account == nil {
		return nil, fmt.Errorf("账号不存在")
	}
	allowed := map[string]bool{}
	for _, model := range PlanModels(account) {
		allowed[model.ID] = true
	}
	clean := []string{}
	for _, id := range disabled {
		id = strings.TrimSuffix(strings.TrimSpace(id), "-thinking")
		if !allowed[id] {
			return nil, fmt.Errorf("模型 %s 不在该账号套餐目录内", id)
		}
		if !slices.Contains(clean, id) {
			clean = append(clean, id)
		}
	}
	if !repository.UpdateAccount(email, func(a *repository.Account) { a.DisabledModels = clean }) {
		return nil, fmt.Errorf("保存失败")
	}
	slog.Info("[账号模型] 已更新停用模型", "email", email, "disabled", clean)
	return repository.AccountByEmail(email), nil
}

func AccountSupportsModel(account *repository.Account, model string) bool {
	base := strings.TrimSuffix(model, "-thinking")
	for _, available := range AccountModels(account) {
		if available.ID == base && (base == model || available.Thinking) {
			return true
		}
	}
	return false
}

// backfillAccountModels 为升级前导入、尚无套餐和模型记录的账号补齐信息，
// 否则按模型调度时这些账号会全部被判定为不可用。
func backfillAccountModels() {
	for _, account := range repository.LoadAccounts() {
		if SessionKey(&account) == "" || account.ModelsUpdatedAt != nil {
			continue
		}
		if _, removed := RefreshAccount(account.Email); !removed {
			slog.Info("[账号刷新] 已补齐模型记录", "email", account.Email)
		}
	}
}

func checkAccountStatuses() {
	for _, account := range repository.LoadAccounts() {
		if SessionKey(&account) == "" {
			continue
		}
		RefreshAccount(account.Email)
	}
}
