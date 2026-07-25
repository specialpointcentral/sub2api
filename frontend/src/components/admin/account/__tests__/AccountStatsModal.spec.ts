import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AccountUsageStatsResponse } from '@/types'
import AccountStatsModal from '../AccountStatsModal.vue'

const { getStats, getRecentUsers } = vi.hoisted(() => ({
  getStats: vi.fn(),
  getRecentUsers: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getStats,
      getRecentUsers
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const account = {
  id: 42,
  name: 'Primary OpenAI',
  platform: 'openai',
  type: 'oauth',
  status: 'active'
}

const statsResponse: AccountUsageStatsResponse = {
  history: [],
  summary: {
    days: 30,
    actual_days_used: 0,
    total_cost: 0,
    total_user_cost: 0,
    total_standard_cost: 0,
    total_requests: 0,
    total_tokens: 0,
    avg_daily_cost: 0,
    avg_daily_user_cost: 0,
    avg_daily_requests: 0,
    avg_daily_tokens: 0,
    avg_duration_ms: 0,
    today: null,
    highest_cost_day: null,
    highest_request_day: null
  },
  models: [],
  endpoints: [],
  upstream_endpoints: []
}

const initialRecentUsers = {
  users: [
    {
      user_id: 1,
      email: 'initial@example.com',
      requests: 1,
      current_requests: 1,
      account_cost: 0.1,
      user_cost: 0.2,
      last_used_at: '2026-07-25T00:00:00Z'
    }
  ]
}

const initialRangeUsers = {
  users: [
    {
      user_id: 10,
      email: 'range@example.com',
      requests: 8,
      current_requests: 0,
      account_cost: 1,
      user_cost: 2,
      last_used_at: '2026-07-24T00:00:00Z'
    }
  ]
}

const freshRecentUsers = {
  users: [
    {
      user_id: 2,
      email: 'fresh@example.com',
      requests: 2,
      current_requests: 2,
      account_cost: 0.3,
      user_cost: 0.4,
      last_used_at: '2026-07-25T00:01:00Z'
    }
  ]
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function userList(prefix: string, count: number, currentRequests = 0) {
  return {
    users: Array.from({ length: count }, (_, index) => {
      const id = index + 1
      return {
        user_id: id,
        email: `${prefix}-${id}@example.com`,
        requests: id,
        current_requests: currentRequests,
        account_cost: id / 10,
        user_cost: id / 5,
        last_used_at: `2026-07-25T00:${String(index).padStart(2, '0')}:00Z`
      }
    })
  }
}

function mountModal(): VueWrapper {
  return mount(AccountStatsModal, {
    props: {
      show: false,
      account: account as any
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        LoadingSpinner: true,
        ModelDistributionChart: true,
        EndpointDistributionChart: true,
        Line: true,
        Icon: {
          props: ['name'],
          template: '<span class="icon-stub" :data-icon="name" />'
        },
        Pagination: {
          name: 'PaginationStub',
          inheritAttrs: false,
          props: ['page', 'total', 'pageSize', 'showPageSizeSelector'],
          emits: ['update:page'],
          template: `
            <div v-bind="$attrs" class="pagination-stub" :data-page="page" :data-total="total" :data-page-size="pageSize">
              <button class="pagination-next" type="button" @click="$emit('update:page', page + 1)">next</button>
            </div>
          `
        }
      }
    }
  })
}

async function openModal(
  wrapper: VueWrapper,
  recentUsers = initialRecentUsers,
  rangeUsers = initialRangeUsers
) {
  getStats.mockResolvedValue(statsResponse)
  getRecentUsers
    .mockResolvedValueOnce(recentUsers)
    .mockResolvedValueOnce(rangeUsers)

  await wrapper.setProps({ show: true })
  await flushPromises()

  expect(getStats).toHaveBeenCalledTimes(1)
  expect(getRecentUsers).toHaveBeenCalledTimes(2)
  getStats.mockClear()
  getRecentUsers.mockClear()
}

describe('AccountStatsModal live recent users', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      value: false
    })
  })

  afterEach(() => {
    delete (window as any).__APP_CONFIG__
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('refreshes only the current/recent list every five seconds', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)
    getRecentUsers.mockResolvedValue(freshRecentUsers)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()

    expect(getRecentUsers).toHaveBeenCalledTimes(1)
    expect(getRecentUsers).toHaveBeenCalledWith(42)
    expect(getStats).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('fresh@example.com')
    expect(wrapper.text()).toContain('range@example.com')
  })

  it('does not poll while hidden or after the dialog closes', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)
    getRecentUsers.mockResolvedValue(freshRecentUsers)

    Object.defineProperty(document, 'hidden', {
      configurable: true,
      value: true
    })
    await vi.advanceTimersByTimeAsync(5000)
    expect(getRecentUsers).not.toHaveBeenCalled()

    Object.defineProperty(document, 'hidden', {
      configurable: true,
      value: false
    })
    await wrapper.setProps({ show: false })
    await vi.advanceTimersByTimeAsync(5000)
    expect(getRecentUsers).not.toHaveBeenCalled()
  })

  it('does not overlap live refresh requests', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)
    const pending = deferred<typeof freshRecentUsers>()
    getRecentUsers.mockReturnValue(pending.promise)

    await vi.advanceTimersByTimeAsync(5000)
    await vi.advanceTimersByTimeAsync(5000)

    expect(getRecentUsers).toHaveBeenCalledTimes(1)

    pending.resolve(freshRecentUsers)
    await flushPromises()
    expect(wrapper.text()).toContain('fresh@example.com')
  })

  it('manually refreshes only recent users and spins the icon while pending', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)
    const pending = deferred<typeof freshRecentUsers>()
    getRecentUsers.mockReturnValue(pending.promise)

    const refresh = wrapper.get('[aria-label="common.refresh"]')
    await refresh.trigger('click')

    expect(getRecentUsers).toHaveBeenCalledTimes(1)
    expect(getRecentUsers).toHaveBeenCalledWith(42)
    expect(getStats).not.toHaveBeenCalled()
    expect(refresh.find('[data-icon="refresh"]').classes()).toContain('animate-spin')

    pending.resolve(freshRecentUsers)
    await flushPromises()

    expect(refresh.find('[data-icon="refresh"]').classes()).not.toContain('animate-spin')
    expect(wrapper.text()).toContain('fresh@example.com')
  })
})

