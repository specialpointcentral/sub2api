<template>
  <div class="relative flex min-h-screen items-center justify-center overflow-hidden bg-gray-50 p-4 dark:bg-dark-950">
    <!-- Pixel Grid Background -->
    <div class="pixel-grid-bg pointer-events-none absolute inset-0"></div>

    <!-- Decorative floating pixels -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
      <div class="absolute left-[12%] top-[18%] h-3 w-3 bg-primary-400/60"></div>
      <div class="absolute right-[15%] top-[26%] h-2 w-2 bg-primary-500/50"></div>
      <div class="absolute bottom-[22%] left-[20%] h-2 w-2 bg-primary-300/60"></div>
      <div class="absolute bottom-[30%] right-[10%] h-3 w-3 bg-primary-400/40"></div>
      <div class="absolute left-[45%] top-[10%] h-2 w-2 bg-primary-500/40"></div>
      <div class="absolute bottom-[12%] right-[38%] h-2 w-2 bg-primary-300/50"></div>
    </div>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo/Brand -->
      <div class="mb-8 text-center">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div class="pixel-frame mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden p-1.5">
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <h1 class="mb-2 font-pixel text-2xl font-bold text-gray-900 dark:text-white">
            {{ siteName }}<span class="pixel-cursor"></span>
          </h1>
          <p class="font-pixel text-sm text-gray-500 dark:text-dark-400">
            <span class="text-primary-500">&gt;</span> {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container -->
      <div class="pixel-card p-6 sm:p-8">
        <slot />
      </div>

      <!-- Footer Links -->
      <div class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- Copyright -->
      <div class="mt-8 text-center font-pixel text-xs text-gray-400 dark:text-dark-500">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>
