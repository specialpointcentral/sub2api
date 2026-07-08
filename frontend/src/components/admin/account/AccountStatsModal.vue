<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.usageStatistics')"
    width="extra-wide"
    @close="handleClose"
  >
    <div class="space-y-6">
      <!-- Account Info Header -->
      <div
        v-if="account"
        class="flex items-center justify-between rounded-xl border border-primary-200 bg-gradient-to-r from-primary-50 to-primary-100 p-3 dark:border-primary-700/50 dark:from-primary-900/20 dark:to-primary-800/20"
      >
        <div class="flex items-center gap-3">
          <div
            class="flex h-10 w-10 items-center justify-center rounded-lg bg-gradient-to-br from-primary-500 to-primary-600"
          >
            <Icon name="chartBar" size="md" class="text-white" />
          </div>
          <div>
            <div class="font-semibold text-gray-900 dark:text-gray-100">{{ account.name }}</div>
            <div class="text-xs text-gray-500 dark:text-gray-400">
              {{ selectedRangeLabel }}
            </div>
          </div>
        </div>
        <span
          :class="[
            'rounded-full px-2.5 py-1 text-xs font-semibold',
            account.status === 'active'
              ? 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
              : 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400'
          ]"
        >
          {{ account.status }}
        </span>
      </div>

      <div
        v-if="account"
        class="flex flex-wrap items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-800"
      >
        <span class="text-xs font-medium text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.stats.dateRange') }}
        </span>
        <button
          v-for="range in dateRangeOptions"
          :key="range.key"
          type="button"
          class="rounded-lg border px-3 py-1.5 text-xs transition-colors"
          :class="currentRange === range.key
            ? 'border-primary-500 bg-primary-500 text-white'
            : 'border-gray-200 bg-white text-gray-700 hover:border-primary-300 dark:border-dark-600 dark:bg-dark-900 dark:text-dark-200 dark:hover:border-dark-500'"
          @click="setStatsRange(range.key)"
        >
          {{ range.label }}
        </button>
        <div v-if="currentRange === 'custom'" class="ml-1 flex flex-wrap items-center gap-2">
          <input
            v-model="customStartDate"
            type="date"
            class="rounded-lg border border-gray-200 bg-white px-2 py-1.5 text-xs text-gray-900 dark:border-dark-600 dark:bg-dark-900 dark:text-white"
          />
          <span class="text-xs text-gray-400">-</span>
          <input
            v-model="customEndDate"
            type="date"
            class="rounded-lg border border-gray-200 bg-white px-2 py-1.5 text-xs text-gray-900 dark:border-dark-600 dark:bg-dark-900 dark:text-white"
          />
          <button
            type="button"
            class="rounded-lg bg-primary-500 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-primary-600 disabled:cursor-not-allowed disabled:bg-gray-300 dark:disabled:bg-dark-600"
            :disabled="!canApplyCustomRange"
            @click="applyCustomRange"
          >
            {{ t('admin.accounts.stats.apply') }}
          </button>
        </div>
      </div>

      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>

      <template v-else-if="stats">
        <!-- Row 1: Main Stats Cards -->
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <!-- Total Cost -->
          <div
            class="card border-emerald-200 bg-gradient-to-br from-emerald-50 to-white p-4 dark:border-emerald-800/30 dark:from-emerald-900/10 dark:to-dark-700"
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.accounts.stats.totalCost')
              }}</span>
              <div class="rounded-lg bg-emerald-100 p-1.5 dark:bg-emerald-900/30">
                <Icon name="dollar" size="sm" class="text-emerald-600 dark:text-emerald-400" />
              </div>
            </div>
            <p class="text-2xl font-bold text-gray-900 dark:text-white">
              ${{ formatCost(stats.summary.total_cost) }}
            </p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.stats.accumulatedCost') }}
              <span class="text-gray-400 dark:text-gray-500">
                ({{ t('usage.userBilled') }}: ${{ formatCost(stats.summary.total_user_cost) }} ·
                {{ t('admin.accounts.stats.standardCost') }}: ${{
                  formatCost(stats.summary.total_standard_cost)
                }})
              </span>
            </p>
          </div>

          <!-- Total Requests -->
          <div
            class="card border-blue-200 bg-gradient-to-br from-blue-50 to-white p-4 dark:border-blue-800/30 dark:from-blue-900/10 dark:to-dark-700"
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.accounts.stats.totalRequests')
              }}</span>
              <div class="rounded-lg bg-blue-100 p-1.5 dark:bg-blue-900/30">
                <Icon name="bolt" size="sm" class="text-blue-600 dark:text-blue-400" />
              </div>
            </div>
            <p class="text-2xl font-bold text-gray-900 dark:text-white">
              {{ formatNumber(stats.summary.total_requests) }}
            </p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.stats.totalCalls') }}
            </p>
          </div>

          <!-- Daily Average Cost -->
          <div
            class="card border-amber-200 bg-gradient-to-br from-amber-50 to-white p-4 dark:border-amber-800/30 dark:from-amber-900/10 dark:to-dark-700"
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.accounts.stats.avgDailyCost')
              }}</span>
              <div class="rounded-lg bg-amber-100 p-1.5 dark:bg-amber-900/30">
                <Icon
                  name="calculator"
                  size="sm"
                  class="text-amber-600 dark:text-amber-400"
                />
              </div>
            </div>
            <p class="text-2xl font-bold text-gray-900 dark:text-white">
              ${{ formatCost(stats.summary.avg_daily_cost) }}
            </p>
             <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{
                t('admin.accounts.stats.basedOnActualDays', {
                  days: stats.summary.actual_days_used
                })
              }}
              <span class="text-gray-400 dark:text-gray-500">
                ({{ t('usage.userBilled') }}: ${{ formatCost(stats.summary.avg_daily_user_cost) }})
              </span>
            </p>
          </div>

          <!-- Daily Average Requests -->
          <div
            class="card border-purple-200 bg-gradient-to-br from-purple-50 to-white p-4 dark:border-purple-800/30 dark:from-purple-900/10 dark:to-dark-700"
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.accounts.stats.avgDailyRequests')
              }}</span>
              <div class="rounded-lg bg-purple-100 p-1.5 dark:bg-purple-900/30">
                <svg
                  class="h-4 w-4 text-purple-600 dark:text-purple-400"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M7 12l3-3 3 3 4-4M8 21l4-4 4 4M3 4h18M4 4h16v12a1 1 0 01-1 1H5a1 1 0 01-1-1V4z"
                  />
                </svg>
              </div>
            </div>
            <p class="text-2xl font-bold text-gray-900 dark:text-white">
              {{ formatNumber(Math.round(stats.summary.avg_daily_requests)) }}
            </p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.stats.avgDailyUsage') }}
            </p>
          </div>
        </div>

        <!-- Row 2: Selected Range, Highest Cost, Highest Requests -->
        <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <!-- Selected Range Overview -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-lg bg-cyan-100 p-1.5 dark:bg-cyan-900/30">
                <Icon name="clock" size="sm" class="text-cyan-600 dark:text-cyan-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.accounts.stats.selectedRangeOverview')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('usage.accountBilled') }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white"
                  >${{ formatCost(stats.summary.total_cost || 0) }}</span
                >
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('usage.userBilled') }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white"
                  >${{ formatCost(stats.summary.total_user_cost || 0) }}</span
                >
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.requests')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatNumber(stats.summary.total_requests || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.tokens')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatTokens(stats.summary.total_tokens || 0)
                }}</span>
              </div>
            </div>
          </div>

          <!-- Highest Cost Day -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-lg bg-orange-100 p-1.5 dark:bg-orange-900/30">
                <Icon name="fire" size="sm" class="text-orange-600 dark:text-orange-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.accounts.stats.highestCostDay')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.date')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  stats.summary.highest_cost_day?.label || '-'
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('usage.accountBilled') }}</span>
                <span class="text-sm font-semibold text-orange-600 dark:text-orange-400"
                  >${{ formatCost(stats.summary.highest_cost_day?.cost || 0) }}</span
                >
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('usage.userBilled') }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white"
                  >${{ formatCost(stats.summary.highest_cost_day?.user_cost || 0) }}</span
                >
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.requests')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatNumber(stats.summary.highest_cost_day?.requests || 0)
                }}</span>
              </div>
            </div>
          </div>

          <!-- Highest Request Day -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-lg bg-indigo-100 p-1.5 dark:bg-indigo-900/30">
                <Icon
                  name="trendingUp"
                  size="sm"
                  class="text-indigo-600 dark:text-indigo-400"
                />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.accounts.stats.highestRequestDay')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.date')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  stats.summary.highest_request_day?.label || '-'
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.requests')
                }}</span>
                <span class="text-sm font-semibold text-indigo-600 dark:text-indigo-400">{{
                  formatNumber(stats.summary.highest_request_day?.requests || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('usage.accountBilled') }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white"
                  >${{ formatCost(stats.summary.highest_request_day?.cost || 0) }}</span
                >
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('usage.userBilled') }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white"
                  >${{ formatCost(stats.summary.highest_request_day?.user_cost || 0) }}</span
                >
              </div>
            </div>
          </div>
        </div>

        <!-- Row 3: Token Stats -->
        <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <!-- Accumulated Tokens -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-lg bg-teal-100 p-1.5 dark:bg-teal-900/30">
                <Icon name="cube" size="sm" class="text-teal-600 dark:text-teal-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.accounts.stats.accumulatedTokens')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.totalTokens')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatTokens(stats.summary.total_tokens)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.dailyAvgTokens')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatTokens(Math.round(stats.summary.avg_daily_tokens))
                }}</span>
              </div>
            </div>
          </div>

          <!-- Performance -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-lg bg-rose-100 p-1.5 dark:bg-rose-900/30">
                <Icon name="bolt" size="sm" class="text-rose-600 dark:text-rose-400" />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.accounts.stats.performance')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.avgResponseTime')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatDuration(stats.summary.avg_duration_ms)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.daysActive')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white"
                  >{{ stats.summary.actual_days_used }} / {{ stats.summary.days }}</span
                >
              </div>
            </div>
          </div>

          <!-- Recent Activity -->
          <div class="card p-4">
            <div class="mb-3 flex items-center gap-2">
              <div class="rounded-lg bg-lime-100 p-1.5 dark:bg-lime-900/30">
                <Icon
                  name="clipboard"
                  size="sm"
                  class="text-lime-600 dark:text-lime-400"
                />
              </div>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.accounts.stats.recentActivity')
              }}</span>
            </div>
            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.todayRequests')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatNumber(stats.summary.today?.requests || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.todayTokens')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  formatTokens(stats.summary.today?.tokens || 0)
                }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.accounts.stats.todayCost')
                }}</span>
                <span class="text-sm font-semibold text-gray-900 dark:text-white"
                  >${{ formatCost(stats.summary.today?.cost || 0) }}</span
                >
              </div>
            </div>
          </div>
        </div>

        <!-- Account Users -->
        <div class="grid grid-cols-1 gap-4 xl:grid-cols-2">
          <div class="card p-4">
            <div class="mb-3 flex items-center justify-between gap-3">
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.accounts.stats.currentUsers') }}
              </h3>
              <span class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.accounts.stats.currentUsersHint') }}
              </span>
            </div>
            <div v-if="usersLoading" class="flex items-center justify-center py-8">
              <LoadingSpinner />
            </div>
            <div
              v-else-if="recentUsers.length === 0"
              class="rounded-lg border border-dashed border-gray-200 py-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
            >
              {{ t('admin.accounts.stats.noRecentUsers') }}
            </div>
            <div v-else class="overflow-x-auto">
              <table class="w-full min-w-[560px] divide-y divide-gray-200 text-sm dark:divide-dark-700">
                <thead class="bg-gray-50 dark:bg-dark-800">
                  <tr>
                    <th class="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('admin.accounts.stats.user') }}
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('admin.accounts.stats.email') }}
                    </th>
                    <th class="px-3 py-2 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('admin.accounts.stats.currentRequests') }}
                    </th>
                    <th class="px-3 py-2 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('admin.accounts.stats.lastUsedAt') }}
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                  <tr
                    v-for="user in recentUsers"
                    :key="`recent-${user.user_id}`"
                    :class="user.current_requests > 0 ? 'bg-green-50 dark:bg-green-900/10' : ''"
                  >
                    <td class="whitespace-nowrap px-3 py-2 text-gray-900 dark:text-white">
                      <span v-if="user.current_requests > 0" class="mr-2 inline-block h-2 w-2 rounded-full bg-green-500"></span>
                      #{{ user.user_id }}
                    </td>
                    <td class="whitespace-nowrap px-3 py-2 text-gray-600 dark:text-gray-300">{{ user.email || '-' }}</td>
                    <td class="whitespace-nowrap px-3 py-2 text-right tabular-nums text-gray-900 dark:text-white">
                      {{ user.current_requests || 0 }}
                    </td>
                    <td class="whitespace-nowrap px-3 py-2 text-right text-gray-500 dark:text-gray-400">
                      {{ formatDateTime(user.last_used_at) }}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          <div class="card p-4">
            <div class="mb-3 flex items-center justify-between gap-3">
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.accounts.stats.rangeUsers') }}
              </h3>
              <span class="text-xs text-gray-500 dark:text-gray-400">
                {{ selectedRangeLabel }}
              </span>
            </div>
            <div v-if="usersLoading" class="flex items-center justify-center py-8">
              <LoadingSpinner />
            </div>
            <div
              v-else-if="rangeUsers.length === 0"
              class="rounded-lg border border-dashed border-gray-200 py-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
            >
              {{ t('admin.accounts.stats.noRangeUsers') }}
            </div>
            <div v-else class="overflow-x-auto">
              <table class="w-full min-w-[680px] divide-y divide-gray-200 text-sm dark:divide-dark-700">
                <thead class="bg-gray-50 dark:bg-dark-800">
                  <tr>
                    <th class="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('admin.accounts.stats.user') }}
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('admin.accounts.stats.email') }}
                    </th>
                    <th class="px-3 py-2 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('admin.accounts.stats.requests') }}
                    </th>
                    <th class="px-3 py-2 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('usage.accountBilled') }}
                    </th>
                    <th class="px-3 py-2 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                      {{ t('usage.userBilled') }}
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                  <tr v-for="user in rangeUsers" :key="`range-${user.user_id}`">
                    <td class="whitespace-nowrap px-3 py-2 text-gray-900 dark:text-white">#{{ user.user_id }}</td>
                    <td class="whitespace-nowrap px-3 py-2 text-gray-600 dark:text-gray-300">{{ user.email || '-' }}</td>
                    <td class="whitespace-nowrap px-3 py-2 text-right tabular-nums text-gray-900 dark:text-white">
                      {{ formatNumber(user.requests || 0) }}
                    </td>
                    <td class="whitespace-nowrap px-3 py-2 text-right tabular-nums text-gray-900 dark:text-white">
                      ${{ formatCost(user.account_cost || 0) }}
                    </td>
                    <td class="whitespace-nowrap px-3 py-2 text-right tabular-nums text-gray-900 dark:text-white">
                      ${{ formatCost(user.user_cost || 0) }}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <!-- Usage Trend Chart -->
        <div class="card p-4">
          <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.accounts.stats.usageTrend') }}
          </h3>
          <div class="h-64">
            <Line v-if="trendChartData" :data="trendChartData" :options="lineChartOptions" />
            <div
              v-else
              class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400"
            >
              {{ t('admin.dashboard.noDataAvailable') }}
            </div>
          </div>
        </div>

        <!-- Model Distribution -->
        <ModelDistributionChart :model-stats="stats.models" :loading="false" />

        <EndpointDistributionChart
          :endpoint-stats="stats.endpoints || []"
          :loading="false"
          :title="t('usage.inboundEndpoint')"
        />

        <EndpointDistributionChart
          :endpoint-stats="stats.upstream_endpoints || []"
          :loading="false"
          :title="t('usage.upstreamEndpoint')"
        />
      </template>

      <!-- No Data State -->
      <div
        v-else-if="!loading"
        class="flex flex-col items-center justify-center py-12 text-gray-500 dark:text-gray-400"
      >
        <Icon name="chartBar" size="xl" class="mb-4 h-12 w-12" />
        <p class="text-sm">{{ t('admin.accounts.stats.noData') }}</p>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button
          @click="handleClose"
          class="rounded-lg bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import EndpointDistributionChart from '@/components/charts/EndpointDistributionChart.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import type { AccountStatsParams, RecentAccountUser } from '@/api/admin/accounts'
