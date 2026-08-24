import { api } from '@/lib/api'
import type { TokenNameAuditResponse } from './types'

export async function getTokenNameAudit(params: {
  hours: number
  include_disabled?: boolean
}) {
  const res = await api.get<TokenNameAuditResponse>('/api/token_name_audit', {
    params,
  })
  return res.data
}

// 配置写入复用通用 options 接口（RootAuth）：
// TokenNameAuditEnabled / TokenNameAuditWhitelistTokens / TokenNameAuditWhitelistUsers
export async function updateTokenNameAuditOption(key: string, value: string) {
  const res = await api.put('/api/option/', { key, value })
  return res.data
}
