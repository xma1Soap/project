export const tokenNameAuditQueryKeys = {
  all: ['token-name-audit'] as const,
  scan: (hours: number, includeDisabled: boolean) =>
    [
      ...tokenNameAuditQueryKeys.all,
      'scan',
      { hours, includeDisabled },
    ] as const,
}
