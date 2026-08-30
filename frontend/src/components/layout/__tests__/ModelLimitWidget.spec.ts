import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import ModelLimitWidget from '../ModelLimitWidget.vue'
import type { ModelRateLimitSnapshot } from '@/api/modelRateLimits'

const usage = (used: number | null, limit: number | null, utilization: number | null) => ({
  used, limit, utilization, saturated: limit != null && used != null && used >= limit,
})

function snapshot(overrides: Partial<ModelRateLimitSnapshot> = {}): ModelRateLimitSnapshot {
  return {
    generated_at: '2026-08-30T12:00:00Z',
    refresh_after_ms: 5000,
    overall_concurrency: usage(2, 5, 40),
    models: [{
      model: 'gpt-5.6-luna-high', matched_pattern: 'gpt-5.6-luna*', source: 'user',
      dimensions: { concurrency: usage(1, 2, 50), rpm: { ...usage(29, 30, 96.7), window_seconds: 60, retry_after_seconds: 0 } },
    }],
    saturated: [],
    usage_available: true,
    ...overrides,
  }
}

describe('ModelLimitWidget', () => {
  afterEach(() => { document.body.innerHTML = '' })

  it('keeps overall concurrency as default, switches only on saturation, renders complete accessible groups, and honors unlimited and color boundaries', async () => {
    const wrapper = mount(ModelLimitWidget, {
      attachTo: document.body,
      props: { snapshot: snapshot(), poll: false },
    })
    expect(wrapper.get('[data-test="primary-dimension"]').text()).toContain('modelRateLimits.overallConcurrency')
    expect(wrapper.get('[data-test="primary-tone"]').attributes('data-tone')).toBe('green')

    const detailCard = wrapper.get('[data-test="model-limit-card"]')
    expect(detailCard.classes()).toEqual(expect.arrayContaining([
      'absolute',
      'right-0',
      'top-full',
      'mt-2',
      'w-56',
      'group-hover:block',
      'group-focus-within:block',
    ]))
    expect(detailCard.element.parentElement?.classList.contains('group')).toBe(true)
    expect(detailCard.element.parentElement?.classList.contains('relative')).toBe(true)
    expect(detailCard.get('[data-test="model-limit-divider"]').classes()).toEqual(expect.arrayContaining([
      'border-t',
      'border-gray-100',
      'pt-2',
      'dark:border-dark-700',
    ]))

    const overallLimits = detailCard.get('[data-test="overall-limits"]')
    expect(overallLimits.text()).not.toMatch(/\d+\/\d+|\d+%/)
    const modelLimits = detailCard.get('[data-test="model-limits"]')
    expect(modelLimits.text()).toContain('1/2')
    expect(modelLimits.text()).toContain('29/30')
    expect(modelLimits.text()).not.toMatch(/\d+%/)
    expect(wrapper.get('[data-test="model-limit-trigger"]').text()).not.toContain('2/5')

    const saturated = snapshot({
      overall_concurrency: usage(5, 5, 100),
      overall_rpm: { ...usage(10, 10, 100), window_seconds: 60, retry_after_seconds: 1 },
      models: [{
        model: 'gpt-5.6-luna-high', matched_pattern: 'gpt-5.6-luna*', source: 'user',
        dimensions: { concurrency: usage(1, 1, 100), rpm: { ...usage(30, 30, 100), window_seconds: 60, retry_after_seconds: 1 } },
      }],
      saturated: [
        { model: '', dimension: 'concurrency' },
        { model: 'gpt-5.6-luna-high', dimension: 'concurrency' },
        { model: 'gpt-5.6-luna-high', dimension: 'rpm' },
      ],
    })
    await wrapper.setProps({ snapshot: saturated })
    expect(wrapper.get('[data-test="primary-dimension"]').text()).toContain('modelRateLimits.overallConcurrency')
    expect(wrapper.get('[data-test="model-limit-trigger"]').text()).toContain('modelRateLimits.reached')
    expect(wrapper.get('[data-test="model-limit-trigger"]').text()).not.toContain('5/5')
    expect(wrapper.get('[data-test="primary-tone"]').attributes('data-tone')).toBe('red')

    expect(detailCard.text()).toContain('modelRateLimits.overallHeading')
    expect(detailCard.text()).toContain('modelRateLimits.modelHeading')
    expect(detailCard.text()).toContain('gpt-5.6-luna-high')
    expect(detailCard.findAll('[data-test="saturated-item"]')).toHaveLength(3)
    expect(detailCard.text()).not.toContain('unlimited-model')
    expect(detailCard.findAll('[data-test="usage-progress"]')).toHaveLength(4)
    expect(detailCard.get('[data-test="overall-limits"]').text()).not.toMatch(/\d+\/\d+|\d+%/)
    expect(detailCard.get('[data-test="model-limits"]').text()).toContain('1/1')
    expect(detailCard.get('[data-test="model-limits"]').text()).toContain('30/30')
    expect(detailCard.get('[data-test="model-limits"]').text()).not.toMatch(/\d+%/)

    await wrapper.setProps({
      snapshot: snapshot({
        overall_concurrency: usage(2, null, null),
        models: [{
          model: 'unlimited-model', matched_pattern: '*', source: 'global',
          dimensions: { concurrency: usage(2, null, null) },
        }],
        saturated: [],
      }),
    })
    expect(wrapper.text()).toContain('modelRateLimits.unlimited')
    expect(detailCard.text()).not.toContain('unlimited-model')
    expect(detailCard.find('[data-test="model-limits"]').exists()).toBe(false)

    for (const [utilization, tone] of [[69, 'green'], [70, 'yellow'], [90, 'yellow'], [91, 'red'], [100, 'red']] as const) {
      await wrapper.setProps({
        snapshot: snapshot({ overall_concurrency: usage(utilization, 100, utilization), models: [], saturated: utilization === 100 ? [{ model: '', dimension: 'concurrency' }] : [] }),
      })
      expect(wrapper.get('[data-test="primary-tone"]').attributes('data-tone')).toBe(tone)
    }

    await wrapper.setProps({ snapshot: snapshot({ usage_available: false }) })
    expect(wrapper.text()).toContain('modelRateLimits.unavailable')
    expect(detailCard.text()).not.toContain('0%')
  })
})
