import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

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
    expect(wrapper.text()).toContain('modelRateLimits.reached')
    expect(wrapper.get('[data-test="primary-tone"]').attributes('data-tone')).toBe('red')

    const trigger = wrapper.get('[data-test="model-limit-trigger"]')
    await trigger.trigger('focusin')
    await nextTick()
    const tooltip = document.body.querySelector('[role="tooltip"]') as HTMLElement
    expect(tooltip.style.display).not.toBe('none')
    expect(tooltip.textContent).toContain('modelRateLimits.overallHeading')
    expect(tooltip.textContent).toContain('modelRateLimits.modelHeading')
    expect(tooltip.textContent).toContain('gpt-5.6-luna-high')
    expect(tooltip.querySelectorAll('[data-test="saturated-item"]')).toHaveLength(3)
    expect(tooltip.textContent).not.toContain('unlimited-model')
    expect(tooltip.querySelectorAll('[data-test="usage-progress"]')).toHaveLength(4)
    await trigger.trigger('focusout')

    await wrapper.setProps({
      snapshot: snapshot({ overall_concurrency: usage(2, null, null), models: [], saturated: [] }),
    })
    expect(wrapper.text()).toContain('modelRateLimits.unlimited')

    for (const [utilization, tone] of [[69, 'green'], [70, 'yellow'], [90, 'yellow'], [91, 'red'], [100, 'red']] as const) {
      await wrapper.setProps({
        snapshot: snapshot({ overall_concurrency: usage(utilization, 100, utilization), models: [], saturated: utilization === 100 ? [{ model: '', dimension: 'concurrency' }] : [] }),
      })
      expect(wrapper.get('[data-test="primary-tone"]').attributes('data-tone')).toBe(tone)
    }

    await wrapper.setProps({ snapshot: snapshot({ usage_available: false }) })
    expect(wrapper.text()).toContain('modelRateLimits.unavailable')
	await trigger.trigger('focusin')
	await nextTick()
	expect((document.body.querySelector('[role="tooltip"]') as HTMLElement).textContent).not.toContain('0%')
  })
})
