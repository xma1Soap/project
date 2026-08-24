import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ShieldAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { updateTokenNameAuditOption } from '../api'
import { tokenNameAuditQueryKeys } from '../lib/query-keys'
import type { TokenNameAuditConfig } from '../types'

interface AuditSettingsCardProps {
  config: TokenNameAuditConfig | undefined
  windowHours: number
  onWindowHoursChange: (hours: number) => void
  includeDisabled: boolean
  onIncludeDisabledChange: (value: boolean) => void
}

export function AuditSettingsCard({
  config,
  windowHours,
  onWindowHoursChange,
  includeDisabled,
  onIncludeDisabledChange,
}: AuditSettingsCardProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isRoot =
    useAuthStore((state) => state.auth.user?.role) === ROLE.SUPER_ADMIN

  const [enabled, setEnabled] = useState(false)
  const [includeAdmins, setIncludeAdmins] = useState(false)
  const [tokensText, setTokensText] = useState('')
  const [usersText, setUsersText] = useState('')
  const [actions, setActions] = useState<Record<string, string>>({
    severe: 'ban',
    medium: 'report',
    review: 'report',
  })
  const [threshold, setThreshold] = useState(2)
  const [rulesText, setRulesText] = useState('')

  useEffect(() => {
    if (!config) return
    setEnabled(config.enabled)
    setIncludeAdmins(config.include_admins)
    setTokensText((config.whitelist_tokens || []).join(', '))
    setUsersText((config.whitelist_users || []).join(', '))
    setActions({
      severe: config.actions?.severe ?? 'ban',
      medium: config.actions?.medium ?? 'report',
      review: config.actions?.review ?? 'report',
    })
    setThreshold(config.rules?.missing_elements_threshold ?? 2)
    const { missing_elements_threshold: _th, ...rest } = config.rules || ({} as never)
    setRulesText(JSON.stringify(rest, null, 2))
  }, [config])

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: tokenNameAuditQueryKeys.all,
    })

  const optionMutation = useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) =>
      updateTokenNameAuditOption(key, value),
    onSuccess: (data) => {
      if (!data.success) toast.error(data.message || t('Failed to save'))
      invalidate()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const saveWhitelists = useMutation({
    mutationFn: async () => {
      const tokens = tokensText
        .split(/[,，\n]/)
        .map((s) => s.trim())
        .filter(Boolean)
      const users = usersText
        .split(/[,，\n\s]+/)
        .map((s) => parseInt(s, 10))
        .filter((n) => Number.isInteger(n) && n > 0)
      await updateTokenNameAuditOption(
        'TokenNameAuditWhitelistTokens',
        JSON.stringify(tokens)
      )
      await updateTokenNameAuditOption(
        'TokenNameAuditWhitelistUsers',
        JSON.stringify(users)
      )
    },
    onSuccess: () => {
      toast.success(t('Whitelists saved'))
      invalidate()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const saveRules = useMutation({
    mutationFn: async () => {
      const base = config?.rules || ({} as never)
      const { missing_elements_threshold: _old, ...rest } = base
      let parsed = rest
      if (rulesText.trim() !== '') {
        parsed = JSON.parse(rulesText)
      }
      const full = { ...parsed, missing_elements_threshold: threshold }
      await updateTokenNameAuditOption(
        'TokenNameAuditRules',
        JSON.stringify(full)
      )
    },
    onSuccess: () => {
      toast.success(t('Rules saved'))
      invalidate()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const saveActions = useMutation({
    mutationFn: (next: Record<string, string>) =>
      updateTokenNameAuditOption('TokenNameAuditActions', JSON.stringify(next)),
    onSuccess: () => invalidate(),
    onError: (error: Error) => toast.error(error.message),
  })

  const severitySelect = (
    level: 'severe' | 'medium' | 'review',
    label: string
  ) => (
    <div className='flex items-center gap-2'>
      <Label className='w-20'>{label}</Label>
      <NativeSelect
        className='w-32'
        value={actions[level] || 'report'}
        disabled={!isRoot}
        onChange={(e) => {
          const next = { ...actions, [level]: e.target.value }
          setActions(next)
          saveActions.mutate(next)
        }}
      >
        <NativeSelectOption value='ban'>{t('Auto Ban')}</NativeSelectOption>
        <NativeSelectOption value='report'>{t('Report Only')}</NativeSelectOption>
      </NativeSelect>
    </div>
  )

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <ShieldAlert className='size-4' />
          {t('Audit Settings')}
        </CardTitle>
        <CardDescription>
          {t(
            'Enforce naming rules: environment + software + purpose. Root users are never scanned; admins only when enabled.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='flex flex-wrap items-center gap-4'>
          <div className='flex items-center gap-2'>
            <Switch
              id='token-name-audit-enabled'
              checked={enabled}
              disabled={!isRoot || optionMutation.isPending}
              onCheckedChange={(checked) => {
                setEnabled(checked)
                optionMutation.mutate({
                  key: 'TokenNameAuditEnabled',
                  value: checked ? 'true' : 'false',
                })
              }}
            />
            <Label htmlFor='token-name-audit-enabled'>{t('Auto Ban')}</Label>
          </div>
          <div className='flex items-center gap-2'>
            <Switch
              id='token-name-audit-admins'
              checked={includeAdmins}
              disabled={!isRoot || optionMutation.isPending}
              onCheckedChange={(checked) => {
                setIncludeAdmins(checked)
                optionMutation.mutate({
                  key: 'TokenNameAuditIncludeAdmins',
                  value: checked ? 'true' : 'false',
                })
              }}
            />
            <Label htmlFor='token-name-audit-admins'>
              {t('Detect admins')}
            </Label>
          </div>
          <NativeSelect
            className='w-36'
            value={String(windowHours)}
            onChange={(e) => onWindowHoursChange(Number(e.target.value))}
          >
            <NativeSelectOption value='24'>{t('Last 24 hours')}</NativeSelectOption>
            <NativeSelectOption value='168'>{t('Last 7 days')}</NativeSelectOption>
            <NativeSelectOption value='720'>{t('Last 30 days')}</NativeSelectOption>
          </NativeSelect>
          <label className='flex items-center gap-2 text-sm'>
            <Checkbox
              checked={includeDisabled}
              onCheckedChange={(checked) =>
                onIncludeDisabledChange(checked === true)
              }
            />
            {t('Include banned users')}
          </label>
        </div>

        {isRoot ? (
          <>
            <div className='flex flex-wrap items-center gap-4'>
              <span className='text-muted-foreground text-sm'>
                {t('Severity Actions')}:
              </span>
              {severitySelect('severe', t('Severe'))}
              {severitySelect('medium', t('Needs Fix'))}
              {severitySelect('review', t('Needs Review'))}
              <div className='flex items-center gap-2'>
                <Label className='w-24'>{t('Strictness')}</Label>
                <NativeSelect
                  className='w-40'
                  value={String(threshold)}
                  onChange={(e) => setThreshold(Number(e.target.value))}
                >
                  <NativeSelectOption value='1'>
                    {t('Missing any element')}
                  </NativeSelectOption>
                  <NativeSelectOption value='2'>
                    {t('Missing 2+ elements')}
                  </NativeSelectOption>
                  <NativeSelectOption value='0'>
                    {t('Mark only')}
                  </NativeSelectOption>
                </NativeSelect>
              </div>
            </div>

            <div className='grid gap-4 sm:grid-cols-2'>
              <div className='space-y-2'>
                <Label htmlFor='tna-whitelist-tokens'>
                  {t('Whitelisted token names')}
                </Label>
                <Input
                  id='tna-whitelist-tokens'
                  value={tokensText}
                  placeholder='rp, st, tt, glm'
                  onChange={(e) => setTokensText(e.target.value)}
                />
              </div>
              <div className='space-y-2'>
                <Label htmlFor='tna-whitelist-users'>
                  {t('Whitelisted user IDs')}
                </Label>
                <Input
                  id='tna-whitelist-users'
                  value={usersText}
                  placeholder='1, 2, 100'
                  onChange={(e) => setUsersText(e.target.value)}
                />
              </div>
            </div>

            <div className='space-y-2'>
              <Label htmlFor='tna-rules'>{t('Rule wordlists (JSON)')}</Label>
              <Textarea
                id='tna-rules'
                className='font-mono text-xs'
                rows={8}
                value={rulesText}
                onChange={(e) => setRulesText(e.target.value)}
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'purposes / software / env / tavern_sources / agent_exempt / group_abuse_groups / checked_groups; keyword matching is case-insensitive substring.'
                )}
              </p>
            </div>

            <div className='flex gap-2'>
              <Button
                size='sm'
                disabled={saveWhitelists.isPending}
                onClick={() => saveWhitelists.mutate()}
              >
                {saveWhitelists.isPending
                  ? t('Saving...')
                  : t('Save Whitelists')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={saveRules.isPending}
                onClick={() => saveRules.mutate()}
              >
                {saveRules.isPending ? t('Saving...') : t('Save Rules')}
              </Button>
            </div>
          </>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('Only the root account can modify audit settings.')}
          </p>
        )}
      </CardContent>
    </Card>
  )
}
