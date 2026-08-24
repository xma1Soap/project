import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Eye, EyeOff } from 'lucide-react'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ErrorState } from '@/components/error-state'
import { getTokenNameAudit } from './api'
import { tokenNameAuditQueryKeys } from './lib/query-keys'
import { AuditSettingsCard } from './components/audit-settings-card'
import { AuditTable } from './components/audit-table'
import type { AuditSeverity } from './types'

type TabId = 'all' | AuditSeverity

export function TokenNamingAudit() {
  const { t } = useTranslation()
  const [windowHours, setWindowHours] = useState(168)
  const [includeDisabled, setIncludeDisabled] = useState(false)
  const [tab, setTab] = useState<TabId>('all')
  const [showDisplayName, setShowDisplayName] = useState(false)

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: tokenNameAuditQueryKeys.scan(windowHours, includeDisabled),
    queryFn: () =>
      getTokenNameAudit({
        hours: windowHours,
        include_disabled: includeDisabled,
      }),
  })

  const items = useMemo(() => {
    const all = data?.data?.items ?? []
    if (tab === 'all') return all
    return all.filter((it) => it.severity === tab)
  }, [data, tab])

  const tabs: { id: TabId; label: string; count: number }[] = [
    { id: 'all', label: t('All'), count: data?.data?.items.length ?? 0 },
    {
      id: 'severe',
      label: t('Severe'),
      count: data?.data?.severe_count ?? 0,
    },
    { id: 'medium', label: t('Needs Fix'), count: data?.data?.medium_count ?? 0 },
    {
      id: 'review',
      label: t('Needs Review'),
      count: data?.data?.review_count ?? 0,
    },
  ]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='inline-flex min-w-0 items-center gap-2'>
          <span className='truncate'>{t('Token Naming Audit')}</span>
          <Badge variant='outline' className='shrink-0'>
            Admin
          </Badge>
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <AuditSettingsCard
            config={data?.data?.config}
            windowHours={windowHours}
            onWindowHoursChange={setWindowHours}
            includeDisabled={includeDisabled}
            onIncludeDisabledChange={setIncludeDisabled}
          />

          <div className='flex flex-wrap items-center gap-2'>
            {tabs.map((tb) => (
              <Button
                key={tb.id}
                size='sm'
                variant={tab === tb.id ? 'default' : 'outline'}
                onClick={() => setTab(tb.id)}
              >
                {tb.label}
                <Badge variant='secondary' className='ml-1'>
                  {tb.count}
                </Badge>
              </Button>
            ))}
            <Button
              size='sm'
              variant={showDisplayName ? 'default' : 'outline'}
              onClick={() => setShowDisplayName((v) => !v)}
              title={t('Show display names')}
            >
              {showDisplayName ? (
                <Eye className='size-3.5' />
              ) : (
                <EyeOff className='size-3.5' />
              )}
              {t('Display Name')}
            </Button>
          </div>

          {isLoading ? (
            <div className='space-y-2'>
              <Skeleton className='h-10 w-full' />
              <Skeleton className='h-10 w-full' />
              <Skeleton className='h-10 w-full' />
            </div>
          ) : isError ? (
            <ErrorState
              description={error?.message || t('Operation failed')}
              onRetry={() => refetch()}
            />
          ) : (
            <AuditTable items={items} showDisplayName={showDisplayName} />
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