import type { Account, AccountUsageStatsResponse } from '@/types'

ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler
)

const { t } = useI18n()

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const loading = ref(false)
const usersLoading = ref(false)
const stats = ref<AccountUsageStatsResponse | null>(null)
const recentUsers = ref<RecentAccountUser[]>([])
const rangeUsers = ref<RecentAccountUser[]>([])
type StatsRangeKey = 'today' | '7d' | '30d' | '90d' | 'custom'
const currentRange = ref<StatsRangeKey>('30d')
const customStartDate = ref('')
const customEndDate = ref('')

// Dark mode detection
const isDarkMode = computed(() => {
  return document.documentElement.classList.contains('dark')
})

// Chart colors
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#e5e7eb' : '#374151',
  grid: isDarkMode.value ? '#374151' : '#e5e7eb'
}))

const dateRangeOptions = computed(() => [
  { key: 'today' as const, label: t('admin.accounts.stats.dateRangeToday') },
  { key: '7d' as const, label: t('admin.accounts.stats.dateRange7d') },
  { key: '30d' as const, label: t('admin.accounts.stats.dateRange30d') },
  { key: '90d' as const, label: t('admin.accounts.stats.dateRange90d') },
  { key: 'custom' as const, label: t('admin.accounts.stats.dateRangeCustom') }
])

