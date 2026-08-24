export type AuditSeverity = 'severe' | 'medium' | 'review'

export type AuditRuleId =
  | 'meaningless'
  | 'group_abuse'
  | 'cloud_tavern'
  | 'missing_elements'
  | 'cross_group'
  | 'unclear'

export interface TokenNameFinding {
  rule: AuditRuleId
  severity: AuditSeverity
  detail: string
}

export interface TokenNameAuditItem {
  user_id: number
  username: string
  display_name: string
  role: number
  status: number
  token_name: string
  calls: number
  last_used: number
  groups: Record<string, number>
  findings: TokenNameFinding[]
  severity: AuditSeverity
}

export interface TokenNameAuditRules {
  purposes: Record<string, string[]>
  software: string[]
  env: string[]
  tavern_sources: string[]
  agent_exempt: string[]
  group_abuse_groups: string[]
  checked_groups: string[]
  missing_elements_threshold: number
}

export interface TokenNameAuditConfig {
  enabled: boolean
  include_admins: boolean
  whitelist_tokens: string[]
  whitelist_users: number[]
  rules: TokenNameAuditRules
  actions: Record<'severe' | 'medium' | 'review', 'ban' | 'report'>
}

export interface TokenNameAuditData {
  window_hours: number
  items: TokenNameAuditItem[]
  config: TokenNameAuditConfig
  total_hits: number
  severe_count: number
  medium_count: number
  review_count: number
  protected_skipped: number
  whitelist_skipped: number
  disabled_skipped: number
}

export interface TokenNameAuditResponse {
  success: boolean
  message?: string
  data: TokenNameAuditData
}
