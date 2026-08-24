package setting

import (
	"encoding/json"
	"strings"
	"sync"
)

// Token naming audit (v8.6 基础版, v8.8 升级为站规规则引擎):
// 按站规校验令牌命名(环境+软件+用途三要素/云酒馆来源/RP蹭coding分组等),
// 六级规则判定 + 每级动作可配。词表/动作/管理员开关存 options,
// 前端审计页与宿主机 cron(/opt/new-api/scripts/token-name-monitor.py v3)共用同一配置。
// 角色范围: root 恒不检测; 次级管理员默认不检测, 由 TokenNameAuditIncludeAdmins 控制。
// 详见 VPS-CONNECTION.md §22。

var TokenNameAuditEnabled = false
var TokenNameAuditWhitelistTokens = "[]" // JSON array of token names (lowercase compare)
var TokenNameAuditWhitelistUsers = "[]"  // JSON array of user ids
var TokenNameAuditRulesJSON = ""         // JSON, empty = use defaults
var TokenNameAuditActionsJSON = `{"severe":"ban","medium":"report","review":"report"}`
var TokenNameAuditIncludeAdmins = false

type TokenNameAuditRules struct {
	// 用途关键词(命中即视为具备该用途要素), key = 用途类别
	Purposes map[string][]string `json:"purposes"`
	// 软件关键词
	Software []string `json:"software"`
	// 使用环境关键词
	Env []string `json:"env"`
	// 云酒馆合规来源词(自建/自搭/公益/家名)
	TavernSources []string `json:"tavern_sources"`
	// group_abuse 豁免词(如 agent: 带工具调用的 agent 命名不算正文 RP)
	AgentExempt []string `json:"agent_exempt"`
	// group_abuse 检查的目标分组(命中 RP 用途的令牌在这些分组产生请求即违规)
	GroupAbuseGroups []string `json:"group_abuse_groups"`
	// cross_group 检查的分组范围
	CheckedGroups []string `json:"checked_groups"`
	// 三要素缺项阈值: 1=缺任一项报, 2=缺≥2项报, 0=只标记(归待人工)
	MissingElementsThreshold int `json:"missing_elements_threshold"`
}

func DefaultTokenNameAuditRules() TokenNameAuditRules {
	return TokenNameAuditRules{
		Purposes: map[string][]string{
			"rp":              {"rp", "角色", "正文", "roleplay"},
			"chat":            {"聊天", "闲聊", "chat"},
			"variable":        {"mvu", "变量"},
			"form":            {"填表", "记忆"},
			"vector":          {"向量"},
			"vector_rewrite":  {"重写"},
			"vector_cross":    {"交火"},
			"code":            {"代码", "code", "接码", "写卡", "ide", "zed"},
			"image":           {"生图", "画图", "绘图", "image"},
		},
		Software:                  []string{"酒馆", "sillytavern", "tavern", "st", "tt", "tauri", "claude", "codex", "opencode", "code", "vscode", "zed", "cherry", "chatbox", "luker", "前端", "小手机", "插件", "termux"},
		Env:                       []string{"本地", "云", "自建", "自搭", "公益", "手机", "termux", "安卓", "android", "pc", "移动", "网页", "云端"},
		TavernSources:             []string{"自建", "自搭", "公益", "夜鹿", "凡人", "神隐", "喵"},
		AgentExempt:               []string{"agent"},
		GroupAbuseGroups:          []string{"coding"},
		CheckedGroups:             []string{"default", "coding"},
		MissingElementsThreshold: 2,
	}
}

// TokenNameFinding 单条命中
type TokenNameFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // severe / medium / review
	Detail   string `json:"detail"`
}

// TokenNameEval 静态评估结果(不含分组信息; group_abuse/cross_group 由调用方结合分组数据判定)
type TokenNameEval struct {
	Findings    []TokenNameFinding
	HasEnv      bool
	HasSoftware bool
	HasPurpose  bool
	Purposes    []string // 命中的用途类别
	IsRP        bool     // 命中 rp 用途
	Exempt      bool     // 命中豁免词(agent)
}