const browserTimezone = (): string => Intl.DateTimeFormat().resolvedOptions().timeZone

const selectedDateParams = computed<AccountStatsParams>(() => {
  if (currentRange.value === 'custom') {
    return {
      start_date: customStartDate.value || undefined,
      end_date: customEndDate.value || undefined,
      timezone: browserTimezone()
    }
  }

  const today = new Date()
  const end = formatDateParam(today)
  const days = currentRange.value === 'today'
    ? 1
    : currentRange.value === '7d'
      ? 7
      : currentRange.value === '90d'
        ? 90
        : 30
  const start = new Date(today.getTime() - (days - 1) * 24 * 60 * 60 * 1000)

  return {
    start_date: formatDateParam(start),
    end_date: end,
    timezone: browserTimezone()
  }
})

const selectedRangeLabel = computed(() => {
  if (currentRange.value === 'custom') {
    if (customStartDate.value && customEndDate.value) {
      return `${customStartDate.value} - ${customEndDate.value}`
    }
    return t('admin.accounts.stats.dateRangeCustom')
  }
  const option = dateRangeOptions.value.find((range) => range.key === currentRange.value)
  return option?.label || t('admin.accounts.stats.dateRange30d')
})

const canApplyCustomRange = computed(() => Boolean(
  customStartDate.value &&
  customEndDate.value &&
  customStartDate.value <= customEndDate.value
))