describe('AccountStatsModal user pagination', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      value: false
    })
    ;(window as any).__APP_CONFIG__ = {
      table_default_page_size: 5,
      table_page_size_options: [5, 10, 20]
    }
  })

  afterEach(() => {
    delete (window as any).__APP_CONFIG__
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('paginates current and range users independently with the configured default size', async () => {
    const wrapper = mountModal()
    await openModal(wrapper, userList('recent', 7), userList('range', 7))

    const currentList = wrapper.get('[data-testid="current-users-list"]')
    const rangeList = wrapper.get('[data-testid="range-users-list"]')
    expect(currentList.text()).toContain('recent-5@example.com')
    expect(currentList.text()).not.toContain('recent-6@example.com')
    expect(rangeList.text()).toContain('range-5@example.com')
    expect(rangeList.text()).not.toContain('range-6@example.com')

    const currentPagination = wrapper.get('[data-testid="current-users-pagination"]')
    expect(currentPagination.attributes('data-page-size')).toBe('5')
    const paginationStubs = wrapper.findAllComponents({ name: 'PaginationStub' })
    expect(paginationStubs).toHaveLength(2)
    expect(paginationStubs.every((pagination) => pagination.props('showPageSizeSelector') === false)).toBe(true)
    await currentPagination.get('.pagination-next').trigger('click')

    expect(currentList.text()).toContain('recent-6@example.com')
    expect(rangeList.text()).toContain('range-1@example.com')
    expect(rangeList.text()).not.toContain('range-6@example.com')

    await wrapper.get('[data-testid="range-users-pagination"] .pagination-next').trigger('click')
    expect(rangeList.text()).toContain('range-6@example.com')
    expect(currentList.text()).toContain('recent-6@example.com')
  })

  it('clamps only the live-user page when refreshed data shrink', async () => {
    const wrapper = mountModal()
    await openModal(wrapper, userList('recent', 7), userList('range', 7))

    await wrapper.get('[data-testid="current-users-pagination"] .pagination-next').trigger('click')
    await wrapper.get('[data-testid="range-users-pagination"] .pagination-next').trigger('click')
    expect(wrapper.get('[data-testid="current-users-pagination"]').attributes('data-page')).toBe('2')
    expect(wrapper.get('[data-testid="range-users-pagination"]').attributes('data-page')).toBe('2')

    getRecentUsers.mockResolvedValue(userList('fresh', 2))
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()

    expect(wrapper.find('[data-testid="current-users-pagination"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="current-users-list"]').text()).toContain('fresh-1@example.com')
    expect(wrapper.get('[data-testid="range-users-pagination"]').attributes('data-page')).toBe('2')
    expect(wrapper.get('[data-testid="range-users-list"]').text()).toContain('range-6@example.com')
  })
})

