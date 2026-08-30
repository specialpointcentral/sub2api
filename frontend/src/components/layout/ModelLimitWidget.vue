<template>
  <HelpTooltip width-class="w-80">
    <template #trigger>
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
          <span class="block text-xs font-medium text-gray-800 dark:text-gray-200">
            {{ primaryText }}
            <span v-if="reached" class="ml-1 text-red-600 dark:text-red-400">{{ t('modelRateLimits.reached') }}</span>
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
    </template>

    <div v-if="currentSnapshot" class="space-y-3">
      <div v-if="currentSnapshot.usage_available === false" class="font-medium text-amber-300">
        {{ t('modelRateLimits.unavailable') }}
      </div>
      <div>
        <h3 class="mb-1.5 font-semibold text-white">{{ t('modelRateLimits.overallHeading') }}</h3>
        <div class="space-y-1.5">
          <div data-test="usage-progress">
            <UsageProgressBar
              :label="t('modelRateLimits.concurrency')"
              :utilization="currentSnapshot.overall_concurrency.utilization ?? 0"
              :color="'indigo'"
              :warning-at="70"
              :danger-above="90"
			  :unavailable="!currentSnapshot.usage_available"
            />
            <span class="text-[10px] text-gray-300">{{ usageText(currentSnapshot.overall_concurrency) }}</span>
          </div>
          <div v-if="currentSnapshot.overall_rpm" data-test="usage-progress">
            <UsageProgressBar
              :label="'RPM'"
              :utilization="currentSnapshot.overall_rpm.utilization ?? 0"
              :color="'purple'"
              :warning-at="70"
              :danger-above="90"
			  :unavailable="!currentSnapshot.usage_available"
            />
            <span class="text-[10px] text-gray-300">{{ usageText(currentSnapshot.overall_rpm) }}</span>
          </div>
        </div>
      </div>

      <div v-if="currentSnapshot.models.length">
        <h3 class="mb-1.5 font-semibold text-white">{{ t('modelRateLimits.modelHeading') }}</h3>
        <div v-for="model in currentSnapshot.models" :key="model.model" class="mb-2 last:mb-0">
          <div class="mb-1 truncate font-medium text-gray-100">{{ model.model }}</div>
          <div class="space-y-1.5 pl-1">
            <div v-if="model.dimensions.concurrency" data-test="usage-progress">
              <UsageProgressBar
                :label="t('modelRateLimits.concurrency')"
                :utilization="model.dimensions.concurrency.utilization ?? 0"
                :color="'indigo'"
                :warning-at="70"
                :danger-above="90"
				:unavailable="!currentSnapshot.usage_available"
              />
              <span class="text-[10px] text-gray-300">{{ usageText(model.dimensions.concurrency) }}</span>
            </div>
            <div v-if="model.dimensions.rpm" data-test="usage-progress">
              <UsageProgressBar
                :label="'RPM'"
                :utilization="model.dimensions.rpm.utilization ?? 0"
                :color="'purple'"
                :warning-at="70"
                :danger-above="90"
				:unavailable="!currentSnapshot.usage_available"
              />
              <span class="text-[10px] text-gray-300">{{ usageText(model.dimensions.rpm) }}</span>
            </div>
            <div v-if="model.dimensions.tpm" data-test="usage-progress">
              <UsageProgressBar
                :label="'TPM'"
                :utilization="model.dimensions.tpm.utilization ?? 0"
                :color="'amber'"
                :warning-at="70"
                :danger-above="90"
				:unavailable="!currentSnapshot.usage_available"
              />
              <span class="text-[10px] text-gray-300">{{ usageText(model.dimensions.tpm) }}</span>
            </div>
          </div>
        </div>
      </div>

      <div v-if="currentSnapshot.saturated.length" class="border-t border-white/10 pt-2">
        <div
          v-for="item in currentSnapshot.saturated"
          :key="`${item.model}:${item.dimension}`"
          data-test="saturated-item"
          class="text-[10px] text-red-300"
        >
          {{ item.model || t('modelRateLimits.overall') }} · {{ dimensionLabel(item.dimension) }} · {{ t('modelRateLimits.reached') }}
        </div>
      </div>
    </div>
  </HelpTooltip>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
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

const unrestricted = computed(() => {
  const snapshot = currentSnapshot.value
  return Boolean(snapshot && snapshot.overall_concurrency.limit == null && !snapshot.overall_rpm && snapshot.models.length === 0)
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
const primaryText = computed(() => {
  const snapshot = currentSnapshot.value
  if (!snapshot?.usage_available) return t('modelRateLimits.unavailable')
  if (unrestricted.value) return t('modelRateLimits.unlimited')
  return primary.value ? usageText(primary.value.usage) : '—'
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