// Line chart data
const trendChartData = computed(() => {
  if (!stats.value?.history?.length) return null

  return {
    labels: stats.value.history.map((h) => h.label),
    datasets: [
      {
        label: t('usage.accountBilled') + ' (USD)',
        data: stats.value.history.map((h) => h.actual_cost),
        borderColor: '#3b82f6',
        backgroundColor: 'rgba(59, 130, 246, 0.1)',
        fill: true,
        tension: 0.3,
        yAxisID: 'y'
      },
      {
        label: t('usage.userBilled') + ' (USD)',
        data: stats.value.history.map((h) => h.user_cost),
        borderColor: '#10b981',
        backgroundColor: 'rgba(16, 185, 129, 0.08)',
        fill: false,
        tension: 0.3,
        borderDash: [5, 5],
        yAxisID: 'y'
      },
      {
        label: t('admin.accounts.stats.requests'),
        data: stats.value.history.map((h) => h.requests),
        borderColor: '#f97316',
        backgroundColor: 'rgba(249, 115, 22, 0.1)',
        fill: false,
        tension: 0.3,
        yAxisID: 'y1'
      }
    ]
  }
})

// Line chart options with dual Y-axis
const lineChartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    intersect: false,
    mode: 'index' as const
  },
  plugins: {
    legend: {
      position: 'top' as const,
      labels: {
        color: chartColors.value.text,
        usePointStyle: true,
        pointStyle: 'circle',
        padding: 15,
        font: {
          size: 11
        }
      }
    },
    tooltip: {
      callbacks: {
        label: (context: any) => {
          const label = context.dataset.label || ''
          const value = context.raw
          if (label.includes('USD')) {
            return `${label}: $${formatCost(value)}`
          }
          return `${label}: ${formatNumber(value)}`
        }
      }
    }
  },
  scales: {
    x: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        },
        maxRotation: 45,
        minRotation: 0
      }
    },
    y: {
      type: 'linear' as const,
      display: true,
      position: 'left' as const,
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: '#3b82f6',
        font: {
          size: 10
        },
        callback: (value: string | number) => '$' + formatCost(Number(value))
      },
      title: {
        display: true,
        text: t('usage.accountBilled') + ' (USD)',
        color: '#3b82f6',
        font: {
          size: 11
        }
      }
    },
    y1: {
      type: 'linear' as const,
      display: true,
      position: 'right' as const,
      grid: {
        drawOnChartArea: false
      },
      ticks: {
        color: '#f97316',
        font: {
          size: 10
        },
        callback: (value: string | number) => formatNumber(Number(value))
      },
      title: {
        display: true,
        text: t('admin.accounts.stats.requests'),
        color: '#f97316',
        font: {
          size: 11
        }
      }
    }
  }
}))

