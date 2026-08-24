package controller

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// 令牌命名审计（v8.8 站规规则引擎版，详见 VPS-CONNECTION.md §22）：
// 六级规则(meaningless/group_abuse/cloud_tavern/missing_elements/cross_group/unclear)，
// 每级动作可配(TokenNameAuditActions)，词表可配(TokenNameAuditRules)。
// 检测范围: root 恒不检测; 次级管理员默认不检测(TokenNameAuditIncludeAdmins 控制)。
// 本接口只读; 自动封禁由宿主机 cron 执行(读同一组 options)。

type TokenNameAuditItem struct {
	UserId       int                        `json:"user_id"`
	Username     string                     `json:"username"`
	DisplayName  string                     `json:"display_name"`
	Role         int                        `json:"role"`
	Status       int                        `json:"status"`
	TokenName    string                     `json:"token_name"`
	Calls        int64                      `json:"calls"`
	LastUsed     int64                      `json:"last_used"`
	Groups       map[string]int64           `json:"groups"`
	Findings     []setting.TokenNameFinding `json:"findings"`
	Severity     string                     `json:"severity"` // 最高级别: severe > medium > review
}

type TokenNameAuditConfig struct {
	Enabled          bool                          `json:"enabled"`
	IncludeAdmins    bool                          `json:"include_admins"`
	WhitelistTokens  []string                      `json:"whitelist_tokens"`
	WhitelistUsers   []int                         `json:"whitelist_users"`
	Rules            setting.TokenNameAuditRules   `json:"rules"`
	Actions          map[string]string             `json:"actions"`
}

type TokenNameAuditResponse struct {
	WindowHours       int                  `json:"window_hours"`
	Items             []TokenNameAuditItem `json:"items"`
	Config            TokenNameAuditConfig `json:"config"`
	TotalHits         int                  `json:"total_hits"`
	SevereCount       int                  `json:"severe_count"`
	MediumCount       int                  `json:"medium_count"`
	ReviewCount       int                  `json:"review_count"`
	ProtectedSkipped  int                  `json:"protected_skipped"`
	WhitelistSkipped  int                  `json:"whitelist_skipped"`
	DisabledSkipped   int                  `json:"disabled_skipped"`
}

func severityRank(s string) int {
	switch s {
	case "severe":
		return 3
	case "medium":
		return 2
	case "review":
		return 1
	}
	return 0
}

