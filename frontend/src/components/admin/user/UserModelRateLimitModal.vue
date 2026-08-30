<template>
  <BaseDialog
    :show="show"
    :title="t('admin.modelRateLimits.userTitle')"
    width="wide"
    @close="emit('close')"
  >
    <p v-if="user" class="mb-4 text-sm text-gray-500 dark:text-gray-400">
      {{ user.email }} · {{ t('admin.modelRateLimits.userDescription') }}
    </p>
    <div v-if="loading" class="py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
	<div v-else-if="rulesLoadFailed" class="py-10 text-center text-sm text-red-600">{{ t('admin.modelRateLimits.loadFailed') }}</div>
	<ModelRateLimitRuleEditor
      v-else
      v-model="rules"
      :candidates="candidates"
      :saving="saving"
      @save="save"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import type { AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ModelRateLimitRuleEditor from '@/components/admin/rateLimit/ModelRateLimitRuleEditor.vue'
import { modelRateLimitsAPI, type ModelRateLimitRule } from '@/api/modelRateLimits'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'success'): void }>()
const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(false)
const saving = ref(false)
const rules = ref<ModelRateLimitRule[]>([])
const candidates = ref<string[]>([])
const rulesLoadFailed = ref(false)
let loadSequence = 0

watch(() => [props.show, props.user?.id] as const, async ([show, userId]) => {
	const sequence = ++loadSequence
  if (!show || !userId) return
  loading.value = true
	rulesLoadFailed.value = false
  try {
	const response = await modelRateLimitsAPI.getUserRules(userId)
	if (sequence !== loadSequence || props.user?.id !== userId) return
    rules.value = response.rules
  } catch (error) {
	if (sequence !== loadSequence) return
	rulesLoadFailed.value = true
    appStore.showError(t('admin.modelRateLimits.loadFailed'))
  } finally {
	if (sequence === loadSequence) loading.value = false
  }
	try {
	  const models = await modelRateLimitsAPI.getCandidates()
	  if (sequence === loadSequence && props.user?.id === userId) candidates.value = models
	} catch {
	  if (sequence === loadSequence) candidates.value = []
	}
}, { immediate: true })

async function save(next: ModelRateLimitRule[]) {
  if (!props.user) return
  saving.value = true
  try {
    const response = await modelRateLimitsAPI.putUserRules(props.user.id, next)
    rules.value = response.rules
    appStore.showSuccess(t('admin.modelRateLimits.saved'))
    emit('success')
  } catch (error) {
    appStore.showError(t('admin.modelRateLimits.saveFailed'))
  } finally {
    saving.value = false
  }
}
</script>
