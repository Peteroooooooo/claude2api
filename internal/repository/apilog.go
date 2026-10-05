package repository

import "encoding/json"

// APILog 是 2api 调用日志。
type APILog struct {
	ID                 int64           `json:"id" gorm:"primaryKey"`
	CreatedAt          string          `json:"created_at"`
	Endpoint           string          `json:"endpoint"`
	Model              string          `json:"model"`
	Account            string          `json:"account"`
	Stream             bool            `json:"stream"`
	Success            bool            `json:"success"`
	StatusCode         int             `json:"status_code"`
	InputTokens        int             `json:"input_tokens"`
	OutputTokens       int             `json:"output_tokens"`
	DurationMs         int64           `json:"duration_ms"`
	FirstTokenMs       int64           `json:"first_token_ms"`
	TPS                float64         `json:"tps"`
	Error              string          `json:"error"`
	Request            json.RawMessage `json:"request" gorm:"type:text;serializer:json"`
	Response           string          `json:"response" gorm:"type:text"`
	SessionID          string          `json:"session_id,omitempty"`
	ConversationID     string          `json:"conversation_id,omitempty"`
	ParentUUID         string          `json:"parent_uuid,omitempty"`
	MessageUUID        string          `json:"message_uuid,omitempty"`
	SessionAction      string          `json:"session_action,omitempty"`
	UpstreamInputBytes int             `json:"upstream_input_bytes"`
	SwitchReason       string          `json:"switch_reason,omitempty"`
	UpstreamModel      string          `json:"upstream_model,omitempty"`
	ThinkingMode       string          `json:"thinking_mode,omitempty"`
	Effort             string          `json:"effort,omitempty"`
}

// APILogStats summarizes all retained logs, independently of pagination.
// Tokens are the request/response estimates recorded in those logs.
type APILogStats struct {
	Calls        int64 `json:"calls"`
	Success      int64 `json:"success"`
	Failed       int64 `json:"failed"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

func GetAPILogStats() (APILogStats, error) {
	var stats APILogStats
	err := db.Model(&APILog{}).Select(`
		COUNT(*) AS calls,
		COALESCE(SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END), 0) AS success,
		COALESCE(SUM(input_tokens), 0) AS input_tokens,
		COALESCE(SUM(output_tokens), 0) AS output_tokens
	`).Scan(&stats).Error
	stats.Failed = stats.Calls - stats.Success
	stats.TotalTokens = stats.InputTokens + stats.OutputTokens
	return stats, err
}

func GetAPILog(id int64) (APILog, bool) {
	var out APILog
	if db.First(&out, id).Error != nil {
		return APILog{}, false
	}
	out.calculateTPS()
	return out, true
}

// TPS is the estimated output throughput over the whole request. Buffered
// replies have no measurable decode interval; replays have no model generation.
func (l *APILog) calculateTPS() {
	l.TPS = 0
	if l.DurationMs > 0 && l.OutputTokens > 0 && l.SessionAction != "replay" {
		l.TPS = float64(l.OutputTokens) * 1000 / float64(l.DurationMs)
	}
}

// InsertAPILog 写入调用日志。
func InsertAPILog(l APILog) {
	if db == nil {
		return
	}
	if l.CreatedAt == "" {
		l.CreatedAt = nowUTC()
	}
	l.calculateTPS()
	_ = db.Create(&l).Error
}

// ListAPILogs 返回调用日志。
func ListAPILogs(limit, offset int) []APILog {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var out []APILog
	if err := db.Omit("Request", "Response").Order("id DESC").Limit(limit).Offset(offset).Find(&out).Error; err != nil {
		return []APILog{}
	}
	for i := range out {
		out[i].calculateTPS()
	}
	return out
}

// CountAPILogs 返回日志数。
func CountAPILogs() int {
	var n int64
	if err := db.Model(&APILog{}).Count(&n).Error; err != nil {
		return 0
	}
	return int(n)
}

func DeleteAPILogs(ids []int64) int64 {
	return db.Delete(&APILog{}, ids).RowsAffected
}

func TrimAPILogs(keep int) int64 {
	if keep == 0 {
		return db.Where("id > 0").Delete(&APILog{}).RowsAffected
	}
	var cutoff APILog
	if db.Select("id").Order("id DESC").Offset(keep).Take(&cutoff).Error != nil {
		return 0
	}
	return db.Where("id <= ?", cutoff.ID).Delete(&APILog{}).RowsAffected
}