var (
	tokenNameAuditLock       sync.RWMutex
	tokenNameAuditRulesCache *TokenNameAuditRules
	tokenNameAuditTokenSet   map[string]struct{}
	tokenNameAuditUserSet    map[int]struct{}
)

func parseStringList(raw string) []string {
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	return arr
}

// GetTokenNameAuditRules 返回生效规则(option 覆盖默认值; 字段缺省回落默认)
func GetTokenNameAuditRules() TokenNameAuditRules {
	tokenNameAuditLock.RLock()
	if tokenNameAuditRulesCache != nil {
		defer tokenNameAuditLock.RUnlock()
		return *tokenNameAuditRulesCache
	}
	tokenNameAuditLock.RUnlock()

	rules := DefaultTokenNameAuditRules()
	if TokenNameAuditRulesJSON != "" {
		var custom TokenNameAuditRules
		if err := json.Unmarshal([]byte(TokenNameAuditRulesJSON), &custom); err == nil {
			if len(custom.Purposes) > 0 {
				rules.Purposes = custom.Purposes
			}
			if len(custom.Software) > 0 {
				rules.Software = custom.Software
			}
			if len(custom.Env) > 0 {
				rules.Env = custom.Env
			}
			if len(custom.TavernSources) > 0 {
				rules.TavernSources = custom.TavernSources
			}
			if len(custom.AgentExempt) > 0 {
				rules.AgentExempt = custom.AgentExempt
			}
			if len(custom.GroupAbuseGroups) > 0 {
				rules.GroupAbuseGroups = custom.GroupAbuseGroups
			}
			if len(custom.CheckedGroups) > 0 {
				rules.CheckedGroups = custom.CheckedGroups
			}
			if custom.MissingElementsThreshold >= 0 && custom.MissingElementsThreshold <= 2 {
				rules.MissingElementsThreshold = custom.MissingElementsThreshold
			}
		}
	}

	tokenNameAuditLock.Lock()
	defer tokenNameAuditLock.Unlock()
	if tokenNameAuditRulesCache != nil {
		return *tokenNameAuditRulesCache
	}
	tokenNameAuditRulesCache = &rules
	return rules
}

// GetTokenNameAuditActions 每级动作: ban / report
func GetTokenNameAuditActions() map[string]string {
	actions := map[string]string{"severe": "ban", "medium": "report", "review": "report"}
	var custom map[string]string
	if err := json.Unmarshal([]byte(TokenNameAuditActionsJSON), &custom); err == nil {
		for k, v := range custom {
			if v == "ban" || v == "report" {
				actions[k] = v
			}
		}
	}
	return actions
}

// InvalidateTokenNameAuditCache 词表/白名单 option 变更后调用
func InvalidateTokenNameAuditCache() {
	tokenNameAuditLock.Lock()
	defer tokenNameAuditLock.Unlock()
	tokenNameAuditRulesCache = nil
	tokenNameAuditTokenSet = nil
	tokenNameAuditUserSet = nil
}