func GetTokenNameAudit(c *gin.Context) {
	hours, err := strconv.Atoi(c.Query("hours"))
	if err != nil || hours <= 0 {
		hours = 168
	}
	if hours > 24*90 {
		hours = 24 * 90
	}
	includeDisabled := c.Query("include_disabled") == "true"

	since := time.Now().Unix() - int64(hours)*3600
	// logs 表写入频繁，SQLite 偶发 SQLITE_BUSY，短暂重试
	var rows []model.TokenNameUsageRow
	for attempt := 0; attempt < 3; attempt++ {
		rows, err = model.GetTokenNameUsage(since)
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	rules := setting.GetTokenNameAuditRules()

	// 按 (user, token) 聚合分组用量 + 静态规则评估
	type usage struct {
		groups   map[string]int64
		calls    int64
		lastUsed int64
		eval     setting.TokenNameEval
	}
	type userToken struct {
		userId    int
		tokenName string
		usage     *usage
	}
	var candidates []userToken
	whitelistSkipped := 0
	seen := make(map[string]map[string]*usage)
	for _, r := range rows {
		if r.UserId <= 0 {
			continue
		}
		if setting.TokenNameAuditTokenWhitelisted(r.TokenName) {
			whitelistSkipped++
			continue
		}
		userKey := strconv.Itoa(r.UserId)
		if _, ok := seen[userKey]; !ok {
			seen[userKey] = make(map[string]*usage)
		}
		u, ok := seen[userKey][r.TokenName]
		if !ok {
			ev := setting.EvaluateTokenName(r.TokenName)
			u = &usage{groups: make(map[string]int64), eval: ev}
			seen[userKey][r.TokenName] = u
			if len(ev.Findings) > 0 || ev.IsRP {
				candidates = append(candidates, userToken{userId: r.UserId, tokenName: r.TokenName, usage: u})
			}
		}
		u.groups[r.Group] += r.Calls
		u.calls += r.Calls
		if r.LastUsed > u.lastUsed {
			u.lastUsed = r.LastUsed
		}
	}

	// 组合分组类规则(group_abuse / cross_group)
	abuseSet := make(map[string]struct{}, len(rules.GroupAbuseGroups))
	for _, g := range rules.GroupAbuseGroups {
		abuseSet[g] = struct{}{}
	}
	checkedSet := make(map[string]struct{}, len(rules.CheckedGroups))
	for _, g := range rules.CheckedGroups {
		checkedSet[g] = struct{}{}
	}
	for i := range candidates {
		ut := &candidates[i]
		findings := append([]setting.TokenNameFinding(nil), ut.usage.eval.Findings...)
		// group_abuse: 正文 RP 命名(豁免词除外)在目标分组产生请求
		if ut.usage.eval.IsRP && !ut.usage.eval.Exempt {
			for g, n := range ut.usage.groups {
				if _, bad := abuseSet[g]; bad && n > 0 {
					findings = append(findings, setting.TokenNameFinding{
						Rule:     "group_abuse",
						Severity: "severe",
						Detail:   "RP命名×" + g + "组 " + strconv.FormatInt(n, 10) + "次",
					})
					break
				}
			}
		}
		// cross_group: 同一令牌跨 ≥2 个受检分组
		checked := 0
		for g := range ut.usage.groups {
			if _, ok := checkedSet[g]; ok {
				checked++
			}
		}
		if checked >= 2 {
			findings = append(findings, setting.TokenNameFinding{
				Rule:     "cross_group",
				Severity: "review",
				Detail:   "跨受检分组使用",
			})
		}
		ut.usage.eval.Findings = findings
	}
	// 过滤掉无 findings 的候选(纯 RP 命名但无滥用), 并算最高级别
	type scored struct {
		userId    int
		tokenName string
		usage     *usage
		findings  []setting.TokenNameFinding
		severity  string
	}
	var scoredList []scored
	for _, ut := range candidates {
		if len(ut.usage.eval.Findings) == 0 {
			continue
		}
		sev := ""
		for _, f := range ut.usage.eval.Findings {
			if severityRank(f.Severity) > severityRank(sev) {
				sev = f.Severity
			}
		}
		scoredList = append(scoredList, scored{
			userId: ut.userId, tokenName: ut.tokenName,
			usage: ut.usage, findings: ut.usage.eval.Findings, severity: sev,
		})
	}

	// 批量取用户信息
	uidSet := make(map[int]struct{})
	for _, s := range scoredList {
		uidSet[s.userId] = struct{}{}
	}
	uids := make([]int, 0, len(uidSet))
	for id := range uidSet {
		uids = append(uids, id)
	}
	type userMeta struct {
		Username    string
		DisplayName string
		Role        int
		Status      int
	}
	meta := make(map[int]userMeta, len(uids))
	if len(uids) > 0 {
		var users []struct {
			Id          int    `gorm:"column:id"`
			Username    string `gorm:"column:username"`
			DisplayName string `gorm:"column:display_name"`
			Role        int    `gorm:"column:role"`
			Status      int    `gorm:"column:status"`
		}
		if err := model.DB.Table("users").Select("id, username, display_name, role, status").Where("id IN ?", uids).Find(&users).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		for _, u := range users {
			meta[u.Id] = userMeta{Username: u.Username, DisplayName: u.DisplayName, Role: u.Role, Status: u.Status}
		}
	}

	items := make([]TokenNameAuditItem, 0)
	protectedSkipped, disabledSkipped := 0, 0
	severe, medium, review := 0, 0, 0
	for _, s := range scoredList {
		u, ok := meta[s.userId]
		if !ok {
			protectedSkipped++ // 用户已硬删除
			continue
		}
		// root 恒不检测; 管理员默认不检测(开关控制)
		if u.Role == common.RoleRootUser {
			protectedSkipped++
			continue
		}
		if u.Role >= common.RoleAdminUser && !setting.TokenNameAuditIncludeAdmins {
			protectedSkipped++
			continue
		}
		if u.Status != common.UserStatusEnabled && !includeDisabled {
			disabledSkipped++
			continue
		}
		if setting.TokenNameAuditUserWhitelisted(s.userId) {
			// 用户级白名单在候选阶段没过滤, 这里补(计数归白名单)
			continue
		}
		switch s.severity {
		case "severe":
			severe++
		case "medium":
			medium++
		default:
			review++
		}
		items = append(items, TokenNameAuditItem{
			UserId:      s.userId,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			Status:      u.Status,
			TokenName:   s.tokenName,
			Calls:       s.usage.calls,
			LastUsed:    s.usage.lastUsed,
			Groups:      s.usage.groups,
			Findings:    s.findings,
			Severity:    s.severity,
		})
	}

	// 排序: 级别高在前, 同级按调用量
	sort.SliceStable(items, func(i, j int) bool {
		if severityRank(items[i].Severity) != severityRank(items[j].Severity) {
			return severityRank(items[i].Severity) > severityRank(items[j].Severity)
		}
		return items[i].Calls > items[j].Calls
	})

	common.ApiSuccess(c, TokenNameAuditResponse{
		WindowHours:      hours,
		Items:            items,
		Config: TokenNameAuditConfig{
			Enabled:         setting.TokenNameAuditEnabled,
			IncludeAdmins:   setting.TokenNameAuditIncludeAdmins,
			WhitelistTokens: parseStringListOpt(setting.TokenNameAuditWhitelistTokens),
			WhitelistUsers:  parseIntListOpt(setting.TokenNameAuditWhitelistUsers),
			Rules:           rules,
			Actions:         setting.GetTokenNameAuditActions(),
		},
		TotalHits:        len(scoredList),
		SevereCount:      severe,
		MediumCount:      medium,
		ReviewCount:      review,
		ProtectedSkipped: protectedSkipped,
		WhitelistSkipped: whitelistSkipped,
		DisabledSkipped:  disabledSkipped,
	})
}

func parseStringListOpt(raw string) []string {
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return []string{}
	}
	return arr
}

func parseIntListOpt(raw string) []int {
	var arr []int
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return []int{}
	}
	return arr
}
