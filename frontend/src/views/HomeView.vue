<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- Compact Home Page -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <header class="border-b-2 border-gray-900 px-4 py-4 sm:px-6 dark:border-dark-700">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <div class="pixel-frame h-9 w-9 shrink-0 overflow-hidden p-0.5">
            <img
              :src="siteLogo || '/logo.svg'"
              alt="Logo"
              class="h-full w-full object-contain"
            />
          </div>
          <span class="min-w-0 truncate font-pixel text-base font-bold">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="pixel-icon-btn"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <button
            class="pixel-icon-btn"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="pixel-btn min-h-10 px-4"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="pixel-grid-bg flex min-w-0 flex-1 items-center justify-center px-4 py-16 sm:px-6">
      <div class="min-w-0 max-w-2xl text-center">
        <div class="pixel-frame mx-auto mb-6 h-20 w-20 overflow-hidden p-2">
          <img
            :src="siteLogo || '/logo.svg'"
            alt="Logo"
            class="h-full w-full object-contain"
          />
        </div>
        <h1 class="[overflow-wrap:anywhere] font-pixel text-3xl font-bold md:text-4xl">
          {{ siteName }}<span class="pixel-cursor"></span>
        </h1>
        <p class="mt-4 whitespace-pre-wrap [overflow-wrap:anywhere] font-pixel text-base text-gray-600 dark:text-dark-300">
          <span class="text-primary-500">&gt;</span> {{ siteSubtitle }}
        </p>
        <router-link
          :to="isAuthenticated ? dashboardPath : '/login'"
          class="pixel-btn mt-8 px-5 py-2.5"
        >
          {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
        </router-link>
      </div>
    </main>

    <footer class="min-w-0 border-t-2 border-gray-900 px-4 py-5 text-center font-pixel text-sm text-gray-500 [overflow-wrap:anywhere] sm:px-6 dark:border-dark-700 dark:text-dark-400">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>


  <!-- Default Home Page (Pixel Theme) -->
  <div
    v-else
    data-testid="pixel-home"
    class="relative flex min-h-screen flex-col overflow-hidden bg-gray-50 dark:bg-dark-950"
  >
    <!-- Pixel Grid Background -->
    <div class="pixel-grid-bg pointer-events-none absolute inset-0"></div>

    <!-- Header -->
    <header class="relative z-20 border-b-2 border-gray-900/10 px-4 py-4 sm:px-6 dark:border-dark-800">
      <nav class="mx-auto flex max-w-6xl items-center justify-between gap-3">
        <!-- Logo + Site Name -->
        <div class="flex min-w-0 items-center gap-3">
          <div class="pixel-frame h-10 w-10 shrink-0 overflow-hidden p-1">
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <span class="hidden min-w-0 truncate font-pixel text-base font-bold text-gray-900 sm:inline dark:text-white">
            {{ siteName }}
          </span>
        </div>

        <!-- Nav Actions -->
        <div class="flex shrink-0 items-center gap-2 sm:gap-3">
          <!-- Language Switcher -->
          <LocaleSwitcher />

          <!-- Doc Link -->
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="pixel-icon-btn"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>

          <!-- Theme Toggle -->
          <button
            @click="toggleTheme"
            class="pixel-icon-btn"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>

          <!-- Login / Dashboard Button -->
          <router-link
            v-if="isAuthenticated"
            :to="dashboardPath"
            class="pixel-btn px-3 py-1.5 text-xs"
          >
            <span
              class="flex h-5 w-5 items-center justify-center border border-gray-900 bg-primary-600 font-pixel text-[10px] font-bold text-white"
            >
              {{ userInitial }}
            </span>
            <span>{{ t('home.dashboard') }}</span>
          </router-link>
          <router-link v-else to="/login" class="pixel-btn px-3 py-1.5 text-xs">
            {{ t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <!-- Main Content -->
    <main class="relative z-10 flex-1 px-4 py-12 sm:px-6 md:py-16">
      <div class="mx-auto max-w-6xl">
        <!-- Hero Section - Left/Right Layout -->
        <div class="mb-14 flex flex-col items-center justify-between gap-10 lg:flex-row lg:gap-14">
          <!-- Left: Text Content -->
          <div class="min-w-0 flex-1 text-center lg:text-left">
            <p class="mb-3 font-pixel text-xs font-bold text-primary-600 dark:text-primary-400">
              $ whoami --pixel
            </p>
            <h1
              class="mb-4 break-words font-pixel text-3xl font-bold tracking-tight text-gray-900 dark:text-white sm:text-4xl lg:text-5xl"
            >
              {{ siteName }}<span class="pixel-cursor"></span>
            </h1>
            <p class="mb-8 break-words font-pixel text-base text-gray-600 dark:text-dark-300 md:text-lg">
              <span class="text-primary-500">&gt;</span> {{ siteSubtitle }}
            </p>

            <!-- CTA Button -->
            <div>
              <router-link
                :to="isAuthenticated ? dashboardPath : '/login'"
                class="pixel-btn px-8 py-3 text-base"
              >
                {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
                <Icon name="arrowRight" size="md" :stroke-width="2" />
              </router-link>
            </div>
          </div>

          <!-- Right: Voxel World (Minecraft/LEGO style) -->
          <div class="flex w-full max-w-[560px] flex-1 justify-center lg:justify-end">
            <div class="w-full">
              <div class="h-[320px] w-full sm:h-[400px]">
                <VoxelField />
              </div>
              <p class="mt-2 text-center font-pixel text-xs text-gray-400 dark:text-dark-500">
                {{ t('home.pixelHint') }}
              </p>
            </div>
          </div>
        </div>
      </div>
    </main>

    <!-- Footer -->
    <footer class="relative z-10 border-t-2 border-gray-900/10 px-4 py-8 sm:px-6 dark:border-dark-800">
      <div
        class="mx-auto flex max-w-6xl flex-col items-center justify-center gap-4 text-center sm:flex-row sm:justify-between sm:text-left"
      >
        <p class="font-pixel text-sm text-gray-500 dark:text-dark-400">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex items-center gap-4">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="font-pixel text-sm text-gray-500 transition-colors hover:text-primary-600 dark:text-dark-400 dark:hover:text-primary-400"
          >
            {{ t('home.docs') }}
          </a>
          <a
            :href="githubUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="font-pixel text-sm text-gray-500 transition-colors hover:text-primary-600 dark:text-dark-400 dark:hover:text-primary-400"
          >
            GitHub
          </a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import VoxelField from '@/components/home/VoxelField.vue'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI API Gateway Platform')
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// GitHub URL
const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// Toggle theme
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

// Initialize theme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>