func TokenNameAuditTokenWhitelisted(name string) bool {
	tokenNameAuditLock.RLock()
	set := tokenNameAuditTokenSet
	tokenNameAuditLock.RUnlock()
	if set == nil {
		set = make(map[string]struct{})
		for _, item := range parseStringList(TokenNameAuditWhitelistTokens) {
			if s := strings.TrimSpace(item); s != "" {
				set[strings.ToLower(s)] = struct{}{}
			}
		}
		tokenNameAuditLock.Lock()
		tokenNameAuditTokenSet = set
		tokenNameAuditLock.Unlock()
	}
	_, ok := set[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func TokenNameAuditUserWhitelisted(id int) bool {
	tokenNameAuditLock.RLock()
	set := tokenNameAuditUserSet
	tokenNameAuditLock.RUnlock()
	if set == nil {
		set = make(map[int]struct{})
		for _, id := range parseIdList(TokenNameAuditWhitelistUsers) {
			set[id] = struct{}{}
		}
		tokenNameAuditLock.Lock()
		tokenNameAuditUserSet = set
		tokenNameAuditLock.Unlock()
	}
	_, ok := set[id]
	return ok
}

func parseIdList(raw string) []int {
	var arr []int
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	return arr
}

func containsAnyKeyword(lowerName string, keywords []string) bool {
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(lowerName, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// ClassifyTokenName v8.6 的无意义命名检测(保留): 纯数字/短字母/无元音
func ClassifyTokenName(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return ""
		}
	}
	if TokenNameAuditTokenWhitelisted(s) {
		return ""
	}
	isDigit, isAlpha := true, true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			isAlpha = false
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			isDigit = false
		default:
			isDigit, isAlpha = false, false
		}
	}
	if isDigit {
		return "pure_digits"
	}
	if isAlpha {
		if len(s) <= 2 {
			return "short_letters"
		}
		if !strings.ContainsAny(strings.ToLower(s), "aeiou") {
			return "no_vowel"
		}
	}
	return ""
}

// EvaluateTokenName 静态规则评估(不含分组类规则)
func EvaluateTokenName(name string) TokenNameEval {
	s := strings.TrimSpace(name)
	lower := strings.ToLower(s)
	eval := TokenNameEval{}
	if s == "" {
		return eval
	}

	// 1) 无意义命名(严重) —— 短路返回
	if cat := ClassifyTokenName(s); cat != "" {
		eval.Findings = append(eval.Findings, TokenNameFinding{
			Rule:     "meaningless",
			Severity: "severe",
			Detail:   cat,
		})
		return eval
	}

	rules := GetTokenNameAuditRules()

	if TokenNameAuditTokenWhitelisted(s) {
		return eval // 白名单令牌名完全放行
	}

	// 2) 三要素
	eval.HasEnv = containsAnyKeyword(lower, rules.Env)
	eval.HasSoftware = containsAnyKeyword(lower, rules.Software)
	for cat, keywords := range rules.Purposes {
		if containsAnyKeyword(lower, keywords) {
			eval.HasPurpose = true
			eval.Purposes = append(eval.Purposes, cat)
			if cat == "rp" {
				eval.IsRP = true
			}
		}
	}
	eval.Exempt = containsAnyKeyword(lower, rules.AgentExempt)

	missing := 0
	var missingWhat []string
	if !eval.HasEnv {
		missing++
		missingWhat = append(missingWhat, "env")
	}
	if !eval.HasSoftware {
		missing++
		missingWhat = append(missingWhat, "software")
	}
	if !eval.HasPurpose {
		missing++
		missingWhat = append(missingWhat, "purpose")
	}

	if missing == 3 || (missing == 2 && eval.HasEnv) {
		// 全无 或 只有环境 -> 无法界定
		eval.Findings = append(eval.Findings, TokenNameFinding{
			Rule:     "unclear",
			Severity: "review",
			Detail:   "elements: env only or none",
		})
	} else if missing > 0 {
		th := rules.MissingElementsThreshold
		if th == 0 {
			eval.Findings = append(eval.Findings, TokenNameFinding{
				Rule:     "missing_elements",
				Severity: "review",
				Detail:   "missing: " + strings.Join(missingWhat, "+") + " (mark only)",
			})
		} else if missing >= th {
			eval.Findings = append(eval.Findings, TokenNameFinding{
				Rule:     "missing_elements",
				Severity: "medium",
				Detail:   "missing: " + strings.Join(missingWhat, "+"),
			})
		}
	}

	// 3) 云酒馆未注明来源
	if strings.Contains(s, "云酒馆") && !containsAnyKeyword(lower, rules.TavernSources) {
		eval.Findings = append(eval.Findings, TokenNameFinding{
			Rule:     "cloud_tavern",
			Severity: "medium",
			Detail:   "云酒馆未注明 自建/自搭/公益/家名",
		})
	}

	return eval
}
