# Account Stats Live Users Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add live-only refresh, independent fixed-size pagination, aligned activity indicators, and mobile-friendly user lists to the account usage statistics dialog.

**Architecture:** Keep the existing backend contract and full-load flow. Add one guarded five-second polling path that updates only `recentUsers`, derive independent client-side slices for both lists, and render desktop tables plus mobile list rows from those slices. Reuse the shared `Pagination` component with its page-size selector disabled.

**Tech Stack:** Vue 3 Composition API, TypeScript, VueUse `useIntervalFn`, Vitest fake timers, Vue Test Utils, Tailwind CSS.

## Global Constraints

- Poll every five seconds only while the dialog is open and the document is visible.
- Polling and manual refresh call `getRecentUsers(accountId)` without date parameters and update only `recentUsers`.
- Never refresh summary cards, charts, model/endpoint statistics, or `rangeUsers` from the live polling path.
- Skip overlapping live refresh requests and ignore stale responses for a closed or different account.
- Read the fixed page size from `getConfiguredTableDefaultPageSize()` and render no page-size selector.
- Keep separate current/recent and range-user page state; clamp only when data shrink makes a page invalid.
- Use a dedicated activity column on desktop and a fixed activity slot on mobile.
- Below `sm`, show all row fields in an unframed vertical layout without horizontal scrolling.
- The hint-adjacent refresh control is icon-only, borderless, accessible, clickable, and spins only while refreshing.

---

## File Structure

- Create `frontend/src/components/admin/account/__tests__/AccountStatsModal.spec.ts`: component behavior tests for polling, pagination, refresh state, and responsive row rendering.
- Modify `frontend/src/components/admin/account/AccountStatsModal.vue`: live refresh lifecycle, pagination state/computed slices, refresh icon, desktop status column, and mobile list markup.
- Modify `docs/superpowers/specs/2026-07-25-account-stats-live-users-design.md`: already contains the approved behavior; no implementation edits expected.

### Task 1: Guarded Live-Only Refresh

**Files:**
- Create: `frontend/src/components/admin/account/__tests__/AccountStatsModal.spec.ts`
- Modify: `frontend/src/components/admin/account/AccountStatsModal.vue`

**Interfaces:**
- Consumes: `adminAPI.accounts.getRecentUsers(id, params?)`, `useIntervalFn(callback, 5000, { immediate: false })`, `props.show`, and `props.account.id`.
- Produces: `refreshRecentUsers(): Promise<void>` and reactive `recentUsersRefreshing` state used by polling and the manual icon.

- [x] **Step 1: Write failing live-refresh tests**

Create a Vitest suite that mounts the real `AccountStatsModal` while stubbing only heavyweight child components (`BaseDialog`, charts, spinner, icon, and pagination). Mock `adminAPI.accounts.getStats` and `getRecentUsers` with complete response objects. Use fake timers and assert observable API behavior:

```ts
it('refreshes only current/recent users every five seconds', async () => {
  const wrapper = mountModal()
  await flushPromises()
  getStats.mockClear()
  getRecentUsers.mockClear()

  await vi.advanceTimersByTimeAsync(5000)
  await flushPromises()

  expect(getRecentUsers).toHaveBeenCalledTimes(1)
  expect(getRecentUsers).toHaveBeenCalledWith(42)
  expect(getStats).not.toHaveBeenCalled()
  expect(wrapper.get('[data-testid="recent-user-2"]').text()).toContain('#2')
})
```

Add focused cases proving that `document.hidden` prevents polling, `show=false` stops polling, an unresolved refresh prevents another call, a late result after the dialog closes or changes account is ignored, and clicking the refresh control invokes the same no-range request while exposing a spinning state.

- [x] **Step 2: Run the test and verify RED**

Run:

```bash
cd frontend
pnpm test:run src/components/admin/account/__tests__/AccountStatsModal.spec.ts
```

Expected: FAIL because no five-second recent-user polling, refresh control, or guarded refresh state exists.

- [x] **Step 3: Implement the minimal guarded refresh path**

In `AccountStatsModal.vue`:

```ts
import { computed, ref, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'

const recentUsersRefreshing = ref(false)
let liveRefreshGeneration = 0

const refreshRecentUsers = async () => {
  const accountId = props.account?.id
  if (!props.show || !accountId || document.hidden || recentUsersRefreshing.value) return
  const generation = liveRefreshGeneration
  recentUsersRefreshing.value = true
  try {
    const response = await adminAPI.accounts.getRecentUsers(accountId)
    if (generation === liveRefreshGeneration && props.show && props.account?.id === accountId) {
      recentUsers.value = response.users || []
    }
  } catch (error) {
    console.error('Failed to refresh recent account users:', error)
  } finally {
    if (generation === liveRefreshGeneration) recentUsersRefreshing.value = false
  }
}

const { pause: pauseRecentUsersPolling, resume: resumeRecentUsersPolling } = useIntervalFn(
  refreshRecentUsers,
  5000,
  { immediate: false }
)
```

Extend the dialog watcher to increment the generation, reset state, resume on open, and pause on close. Keep the existing full `loadStats()` call for initial data. Render a compact icon-only control next to `currentUsersHint` with `title` and `aria-label` from `common.refresh`; bind `disabled` and `animate-spin motion-reduce:animate-none` to `recentUsersRefreshing`.

- [x] **Step 4: Run the test and verify GREEN**

Run the Task 1 command again. Expected: all live-refresh cases PASS with no unhandled promise warnings.

### Task 2: Independent Fixed-Size Pagination

