import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('Composite channel platform options', () => {
  it('includes every site concrete provider for pricing and model mapping', () => {
    const source = readFileSync(resolve('src/views/admin/ChannelsView.vue'), 'utf8')
    const declaration = source.match(/const compositePlatforms:[^=]+=[^\n]+/)?.[0]

    expect(declaration).toContain("'kimi'")
    expect(declaration).toContain("'zhipu'")
    expect(declaration).toContain("'deepseek'")
    expect(declaration).toContain("'kiro'")
  })
})