describe('AccountStatsModal responsive user lists', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      value: false
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('renders activity in a dedicated desktop column and keeps inactive rows aligned', async () => {
    const wrapper = mountModal()
    await openModal(wrapper, {
      users: [
        initialRecentUsers.users[0],
        {
          ...initialRecentUsers.users[0],
          user_id: 2,
          email: 'inactive@example.com',
          current_requests: 0
        }
      ]
    })

    const desktop = wrapper.get('[data-testid="current-users-desktop"]')
    expect(desktop.classes()).toContain('hidden')
    expect(desktop.classes()).toContain('sm:block')
    const rows = desktop.findAll('tbody tr')
    expect(rows).toHaveLength(2)

    const activeCells = rows[0].findAll('td')
    expect(activeCells[0].classes()).toContain('w-8')
    expect(activeCells[0].find('.current-user-active-indicator').exists()).toBe(true)
    expect(activeCells[0].find('.animate-ping').classes()).toContain('motion-reduce:animate-none')
    expect(activeCells[1].text()).toBe('#1')

    const inactiveCells = rows[1].findAll('td')
    expect(inactiveCells[0].classes()).toContain('w-8')
    expect(inactiveCells[0].find('.current-user-active-indicator').exists()).toBe(false)
    expect(inactiveCells[1].text()).toBe('#2')
  })

  it('renders complete mobile rows without a horizontally scrolling table', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)

    const currentMobile = wrapper.get('[data-testid="current-users-mobile"]')
    expect(currentMobile.classes()).toContain('sm:hidden')
    expect(currentMobile.find('table').exists()).toBe(false)
    expect(currentMobile.text()).toContain('#1')
    expect(currentMobile.text()).toContain('initial@example.com')
    expect(currentMobile.text()).toContain('admin.accounts.stats.currentRequests')
    expect(currentMobile.text()).toContain('usage.accountBilled')
    expect(currentMobile.text()).toContain('usage.userBilled')
    expect(currentMobile.text()).toContain('admin.accounts.stats.lastUsedAt')
    expect(currentMobile.get('.current-user-status').classes()).toContain('w-5')
    expect(currentMobile.find('.current-user-active-indicator').exists()).toBe(true)

    const rangeDesktop = wrapper.get('[data-testid="range-users-desktop"]')
    expect(rangeDesktop.classes()).toContain('hidden')
    expect(rangeDesktop.classes()).toContain('sm:block')

    const rangeMobile = wrapper.get('[data-testid="range-users-mobile"]')
    expect(rangeMobile.classes()).toContain('sm:hidden')
    expect(rangeMobile.find('table').exists()).toBe(false)
    expect(rangeMobile.text()).toContain('#10')
    expect(rangeMobile.text()).toContain('range@example.com')
    expect(rangeMobile.text()).toContain('admin.accounts.stats.requests')
    expect(rangeMobile.text()).toContain('usage.accountBilled')
    expect(rangeMobile.text()).toContain('usage.userBilled')
  })
})
