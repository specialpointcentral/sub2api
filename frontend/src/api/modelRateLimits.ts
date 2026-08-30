import { apiClient } from './client'

export interface ModelRateLimitRule {
  id?: number
  model_pattern: string
  limits: { concurrency: number; rpm: number; tpm?: number | null }
  windows?: { rpm_seconds: number; tpm_seconds?: number | null }
}

export interface ModelRateLimitRulesResponse {
  rules: ModelRateLimitRule[]
  updated_at: string | null
}

export interface ModelRateLimitUsage {
  used: number | null
  limit: number | null
  utilization: number | null
  saturated: boolean
  window_seconds?: number
  retry_after_seconds?: number
}

export interface ModelRateLimitSnapshotModel {
  model: string
  matched_pattern: string
  source: 'global' | 'user'
  dimensions: {
    concurrency?: ModelRateLimitUsage
    rpm?: ModelRateLimitUsage
    tpm?: ModelRateLimitUsage
  }
}

export interface ModelRateLimitSnapshot {
  generated_at: string
  refresh_after_ms: number
  overall_concurrency: ModelRateLimitUsage
  overall_rpm?: ModelRateLimitUsage
  models: ModelRateLimitSnapshotModel[]
  saturated: Array<{ model: string; dimension: 'concurrency' | 'rpm' | 'tpm' }>
  usage_available: boolean
}

export async function getModelRateLimitSnapshot(signal?: AbortSignal): Promise<ModelRateLimitSnapshot> {
  const { data } = await apiClient.get<ModelRateLimitSnapshot>('/user/model-rate-limits/snapshot', { signal })
  return data
}

export async function getGlobalModelRateLimitRules(): Promise<ModelRateLimitRulesResponse> {
  const { data } = await apiClient.get<ModelRateLimitRulesResponse>('/admin/model-rate-limits/rules')
  return data
}

export async function putGlobalModelRateLimitRules(rules: ModelRateLimitRule[]): Promise<ModelRateLimitRulesResponse> {
  const { data } = await apiClient.put<ModelRateLimitRulesResponse>('/admin/model-rate-limits/rules', { rules })
  return data
}

export async function getUserModelRateLimitRules(userId: number): Promise<ModelRateLimitRulesResponse> {
  const { data } = await apiClient.get<ModelRateLimitRulesResponse>(`/admin/users/${userId}/model-rate-limits`)
  return data
}

export async function putUserModelRateLimitRules(userId: number, rules: ModelRateLimitRule[]): Promise<ModelRateLimitRulesResponse> {
  const { data } = await apiClient.put<ModelRateLimitRulesResponse>(`/admin/users/${userId}/model-rate-limits`, { rules })
  return data
}

export async function getModelRateLimitCandidates(): Promise<string[]> {
  const { data } = await apiClient.get<{ models: string[] }>('/admin/model-rate-limits/model-candidates')
  return data.models
}

export const modelRateLimitsAPI = {
  getSnapshot: getModelRateLimitSnapshot,
  getGlobalRules: getGlobalModelRateLimitRules,
  putGlobalRules: putGlobalModelRateLimitRules,
  getUserRules: getUserModelRateLimitRules,
  putUserRules: putUserModelRateLimitRules,
  getCandidates: getModelRateLimitCandidates,
}
