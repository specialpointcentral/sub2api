import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import ModelRateLimitRuleEditor from '../ModelRateLimitRuleEditor.vue'

describe('ModelRateLimitRuleEditor', () => {
  it('edits candidate and free-text rows, validates immediately, normalizes empty limits, and never saves invalid rows', async () => {
    const wrapper = mount(ModelRateLimitRuleEditor, {
	  attachTo: document.body,
      props: {
        modelValue: [],
        candidates: ['claude-opus-4-1', 'gpt-5.6-luna-high'],
      },
    })

    const addRule = wrapper.get('[data-test="add-rule"]')
    expect(addRule.classes()).toEqual(expect.arrayContaining([
      'w-full',
      'border-2',
      'border-dashed',
      'border-gray-300',
    ]))
    expect(addRule.get('svg path').attributes('d')).toBe('M12 4v16m8-8H4')

    await addRule.trigger('click')
    expect(wrapper.findAll('[data-test="rule-row"]')).toHaveLength(1)
    const deleteRule = wrapper.get('[data-test="delete-rule"]')
    expect(deleteRule.classes()).toEqual(expect.arrayContaining([
      'rounded-lg',
      'p-2',
      'text-red-500',
      'transition-colors',
    ]))
    expect(deleteRule.get('svg path').attributes('d')).toBe('M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16')
	const firstPattern = wrapper.get('[data-test="model-pattern"]')
	;(firstPattern.element as HTMLInputElement).focus()
	await firstPattern.setValue('claude-opus-4-1')
	expect(document.activeElement).toBe(firstPattern.element)
    await wrapper.get('[data-test="concurrency-limit"]').setValue('')
    await wrapper.get('[data-test="rpm-limit"]').setValue('30')
    await wrapper.get('[data-test="save-rules"]').trigger('click')
    expect(wrapper.emitted('save')?.at(-1)?.[0]).toEqual([
      { model_pattern: 'claude-opus-4-1', limits: { concurrency: 0, rpm: 30 } },
    ])

    await wrapper.get('[data-test="add-rule"]').trigger('click')
    const patterns = wrapper.findAll('[data-test="model-pattern"]')
    await patterns[1].setValue('CLAUDE-OPUS-4-1')
    expect(wrapper.text()).toContain('admin.modelRateLimits.errors.duplicate')
    expect(wrapper.get('[data-test="save-rules"]').attributes()).toHaveProperty('disabled')
    const saveCount = wrapper.emitted('save')?.length ?? 0
    await wrapper.get('[data-test="save-rules"]').trigger('click')
    expect(wrapper.emitted('save')?.length ?? 0).toBe(saveCount)

	await patterns[1].setValue('custom-*')
		expect(wrapper.text()).toContain('admin.modelRateLimits.explicitUnlimited')
		const concurrency = wrapper.findAll('[data-test="concurrency-limit"]')
    await concurrency[1].setValue('-1')
    expect(wrapper.text()).toContain('admin.modelRateLimits.errors.nonNegativeInteger')
    await concurrency[1].setValue('2')

    await wrapper.findAll('[data-test="delete-rule"]')[0].trigger('click')
    expect(wrapper.findAll('[data-test="rule-row"]')).toHaveLength(1)
    expect((wrapper.get('[data-test="model-pattern"]').element as HTMLInputElement).value).toBe('custom-*')
	wrapper.unmount()
  })
})
