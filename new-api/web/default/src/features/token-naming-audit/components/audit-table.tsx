import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Ban, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestampToDate } from '@/lib/format'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { manageUser } from '@/features/users/api'
import { tokenNameAuditQueryKeys } from '../lib/query-keys'
import type {
  AuditRuleId,
  AuditSeverity,
  TokenNameAuditItem,
  TokenNameFinding,
} from '../types'

const RULE_LABEL: Record<AuditRuleId, string> = {
  meaningless: 'Meaningless name',
  group_abuse: 'RP on coding group',
  cloud_tavern: 'Cloud tavern unspecified',
  missing_elements: 'Missing elements',
  cross_group: 'Cross-group usage',
  unclear: 'Unclear naming',
}

const SEVERITY_CLASS: Record<AuditSeverity, string> = {
  severe: 'bg-red-50 text-red-700 dark:bg-red-500/15 dark:text-red-300',
  medium: 'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  review: 'bg-sky-50 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
}

export function AuditTable({
  items,
  showDisplayName = false,
}: {
  items: TokenNameAuditItem[]
  showDisplayName?: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [banTarget, setBanTarget] = useState<TokenNameAuditItem | null>(null)

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: tokenNameAuditQueryKeys.all,
    })

  const manageMutation = useMutation({
    mutationFn: ({ id, action }: { id: number; action: string }) =>
      manageUser(id, action as 'disable' | 'enable'),
    onSuccess: (data, vars) => {
      if (data.success) {
        toast.success(
          vars.action === 'disable' ? t('User banned') : t('User unbanned')
        )
        invalidate()
      } else {
        toast.error(data.message || t('Operation failed'))
      }
    },
    onError: (error: Error) => toast.error(error.message),
  })

  if (items.length === 0) {
    return (
      <div className='text-muted-foreground py-8 text-center text-sm'>
        {t('No violations found in the selected window.')}
      </div>
    )
  }

  const renderFinding = (f: TokenNameFinding, key: string) => (
    <Badge
      key={key}
      variant='outline'
      className={SEVERITY_CLASS[f.severity]}
      title={f.detail}
    >
      {t(RULE_LABEL[f.rule])}
      {f.detail && f.detail !== 'pure_digits' && f.detail !== 'elements: env only or none'
        ? ` · ${f.detail}`
        : ''}
    </Badge>
  )

  return (
    <>
      <div className='rounded-md border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('User')}</TableHead>
              <TableHead>{t('Token Name')}</TableHead>
              <TableHead>{t('Findings')}</TableHead>
              <TableHead>{t('Groups')}</TableHead>
              <TableHead className='text-right'>{t('Calls')}</TableHead>
              <TableHead>{t('Last Used')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={`${item.user_id}-${item.token_name}`}>
                <TableCell className='font-mono'>
                  {item.username}
                  <span className='text-muted-foreground ml-1 text-xs'>
                    #{item.user_id}
                  </span>
                  {showDisplayName && item.display_name ? (
                    <span
                      className='block max-w-40 truncate text-xs text-sky-600 dark:text-sky-400'
                      title={item.display_name}
                    >
                      {item.display_name}
                    </span>
                  ) : null}
                </TableCell>
                <TableCell className='max-w-56 truncate font-mono'>
                  {item.token_name}
                </TableCell>
                <TableCell>
                  <div className='flex max-w-72 flex-wrap gap-1'>
                    {item.findings.map((f, i) =>
                      renderFinding(f, `${item.user_id}-${i}`)
                    )}
                  </div>
                </TableCell>
                <TableCell className='text-xs'>
                  {Object.entries(item.groups || {})
                    .sort((a, b) => b[1] - a[1])
                    .slice(0, 3)
                    .map(([g, n]) => (
                      <span key={g} className='mr-1 font-mono'>
                        {g}:{n}
                      </span>
                    ))}
                </TableCell>
                <TableCell className='text-right font-mono'>
                  {item.calls}
                </TableCell>
                <TableCell className='text-sm'>
                  {formatTimestampToDate(item.last_used)}
                </TableCell>
                <TableCell>
                  {item.status === 1 ? (
                    <Badge
                      variant='outline'
                      className='bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
                    >
                      {t('Normal')}
                    </Badge>
                  ) : (
                    <Badge
                      variant='outline'
                      className='bg-red-50 text-red-700 dark:bg-red-500/15 dark:text-red-300'
                    >
                      {t('Banned')}
                    </Badge>
                  )}
                </TableCell>
                <TableCell className='text-right'>
                  <div className='flex justify-end gap-1'>
                    {item.status === 1 ? (
                      <Button
                        size='sm'
                        variant='destructive'
                        disabled={manageMutation.isPending}
                        onClick={() => setBanTarget(item)}
                      >
                        <Ban className='size-3.5' />
                        {t('Ban')}
                      </Button>
                    ) : (
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={manageMutation.isPending}
                        onClick={() =>
                          manageMutation.mutate({
                            id: item.user_id,
                            action: 'enable',
                          })
                        }
                      >
                        <RotateCcw className='size-3.5' />
                        {t('Unban')}
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <ConfirmDialog
        open={banTarget !== null}
        onOpenChange={(open) => !open && setBanTarget(null)}
        title={t('Ban this user?')}
        desc={t(
          'The user will be disabled immediately and can be restored later.'
        )}
        destructive
        isLoading={manageMutation.isPending}
        handleConfirm={() => {
          if (banTarget) {
            manageMutation.mutate({ id: banTarget.user_id, action: 'disable' })
          }
          setBanTarget(null)
        }}
        confirmText={t('Ban')}
      />
    </>
  )
}
