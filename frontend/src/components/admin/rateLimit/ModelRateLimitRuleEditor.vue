<template>
  <div>
    <div
      v-if="rows.length === 0"
      class="rounded-lg border border-gray-200 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
    >
      {{ t('admin.modelRateLimits.empty') }}
    </div>
    <div v-else class="space-y-2">
      <div
        v-for="(row, index) in rows"
        :key="row.key"
        data-test="rule-row"
        class="flex items-center gap-2"
      >
        <div class="min-w-0 flex-1">
          <input
            v-model="row.model_pattern"
            data-test="model-pattern"
            type="text"
            :list="candidateListID"
            class="input"
            :class="rowErrors[index]?.pattern && 'border-red-500'"
            :placeholder="t('admin.modelRateLimits.patternPlaceholder')"
            @input="emitRows"
          />
          <p v-if="rowErrors[index]?.pattern" class="mt-1 text-xs text-red-600">
            {{ rowErrors[index].pattern }}
          </p>
          <p v-else-if="isUnlimited(row)" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
            {{ t('admin.modelRateLimits.explicitUnlimited') }}
          </p>
        </div>
        <div class="w-32 shrink-0">
          <input
            v-model="row.concurrency"
            data-test="concurrency-limit"
            type="number"
            min="0"
            step="1"
            class="input"
            :class="rowErrors[index]?.concurrency && 'border-red-500'"
            :placeholder="t('admin.modelRateLimits.concurrency')"
            @input="emitRows"
          />
          <p v-if="rowErrors[index]?.concurrency" class="mt-1 text-xs text-red-600">
            {{ rowErrors[index].concurrency }}
          </p>
        </div>
        <div class="w-32 shrink-0">
          <input
            v-model="row.rpm"
            data-test="rpm-limit"
            type="number"
            min="0"
            step="1"
            class="input"
            :class="rowErrors[index]?.rpm && 'border-red-500'"
            :placeholder="t('admin.modelRateLimits.rpm')"
            @input="emitRows"
          />
          <p v-if="rowErrors[index]?.rpm" class="mt-1 text-xs text-red-600">
            {{ rowErrors[index].rpm }}
          </p>
        </div>
        <button
          type="button"
          data-test="delete-rule"
          class="shrink-0 rounded-lg p-2 text-red-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
          :aria-label="t('common.delete')"
          @click="removeRow(index)"
        >
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
            />
          </svg>
        </button>
      </div>
    </div>

    <datalist :id="candidateListID">
      <option v-for="candidate in candidates" :key="candidate" :value="candidate" />
    </datalist>

    <div class="mt-4 space-y-3">
      <button
        type="button"
        data-test="add-rule"
        class="w-full rounded-lg border-2 border-dashed border-gray-300 px-4 py-2 text-gray-600 transition-colors hover:border-gray-400 hover:text-gray-700 dark:border-dark-500 dark:text-gray-400 dark:hover:border-dark-400 dark:hover:text-gray-300"
        @click="addRow"
      >
        <svg class="mr-1 inline h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
        </svg>
        {{ t('admin.modelRateLimits.addRule') }}
      </button>
      <div v-if="showSave" class="flex justify-end">
        <button
          type="button"
          data-test="save-rules"
          class="btn btn-primary"
          :disabled="invalid || saving"
          @click="save"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ModelRateLimitRule } from '@/api/modelRateLimits'

const props = withDefaults(defineProps<{
  modelValue: ModelRateLimitRule[]
  candidates: string[]
  saving?: boolean
  showSave?: boolean
}>(), { saving: false, showSave: true })

const emit = defineEmits<{
  (event: 'update:modelValue', rules: ModelRateLimitRule[]): void
  (event: 'save', rules: ModelRateLimitRule[]): void
}>()

const { t } = useI18n()
const candidateListID = `model-rate-limit-candidates-${Math.random().toString(36).slice(2)}`
let rowKey = 0
type EditableRow = { key: number; model_pattern: string; concurrency: string | number; rpm: string | number }
const rows = ref<EditableRow[]>([])

function toRows(rules: ModelRateLimitRule[]): EditableRow[] {
  return rules.map((rule) => ({
    key: ++rowKey,
    model_pattern: rule.model_pattern,
    concurrency: String(rule.limits.concurrency ?? 0),
    rpm: String(rule.limits.rpm ?? 0),
  }))
}

watch(() => props.modelValue, (rules) => {
  const current = rows.value.map((row) => ({
    model_pattern: row.model_pattern.trim(),
    limits: { concurrency: normalizeLimit(row.concurrency), rpm: normalizeLimit(row.rpm) },
  }))
  const incoming = rules.map((rule) => ({
    model_pattern: rule.model_pattern.trim(),
    limits: { concurrency: rule.limits.concurrency ?? 0, rpm: rule.limits.rpm ?? 0 },
  }))
  if (JSON.stringify(current) !== JSON.stringify(incoming)) rows.value = toRows(rules)
}, { immediate: true, deep: true })

function validInteger(value: string | number) {
	const text = String(value).trim()
	return text === '' || /^\d+$/.test(text)
}

const rowErrors = computed(() => {
  const occurrences = new Map<string, number>()
  for (const row of rows.value) {
    const key = row.model_pattern.trim().toLowerCase()
    if (key) occurrences.set(key, (occurrences.get(key) ?? 0) + 1)
  }
  return rows.value.map((row) => {
    const pattern = row.model_pattern.trim()
	let patternError = ''
	if (!pattern) {
	  patternError = t('admin.modelRateLimits.errors.required')
	} else if (/[?\[\]\\]/.test(pattern)) {
	  patternError = t('admin.modelRateLimits.errors.glob')
	} else if ((occurrences.get(pattern.toLowerCase()) ?? 0) > 1) {
	  patternError = t('admin.modelRateLimits.errors.duplicate')
	}
    return {
	  pattern: patternError,
      concurrency: validInteger(row.concurrency) ? '' : t('admin.modelRateLimits.errors.nonNegativeInteger'),
      rpm: validInteger(row.rpm) ? '' : t('admin.modelRateLimits.errors.nonNegativeInteger'),
    }
  })
})

const invalid = computed(() => rowErrors.value.some((errors) => Object.values(errors).some(Boolean)))

function normalizeLimit(value: string | number) {
	const text = String(value).trim()
	return text === '' ? 0 : Number(text)
}

function normalizedRules(): ModelRateLimitRule[] {
  return rows.value.map((row) => ({
    model_pattern: row.model_pattern.trim(),
    limits: { concurrency: normalizeLimit(row.concurrency), rpm: normalizeLimit(row.rpm) },
  }))
}

function emitRows() {
  if (!invalid.value) emit('update:modelValue', normalizedRules())
}

function addRow() {
  rows.value.push({ key: ++rowKey, model_pattern: '', concurrency: '', rpm: '' })
  emitRows()
}

function removeRow(index: number) {
  rows.value.splice(index, 1)
  emitRows()
}

function isUnlimited(row: EditableRow) {
  return normalizeLimit(row.concurrency) === 0 && normalizeLimit(row.rpm) === 0
}

function save() {
  if (!invalid.value) emit('save', normalizedRules())
}
</script>