**Files:**
- Modify: `frontend/src/components/admin/account/__tests__/AccountStatsModal.spec.ts`
- Modify: `frontend/src/components/admin/account/AccountStatsModal.vue`

**Interfaces:**
- Consumes: `getConfiguredTableDefaultPageSize(): number`, `Pagination` props `page`, `total`, `pageSize`, and `showPageSizeSelector`.
- Produces: `recentUsersPage`, `rangeUsersPage`, `paginatedRecentUsers`, and `paginatedRangeUsers`.

- [x] **Step 1: Write failing pagination tests**

Set `window.__APP_CONFIG__.table_default_page_size = 5`, return seven users in each API response, and assert that only users 1-5 are rendered initially. Emit `update:page` from each identifiable pagination stub and assert the current/recent and range lists change independently. Add a polling response that shrinks the recent list and verify its visible slice returns to page 1 while the range page is unchanged.

- [x] **Step 2: Run the test and verify RED**

Run the component test command. Expected: FAIL because all returned users render and no independent pagination state exists.

- [x] **Step 3: Implement fixed-size client pagination**

Import `Pagination` and `getConfiguredTableDefaultPageSize`. Add:

```ts
const usersPageSize = getConfiguredTableDefaultPageSize()
const recentUsersPage = ref(1)
const rangeUsersPage = ref(1)
const paginate = <T>(items: T[], page: number) =>
  items.slice((page - 1) * usersPageSize, page * usersPageSize)
const paginatedRecentUsers = computed(() => paginate(recentUsers.value, recentUsersPage.value))
const paginatedRangeUsers = computed(() => paginate(rangeUsers.value, rangeUsersPage.value))
```

Add page-clamp watchers using `Math.max(1, Math.ceil(total / usersPageSize))`. Reset both pages when opening, reset range page before date-range reloads, and update both desktop/mobile loops to use the paginated computed arrays. Place a `Pagination` below each list only when total rows exceed the page size:

```vue
<Pagination
  :page="recentUsersPage"
  :total="recentUsers.length"
  :page-size="usersPageSize"
  :show-page-size-selector="false"
  @update:page="recentUsersPage = $event"
/>
```

- [x] **Step 4: Run the test and verify GREEN**

Run the component test command. Expected: all independent pagination and clamping cases PASS.

### Task 3: Responsive Rows And Aligned Activity Indicator

**Files:**
- Modify: `frontend/src/components/admin/account/__tests__/AccountStatsModal.spec.ts`
- Modify: `frontend/src/components/admin/account/AccountStatsModal.vue`

**Interfaces:**
- Consumes: paginated user arrays from Task 2 and `RecentAccountUser.current_requests`.
- Produces: desktop table rows at `sm` and above, mobile vertical rows below `sm`, and a dedicated active-state slot in both renderings.

- [x] **Step 1: Write failing presentation tests**

Return one active and one inactive recent user. Assert that both desktop and mobile renderings expose all existing field labels/values, active markers exist only for the active user's dedicated status slot, and user IDs occupy a separate element from that slot. Assert the desktop container is hidden below `sm`, the mobile container is hidden at `sm` and above, and neither mobile list has an overflow/min-width table wrapper.

- [x] **Step 2: Run the test and verify RED**

Run the component test command. Expected: FAIL because the status dot shares the user-ID cell and mobile uses a horizontally scrolling table.

- [x] **Step 3: Implement desktop and mobile renderings**

For desktop, wrap each table in `hidden overflow-x-auto sm:block`, add a fixed `w-8` status header/cell, and render the active indicator as:

```vue
<span v-if="user.current_requests > 0" class="relative inline-flex h-2.5 w-2.5" aria-hidden="true">
  <span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-400 opacity-70 motion-reduce:animate-none" />
  <span class="relative inline-flex h-2.5 w-2.5 rounded-full bg-green-500" />
</span>
```

For mobile, add `divide-y sm:hidden` lists. Each row uses a fixed activity slot followed by a `min-w-0` identity block and a responsive two-column detail grid. Preserve user ID, email, current/range requests, account billed cost, user billed cost, and last-used time where applicable. Do not add nested cards.

- [x] **Step 4: Run focused tests and refactor**

Run the component suite. Remove duplicated helper logic only where it improves clarity; keep desktop/mobile markup explicit so their layouts remain independently understandable. Re-run until PASS.

### Task 4: Full Frontend Verification And Single-Commit Integration

**Files:**
- Modify: `docs/superpowers/plans/2026-07-25-account-stats-live-users.md` only to check completed boxes if desired.

**Interfaces:**
- Consumes: all implementation and tests from Tasks 1-3.
- Produces: one site-specific commit containing design, plan, tests, and implementation.

- [x] **Step 1: Run the focused component suite**

```bash
cd frontend
pnpm test:run src/components/admin/account/__tests__/AccountStatsModal.spec.ts
```

Expected: PASS.

- [x] **Step 2: Run frontend type checking**

```bash
cd frontend
pnpm typecheck
```

Expected: exit code 0.

- [x] **Step 3: Run frontend lint without auto-fixing**

```bash
cd frontend
pnpm lint:check
```

Expected: exit code 0 with no new lint findings.

- [x] **Step 4: Inspect the final diff and amend the feature commit**

```bash
git diff --check
git status --short
git diff --stat HEAD
git add docs/superpowers/plans/2026-07-25-account-stats-live-users.md \
  frontend/src/components/admin/account/AccountStatsModal.vue \
  frontend/src/components/admin/account/__tests__/AccountStatsModal.spec.ts
git commit --amend --no-edit
```

Verify that only the site-specific `21329a41b feat: refresh account stats users` commit is amended and no commit inherited from `main` is changed.