// Load stats when modal opens
watch(
  () => props.show,
  async (newVal) => {
    if (newVal && props.account) {
      await loadStats()
    } else {
      stats.value = null
      recentUsers.value = []
      rangeUsers.value = []
    }
  }
)

const loadStats = async () => {
  if (!props.account) return
  const rangeParams = selectedDateParams.value
  if (!rangeParams.start_date || !rangeParams.end_date) return

  loading.value = true
  try {
    await Promise.all([
      loadUsageStats(props.account.id, rangeParams),
      loadUserStats(props.account.id, rangeParams)
    ])
  } catch (error) {
    console.error('Failed to load account stats:', error)
    stats.value = null
  } finally {
    loading.value = false
  }
}

const loadUsageStats = async (accountId: number, rangeParams: AccountStatsParams) => {
  stats.value = await adminAPI.accounts.getStats(accountId, rangeParams)
}

const loadUserStats = async (accountId: number, rangeParams: AccountStatsParams) => {
  usersLoading.value = true
  try {
    const [recent, range] = await Promise.all([
      adminAPI.accounts.getRecentUsers(accountId),
      adminAPI.accounts.getRecentUsers(accountId, rangeParams)
    ])
    recentUsers.value = recent.users || []
    rangeUsers.value = range.users || []
  } catch (error) {
    console.error('Failed to load account users:', error)
    recentUsers.value = []
    rangeUsers.value = []
  } finally {
    usersLoading.value = false
  }
}

