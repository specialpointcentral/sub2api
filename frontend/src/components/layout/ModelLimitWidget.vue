<template>
  <div class="group relative ml-1 inline-flex items-center align-middle" :class="compact ? 'w-full' : ''">
    <button
      type="button"
      data-test="model-limit-trigger"
      class="flex min-w-[112px] items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-gray-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 dark:hover:bg-dark-800"
      :class="compact ? 'w-full' : ''"
    >
      <span class="min-w-0 flex-1">
        <span data-test="primary-dimension" class="block truncate text-[11px] text-gray-500 dark:text-gray-400">
          {{ primaryLabel }}
        </span>
        <span v-if="reached" class="block text-xs font-medium text-red-600 dark:text-red-400">
          {{ t('modelRateLimits.reached') }}
        </span>
      </span>
      <span data-test="primary-tone" :data-tone="primaryTone" class="w-10 shrink-0">
        <span class="block h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
          <span
            class="block h-full rounded-full transition-all"
            :class="toneClass"
            :style="{ width: `${primaryWidth}%` }"
          />
        </span>
      </span>
    </button>

    <div
      v-if="currentSnapshot"
      role="tooltip"
      data-test="model-limit-card"
      class="pointer-events-none absolute right-0 top-full mt-2 hidden w-56 rounded-lg border border-gray-200 bg-white p-3 text-xs shadow-lg group-hover:block group-focus-within:block dark:border-dark-700 dark:bg-dark-800"
    >
      <div v-if="currentSnapshot.usage_available === false" class="mb-2 font-medium text-amber-600 dark:text-amber-300">
        {{ t('modelRateLimits.unavailable') }}
      </div>
      <div data-test="overall-limits">
        <h3 class="mb-1.5 font-semibold text-gray-900 dark:text-white">{{ t('modelRateLimits.overallHeading') }}</h3>
        <div class="space-y-1.5">
          <div data-test="usage-progress" class="flex items-center gap-1">
            <span class="w-[32px] shrink-0 rounded bg-indigo-100 px-1 text-center text-[10px] font-medium text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300">
              {{ t('modelRateLimits.concurrency') }}
            </span>
            <span class="h-1.5 w-8 shrink-0 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
              <span
                class="block h-full transition-all duration-300"
                :class="usageBarClass(currentSnapshot.overall_concurrency)"
                :style="{ width: usageBarWidth(currentSnapshot.overall_concurrency) }"
              />
            </span>
          </div>
          <div v-if="currentSnapshot.overall_rpm" data-test="usage-progress" class="flex items-center gap-1">
            <span class="w-[32px] shrink-0 rounded bg-purple-100 px-1 text-center text-[10px] font-medium text-purple-700 dark:bg-purple-900/40 dark:text-purple-300">
              RPM
            </span>
            <span class="h-1.5 w-8 shrink-0 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
              <span
                class="block h-full transition-all duration-300"
                :class="usageBarClass(currentSnapshot.overall_rpm)"
                :style="{ width: usageBarWidth(currentSnapshot.overall_rpm) }"
              />
            </span>
          </div>
        </div>
      </div>

      <div
        v-if="limitedModels.length"
        data-test="model-limit-divider"
        class="mt-2 border-t border-gray-100 pt-2 dark:border-dark-700"
      >
        <div data-test="model-limits">
          <h3 class="mb-1.5 font-semibold text-gray-900 dark:text-white">{{ t('modelRateLimits.modelHeading') }}</h3>
          <div v-for="model in limitedModels" :key="model.model" class="mb-2 last:mb-0">
            <div class="mb-1 truncate font-medium text-gray-900 dark:text-gray-100">{{ model.model }}</div>
            <div class="space-y-1.5 pl-1">
              <div v-if="model.dimensions.concurrency?.limit != null" data-test="usage-progress">
                <UsageProgressBar
                  :label="t('modelRateLimits.concurrency')"
                  :utilization="model.dimensions.concurrency.utilization ?? 0"
                  :color="'indigo'"
                  :warning-at="70"
                  :danger-above="90"
                  :unavailable="!currentSnapshot.usage_available"
                  :value-text="usageText(model.dimensions.concurrency)"
                />
              </div>
              <div v-if="model.dimensions.rpm?.limit != null" data-test="usage-progress">
                <UsageProgressBar
                  :label="'RPM'"
                  :utilization="model.dimensions.rpm.utilization ?? 0"
                  :color="'purple'"
                  :warning-at="70"
                  :danger-above="90"
                  :unavailable="!currentSnapshot.usage_available"
                  :value-text="usageText(model.dimensions.rpm)"
                />
              </div>
              <div v-if="model.dimensions.tpm?.limit != null" data-test="usage-progress">
                <UsageProgressBar
                  :label="'TPM'"
                  :utilization="model.dimensions.tpm.utilization ?? 0"
                  :color="'amber'"
                  :warning-at="70"
                  :danger-above="90"
                  :unavailable="!currentSnapshot.usage_available"
                  :value-text="usageText(model.dimensions.tpm)"
                />
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="currentSnapshot.saturated.length" class="mt-2 border-t border-gray-100 pt-2 dark:border-dark-700">
        <div
          v-for="item in currentSnapshot.saturated"
          :key="`${item.model}:${item.dimension}`"
          data-test="saturated-item"
          class="text-[10px] text-red-600 dark:text-red-300"
        >
          {{ item.model || t('modelRateLimits.overall') }} · {{ dimensionLabel(item.dimension) }} · {{ t('modelRateLimits.reached') }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import UsageProgressBar from '@/components/account/UsageProgressBar.vue'
import { getModelRateLimitSnapshot, type ModelRateLimitSnapshot, type ModelRateLimitUsage } from '@/api/modelRateLimits'

const DEFAULT_MODEL_LIMIT_REFRESH_MS = 5000
const MAX_MODEL_LIMIT_BACKOFF_MS = 60_000
const props = withDefaults(defineProps<{
  snapshot?: ModelRateLimitSnapshot
  poll?: boolean
  compact?: boolean
}>(), { poll: true, compact: false })
const emit = defineEmits<{ (event: 'update:snapshot', snapshot: ModelRateLimitSnapshot): void }>()

const { t } = useI18n()
const fetchedSnapshot = ref<ModelRateLimitSnapshot>()
const currentSnapshot = computed(() => props.snapshot ?? fetchedSnapshot.value)
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let failures = 0

type Primary = { model: string; dimension: 'concurrency' | 'rpm' | 'tpm'; usage: ModelRateLimitUsage; overall: boolean }

const saturatedDimensions = computed<Primary[]>(() => {
  const snapshot = currentSnapshot.value
  if (!snapshot) return []
  const result: Primary[] = []
  if (snapshot.overall_concurrency.saturated) result.push({ model: '', dimension: 'concurrency', usage: snapshot.overall_concurrency, overall: true })
  if (snapshot.overall_rpm?.saturated) result.push({ model: '', dimension: 'rpm', usage: snapshot.overall_rpm, overall: true })
  for (const model of snapshot.models) {
    for (const dimension of ['concurrency', 'rpm', 'tpm'] as const) {
      const usage = model.dimensions[dimension]
      if (usage?.saturated) result.push({ model: model.model, dimension, usage, overall: false })
    }
  }
  return result.sort((a, b) => {
    const utilization = (b.usage.utilization ?? 0) - (a.usage.utilization ?? 0)
    if (utilization !== 0) return utilization
    const order = { concurrency: 0, rpm: 1, tpm: 2 }
    if (order[a.dimension] !== order[b.dimension]) return order[a.dimension] - order[b.dimension]
    return a.model.localeCompare(b.model)
  })
})

const limitedModels = computed(() => (currentSnapshot.value?.models ?? []).filter((model) =>
  model.dimensions.concurrency?.limit != null
  || model.dimensions.rpm?.limit != null
  || model.dimensions.tpm?.limit != null
))

const unrestricted = computed(() => {
  const snapshot = currentSnapshot.value
  return Boolean(snapshot && snapshot.overall_concurrency.limit == null && snapshot.overall_rpm?.limit == null && limitedModels.value.length === 0)
})

const primary = computed<Primary | undefined>(() => saturatedDimensions.value[0] ?? (currentSnapshot.value
  ? { model: '', dimension: 'concurrency', usage: currentSnapshot.value.overall_concurrency, overall: true }
  : undefined))
const reached = computed(() => saturatedDimensions.value.length > 0)
const primaryLabel = computed(() => {
  if (!currentSnapshot.value?.usage_available) return t('modelRateLimits.unavailable')
  if (unrestricted.value) return t('modelRateLimits.unlimited')
  const value = primary.value
  if (!value) return t('modelRateLimits.overallConcurrency')
  if (value.overall && value.dimension === 'concurrency') return t('modelRateLimits.overallConcurrency')
  return value.overall ? `${t('modelRateLimits.overall')} ${dimensionLabel(value.dimension)}` : `${value.model} ${dimensionLabel(value.dimension)}`
})
const primaryUtilization = computed(() => primary.value?.usage.utilization ?? 0)
const primaryWidth = computed(() => primary.value?.usage.limit == null ? 35 : Math.min(100, Math.max(0, primaryUtilization.value)))
const primaryTone = computed(() => {
  if (!currentSnapshot.value?.usage_available || primary.value?.usage.limit == null) return 'neutral'
  if (primaryUtilization.value > 90) return 'red'
  if (primaryUtilization.value >= 70) return 'yellow'
  return 'green'
})
const toneClass = computed(() => ({ red: 'bg-red-500', yellow: 'bg-amber-500', green: 'bg-green-500', neutral: 'bg-gray-400' }[primaryTone.value]))

function usageText(usage: ModelRateLimitUsage) {
  const used = usage.used == null ? '—' : usage.used
  const limit = usage.limit == null ? '∞' : usage.limit
  return `${used}/${limit}`
}

function usageBarWidth(usage: ModelRateLimitUsage) {
  if (!currentSnapshot.value?.usage_available) return '0%'
  if (usage.limit == null) return '35%'
  return `${Math.min(100, Math.max(0, usage.utilization ?? 0))}%`
}

function usageBarClass(usage: ModelRateLimitUsage) {
  if (!currentSnapshot.value?.usage_available || usage.limit == null) return 'bg-gray-400 dark:bg-gray-500'
  if ((usage.utilization ?? 0) > 90) return 'bg-red-500'
  if ((usage.utilization ?? 0) >= 70) return 'bg-amber-500'
  return 'bg-green-500'
}

function dimensionLabel(dimension: 'concurrency' | 'rpm' | 'tpm') {
  return dimension === 'concurrency' ? t('modelRateLimits.concurrency') : dimension.toUpperCase()
}

function schedule(delay: number) {
  clearTimeout(timer)
  const jitter = Math.round(delay * (Math.random() * 0.2 - 0.1))
  timer = setTimeout(refresh, Math.max(1000, delay + jitter))
}

async function refresh() {
  if (!props.poll || document.hidden) return
  controller?.abort()
  controller = new AbortController()
  try {
    fetchedSnapshot.value = await getModelRateLimitSnapshot(controller.signal)
    emit('update:snapshot', fetchedSnapshot.value)
    failures = 0
    schedule(fetchedSnapshot.value.refresh_after_ms || DEFAULT_MODEL_LIMIT_REFRESH_MS)
  } catch (error) {
    if (controller.signal.aborted) return
    failures += 1
    schedule(Math.min(MAX_MODEL_LIMIT_BACKOFF_MS, DEFAULT_MODEL_LIMIT_REFRESH_MS * 2 ** failures))
  }
}

function onVisibilityChange() {
  if (document.hidden) {
    clearTimeout(timer)
    controller?.abort()
  } else if (props.poll) {
    void refresh()
  }
}

watch(() => props.poll, (enabled) => enabled ? void refresh() : clearTimeout(timer))
onMounted(() => {
  document.addEventListener('visibilitychange', onVisibilityChange)
  if (props.poll && !props.snapshot) void refresh()
})
onBeforeUnmount(() => {
  clearTimeout(timer)
  controller?.abort()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>
