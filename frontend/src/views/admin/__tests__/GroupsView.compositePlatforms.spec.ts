import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

describe('GroupsView Composite route options', () => {
  it('offers Kimi, Zhipu GLM, and DeepSeek as route targets', () => {
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(
      expect.arrayContaining(['kimi', 'zhipu', 'deepseek'])
    )
  })

  it('offers Kiro as a route target', () => {
    const source = readFileSync(resolve('src/views/admin/GroupsView.vue'), 'utf8')
    const declaration = source.match(/const compositeRoutePlatformOptions[\s\S]*?\]\);/)?.[0]

    expect(declaration).toContain('KIRO_PLATFORM_OPTION')
  })
})
