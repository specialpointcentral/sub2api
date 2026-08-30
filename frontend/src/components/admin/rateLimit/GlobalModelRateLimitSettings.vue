<template>
  <div class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('admin.modelRateLimits.globalTitle') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.modelRateLimits.globalDescription') }}
      </p>
    </div>
    <div class="p-6">
	  <p v-if="notice" class="mb-3 text-sm" :class="noticeError ? 'text-red-600' : 'text-green-600'">{{ notice }}</p>
	  <div v-if="loading" class="py-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
	  <div v-else-if="rulesLoadFailed" class="py-8 text-center text-sm text-red-600">{{ t('admin.modelRateLimits.loadFailed') }}</div>
      <ModelRateLimitRuleEditor
        v-else
        v-model="rules"
        :candidates="candidates"
        :saving="saving"
        @save="save"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ModelRateLimitRuleEditor from './ModelRateLimitRuleEditor.vue'
import { modelRateLimitsAPI, type ModelRateLimitRule } from '@/api/modelRateLimits'

const { t } = useI18n()
const loading = ref(true)
const saving = ref(false)
const rules = ref<ModelRateLimitRule[]>([])
const candidates = ref<string[]>([])
const notice = ref('')
const noticeError = ref(false)
const rulesLoadFailed = ref(false)

onMounted(async () => {
  try {
	const response = await modelRateLimitsAPI.getGlobalRules()
    rules.value = response.rules
  } catch (error) {
	rulesLoadFailed.value = true
	notice.value = t('admin.modelRateLimits.loadFailed')
	noticeError.value = true
  } finally {
    loading.value = false
  }
	try {
	  candidates.value = await modelRateLimitsAPI.getCandidates()
	} catch {
	  candidates.value = []
	}
})

async function save(next: ModelRateLimitRule[]) {
  saving.value = true
  try {
    const response = await modelRateLimitsAPI.putGlobalRules(next)
    rules.value = response.rules
	notice.value = t('admin.modelRateLimits.saved')
	noticeError.value = false
  } catch (error) {
	notice.value = t('admin.modelRateLimits.saveFailed')
	noticeError.value = true
  } finally {
    saving.value = false
  }
}
</script>
