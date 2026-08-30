<template>
  <div>
    <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
      <table class="min-w-[680px] w-full text-sm">
        <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400">
          <tr>
            <th class="px-3 py-2">{{ t('admin.modelRateLimits.model') }}</th>
            <th class="w-36 px-3 py-2">{{ t('admin.modelRateLimits.concurrency') }}</th>
            <th class="w-36 px-3 py-2">{{ t('admin.modelRateLimits.rpm') }}</th>
            <th class="w-16 px-3 py-2"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="rows.length === 0">
            <td colspan="4" class="px-4 py-8 text-center text-gray-500 dark:text-gray-400">
              {{ t('admin.modelRateLimits.empty') }}
            </td>
          </tr>
          <tr
            v-for="(row, index) in rows"
            :key="row.key"
            data-test="rule-row"
            class="border-t border-gray-100 align-top dark:border-dark-700"
          >
            <td class="min-w-[280px] px-3 py-3">
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
            </td>
            <td class="px-3 py-3">
              <input
                v-model="row.concurrency"
                data-test="concurrency-limit"
                type="number"
                min="0"
                step="1"
                class="input"
                :class="rowErrors[index]?.concurrency && 'border-red-500'"
                @input="emitRows"
              />
              <p v-if="rowErrors[index]?.concurrency" class="mt-1 text-xs text-red-600">
                {{ rowErrors[index].concurrency }}
              </p>
            </td>
            <td class="px-3 py-3">
              <input
                v-model="row.rpm"
                data-test="rpm-limit"
                type="number"
                min="0"
                step="1"
                class="input"
                :class="rowErrors[index]?.rpm && 'border-red-500'"
                @input="emitRows"
              />
              <p v-if="rowErrors[index]?.rpm" class="mt-1 text-xs text-red-600">
                {{ rowErrors[index].rpm }}
              </p>
            </td>
            <td class="px-3 py-3 text-right">
              <button
                type="button"
                data-test="delete-rule"
                class="rounded p-2 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
                :aria-label="t('common.delete')"
                @click="removeRow(index)"
              >
                ×
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <datalist :id="candidateListID">
      <option v-for="candidate in candidates" :key="candidate" :value="candidate" />
    </datalist>

    <div class="mt-4 flex items-center justify-between gap-3">
      <button type="button" data-test="add-rule" class="btn btn-secondary" @click="addRow">
        {{ t('admin.modelRateLimits.addRule') }}
      </button>
      <button
        v-if="showSave"
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
