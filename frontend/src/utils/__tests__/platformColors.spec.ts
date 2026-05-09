import { describe, expect, it } from 'vitest'
import {
  platformAccentColor,
  platformBadgeClass,
  platformTextClass
} from '@/utils/platformColors'

describe('platformColors', () => {
  it('uses the Kiro orange palette instead of the fallback palette', () => {
    expect(platformAccentColor('kiro')).toBe('#f97316')
    expect(platformBadgeClass('kiro')).toContain('orange')
    expect(platformTextClass('kiro')).toContain('orange')
  })
})
