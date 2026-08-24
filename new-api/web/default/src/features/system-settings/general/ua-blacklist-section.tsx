/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import * as z from 'zod'
import type { Resolver } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import {
  SettingsForm,
  SettingsFormGrid,
  SettingsFormGridItem,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSettingsForm } from '../hooks/use-settings-form'
import { useUpdateOption } from '../hooks/use-update-option'

const uaBlacklistSchema = z.object({
  ua_blacklist: z.object({
    enabled: z.boolean(),
    keywords: z.string(),
  }),
})

type UABlacklistFormValues = z.infer<typeof uaBlacklistSchema>

type UABlacklistSectionProps = {
  defaultValues: {
    'ua_blacklist.enabled': boolean
    'ua_blacklist.keywords': string[]
  }
}

function keywordsToText(keywords: string[]): string {
  return keywords.join('\n')
}

function textToKeywords(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean)
}

export function UABlacklistSection({
  defaultValues,
}: UABlacklistSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const formDefaults: UABlacklistFormValues = {
    ua_blacklist: {
      enabled: defaultValues['ua_blacklist.enabled'],
      keywords: keywordsToText(defaultValues['ua_blacklist.keywords']),
    },
  }

  const { form, handleSubmit, isDirty, isSubmitting } =
    useSettingsForm<UABlacklistFormValues>({
      resolver: zodResolver(uaBlacklistSchema) as Resolver<
        UABlacklistFormValues,
        unknown,
        UABlacklistFormValues
      >,
      defaultValues: formDefaults,
      onSubmit: async (data) => {
        if (
          data.ua_blacklist.enabled !== defaultValues['ua_blacklist.enabled']
        ) {
          await updateOption.mutateAsync({
            key: 'ua_blacklist.enabled',
            value: data.ua_blacklist.enabled,
          })
        }
        const newKeywords = textToKeywords(data.ua_blacklist.keywords)
        const oldKeywords = defaultValues['ua_blacklist.keywords']
        if (JSON.stringify(newKeywords) !== JSON.stringify(oldKeywords)) {
          await updateOption.mutateAsync({
            key: 'ua_blacklist.keywords',
            value: JSON.stringify(newKeywords),
          })
        }
      },
    })

  return (
    <SettingsSection title={t('UA Blacklist')}>
      <FormNavigationGuard when={isDirty} />
      <Form {...form}>
        <SettingsForm onSubmit={handleSubmit}>
          <SettingsPageFormActions
            onSave={handleSubmit}
            isSaving={updateOption.isPending || isSubmitting}
          />
          <FormDirtyIndicator isDirty={isDirty} />

          <SettingsFormGrid>
            <SettingsFormGridItem span='full'>
              <FormField
                control={form.control}
                name='ua_blacklist.enabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Enable UA Blacklist')}</FormLabel>
                      <FormDescription>
                        {t(
                          'When enabled, users with matching User-Agent keywords will be automatically disabled.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
            </SettingsFormGridItem>

            <SettingsFormGridItem span='full'>
              <FormField
                control={form.control}
                name='ua_blacklist.keywords'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Blacklist Keywords')}</FormLabel>
                    <FormControl>
                      <Textarea
                        placeholder={t('One keyword per line')}
                        rows={4}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Enter keywords separated by newlines or commas. Fuzzy matching is used.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGridItem>
          </SettingsFormGrid>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