const setStatsRange = async (range: StatsRangeKey) => {
  if (range === 'custom') {
    if (!customStartDate.value || !customEndDate.value) {
      const today = new Date()
      const params = {
        start_date: formatDateParam(new Date(today.getTime() - 29 * 24 * 60 * 60 * 1000)),
        end_date: formatDateParam(today)
      }
      customStartDate.value = params.start_date || ''
      customEndDate.value = params.end_date || ''
    }
    currentRange.value = range
    return
  }
  currentRange.value = range
  await loadStats()
}

const applyCustomRange = async () => {
  if (!canApplyCustomRange.value) return
  await loadStats()
}

const handleClose = () => {
  emit('close')
}

// Format helpers
const formatDateParam = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const formatDateTime = (value: string): string => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString()
}

const formatCost = (value: number): string => {
  if (value >= 1000) {
    return (value / 1000).toFixed(2) + 'K'
  } else if (value >= 1) {
    return value.toFixed(2)
  } else if (value >= 0.01) {
    return value.toFixed(3)
  }
  return value.toFixed(4)
}

const formatNumber = (value: number): string => {
  if (value >= 1_000_000) {
    return (value / 1_000_000).toFixed(2) + 'M'
  } else if (value >= 1_000) {
    return (value / 1_000).toFixed(2) + 'K'
  }
  return value.toLocaleString()
}

const formatTokens = (value: number): string => {
  if (value >= 1_000_000_000) {
    return `${(value / 1_000_000_000).toFixed(2)}B`
  } else if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(2)}M`
  } else if (value >= 1_000) {
    return `${(value / 1_000).toFixed(2)}K`
  }
  return value.toLocaleString()
}

const formatDuration = (ms: number): string => {
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
}
</script>
