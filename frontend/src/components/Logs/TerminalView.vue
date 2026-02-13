<template>
  <PageLayout :title="t('sidebar.terminal_logs')" :sticky="true">
    <template #actions>
      <Button variant="outline" size="sm" type="button" @click="handleCopy">
        <svg class="w-4 h-4 mr-2" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
        </svg>
        {{ t('components.terminalLogs.actions.copy') }}
      </Button>
      <Button variant="outline" size="sm" type="button" @click="handleOpenFolder">
        <svg class="w-4 h-4 mr-2" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 2h9a2 2 0 0 1 2 2z"></path>
        </svg>
        {{ t('components.terminalLogs.actions.openFolder') }}
      </Button>
      <Button variant="destructive" size="sm" type="button" @click="handleClear">
        <svg class="w-4 h-4 mr-2" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M9 3h6m-7 4h8m-6 0v11m4-11v11M5 7h14l-.867 12.138A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.862L5 7z" />
        </svg>
        {{ t('components.terminalLogs.actions.clear') }}
      </Button>
    </template>

    <p class="page-lead">{{ t('components.terminalLogs.subtitle') }}</p>

    <div class="terminal-container">
      <div class="terminal-header">
        <div class="terminal-buttons">
          <div class="terminal-button terminal-button-red"></div>
          <div class="terminal-button terminal-button-yellow"></div>
          <div class="terminal-button terminal-button-green"></div>
        </div>
        <div class="terminal-title">{{ t('components.terminalLogs.filename') }}</div>
        <div class="terminal-auto-scroll" @click="toggleAutoScroll">
          <span class="terminal-auto-scroll-text">{{ t('components.console.actions.autoScroll', 'Auto Scroll') }}</span>
          <div class="terminal-auto-scroll-indicator" :class="{ active: autoScroll }" />
        </div>
      </div>

      <div class="terminal-toolbar">
        <div class="terminal-toolbar-left">
          <BaseInput
            v-model="searchQuery"
            class="terminal-toolbar-search"
            :placeholder="t('components.console.searchPlaceholder')"
          />
          <div class="terminal-toolbar-levels">
            <Button
              size="sm"
              type="button"
              :variant="levelFilter.INFO ? 'secondary' : 'outline'"
              @click="toggleLevel('INFO')"
            >
              INFO
            </Button>
            <Button
              size="sm"
              type="button"
              :variant="levelFilter.WARN ? 'secondary' : 'outline'"
              @click="toggleLevel('WARN')"
            >
              WARN
            </Button>
            <Button
              size="sm"
              type="button"
              :variant="levelFilter.ERROR ? 'secondary' : 'outline'"
              @click="toggleLevel('ERROR')"
            >
              ERROR
            </Button>
          </div>
        </div>

        <div class="terminal-toolbar-right">
          <Button variant="outline" size="sm" type="button" @click="togglePaused" :disabled="!loggingEnabled">
            {{ paused ? t('components.console.actions.resume') : t('components.console.actions.pause') }}
          </Button>
          <Button variant="outline" size="sm" type="button" @click="refreshNow" :disabled="!loggingEnabled">
            {{ t('components.console.actions.refresh') }}
          </Button>

          <label class="terminal-toolbar-label">
            {{ t('components.console.actions.interval') }}
            <select
              v-model.number="refreshIntervalMs"
              class="base-input terminal-toolbar-select"
              :disabled="paused || !loggingEnabled"
            >
              <option :value="1000">1s</option>
              <option :value="2000">2s</option>
              <option :value="5000">5s</option>
            </select>
          </label>

          <label class="terminal-toolbar-label">
            {{ t('components.console.actions.maxLines') }}
            <select v-model.number="maxLines" class="base-input terminal-toolbar-select" :disabled="!loggingEnabled">
              <option :value="200">200</option>
              <option :value="500">500</option>
              <option :value="1000">1000</option>
            </select>
          </label>
        </div>
      </div>

      <ScrollArea ref="scrollAreaRef" height="calc(100vh - 240px)">
        <div class="terminal-content">
          <div v-if="!loggingEnabled" class="terminal-empty">
            <p>{{ t('components.console.loggingDisabled') }}</p>
            <Button variant="link" type="button" @click="goToSettings">
              {{ t('components.console.loggingDisabledAction') }}
            </Button>
          </div>

          <div v-else-if="logs.length === 0" class="terminal-empty">
            <p>{{ t('components.terminalLogs.empty') }}</p>
          </div>

          <div v-else-if="visibleLogs.length === 0" class="terminal-empty">
            <p>{{ t('components.console.noMatch') }}</p>
          </div>

          <div v-for="(log, index) in visibleLogs" :key="index" class="log-line">
            <span class="log-timestamp">{{ formatTime(log.timestamp) }}</span>
            <span class="log-level" :class="getLevelClass(log.level)">{{ log.level }}</span>
            <span class="log-message">{{ log.message }}</span>
          </div>
        </div>
      </ScrollArea>
    </div>
  </PageLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { Call } from '@wailsio/runtime'
import PageLayout from '../common/PageLayout.vue'
import BaseInput from '../common/BaseInput.vue'
import Button from '../ui/Button.vue'
import ScrollArea from '../ui/ScrollArea.vue'
import { showToast } from '../../utils/toast'
import { GetMITMLogs } from '../../../bindings/codeswitch/services/mitmservice'
import type { MITMLogEntry } from '../../../bindings/codeswitch/services/models'

const { t } = useI18n()
const router = useRouter()

type LogLevel = 'INFO' | 'WARN' | 'ERROR'
interface TerminalLogLine {
  timestamp: MITMLogEntry['timestamp']
  level: LogLevel
  message: string
}

const AUTO_SCROLL_STORAGE_KEY = 'logs-auto-scroll'
const AUTO_SCROLL_EVENT = 'logs-auto-scroll-change'
const APP_SETTINGS_UPDATED_EVENT = 'app-settings-updated'
const LOGGING_ENABLED_STORAGE_KEY = 'app-settings-enableLogging'
const REFRESH_INTERVAL_STORAGE_KEY = 'terminal-logs-refresh-interval-ms'
const MAX_LINES_STORAGE_KEY = 'terminal-logs-max-lines'
const LEVEL_FILTER_STORAGE_KEY = 'terminal-logs-level-filter'

const logs = ref<TerminalLogLine[]>([])
const autoScroll = ref(true)
const scrollAreaRef = ref<InstanceType<typeof ScrollArea> | null>(null)
let refreshInterval: number | null = null

const loggingEnabled = ref(localStorage.getItem(LOGGING_ENABLED_STORAGE_KEY) === 'true')
const paused = ref(false)
const searchQuery = ref('')
const refreshIntervalMs = ref(readNumberFromStorage(REFRESH_INTERVAL_STORAGE_KEY, 2000))
const maxLines = ref(readNumberFromStorage(MAX_LINES_STORAGE_KEY, 1000))
const levelFilter = ref<Record<LogLevel, boolean>>(readLevelFilterFromStorage())

const visibleLogs = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return logs.value.filter((log) => {
    if (!levelFilter.value[log.level]) return false
    if (!query) return true
    return log.message.toLowerCase().includes(query)
  })
})

const onAutoScrollChanged = (event: Event) => {
  autoScroll.value = Boolean((event as CustomEvent<boolean>).detail)
}

const loadLogs = async (force = false) => {
  if (!loggingEnabled.value) {
    logs.value = []
    return
  }
  if (!force && paused.value) return

  try {
    const newLogs = await GetMITMLogs()
    if (!newLogs.length) return

    const newLines = newLogs.map(toTerminalLine).filter(Boolean) as TerminalLogLine[]
    logs.value = [...logs.value, ...newLines].slice(-maxLines.value)

    if (autoScroll.value) {
      await nextTick()
      scrollAreaRef.value?.scrollToBottom()
    }
  } catch (error) {
    console.error('Failed to load logs:', error)
  }
}

const handleClear = async () => {
  if (!confirm(t('components.terminalLogs.clearConfirm'))) {
    return
  }

  try {
    logs.value = []
    await GetMITMLogs()
  } catch (error) {
    console.error('Failed to clear logs:', error)
  }
}

const handleCopy = async () => {
  const text = visibleLogs.value.map((l) => `[${formatTime(l.timestamp)}] [${l.level}] ${l.message}`).join('\n')
  try {
    await navigator.clipboard.writeText(text)
    showToast(t('components.logs.detail.copied', 'Copied'), 'success')
  } catch (error) {
    console.error('Failed to copy logs:', error)
    showToast(t('components.logs.detail.copyFailed', 'Copy failed'), 'error')
  }
}

const handleOpenFolder = async () => {
  try {
    await Call.ByName('codeswitch/services.ConsoleService.OpenLogFolder')
  } catch (error) {
    console.error('Failed to open log folder:', error)
    showToast(t('components.terminalLogs.openFolderFailed', 'Failed to open log folder'), 'error')
  }
}

const toggleAutoScroll = () => {
  const nextValue = !autoScroll.value
  autoScroll.value = nextValue
  localStorage.setItem(AUTO_SCROLL_STORAGE_KEY, String(nextValue))
  window.dispatchEvent(new CustomEvent(AUTO_SCROLL_EVENT, { detail: nextValue }))
}

const togglePaused = () => {
  paused.value = !paused.value
}

const refreshNow = () => loadLogs(true)

const toggleLevel = (level: LogLevel) => {
  levelFilter.value[level] = !levelFilter.value[level]
}

const goToSettings = () => {
  router.push('/settings')
}

const syncLoggingEnabledFromStorage = () => {
  const enabled = localStorage.getItem(LOGGING_ENABLED_STORAGE_KEY) === 'true'
  if (enabled === loggingEnabled.value) return

  loggingEnabled.value = enabled
  if (!enabled) {
    stopPolling()
    logs.value = []
    return
  }
  startPolling()
  refreshNow()
}

const startPolling = () => {
  if (refreshInterval !== null) return
  if (!loggingEnabled.value || paused.value || document.hidden) return
  refreshInterval = window.setInterval(() => loadLogs(), refreshIntervalMs.value)
}

const stopPolling = () => {
  if (refreshInterval === null) return
  clearInterval(refreshInterval)
  refreshInterval = null
}

const onVisibilityChange = () => {
  if (document.hidden) {
    stopPolling()
    return
  }
  startPolling()
  refreshNow()
}

const formatTime = (timestamp: any) => {
  if (!timestamp) return '--:--:--'
  try {
    const value = String(timestamp)
    if (value.includes('T')) {
      return value.split('T')[1].split('.')[0]
    }
    if (value.includes(' ')) {
      return value.split(' ')[1].split('.')[0]
    }
    return value
  } catch {
    return String(timestamp)
  }
}

const toTerminalLine = (entry: MITMLogEntry): TerminalLogLine | null => {
  const statusCode = entry.statusCode || 0
  const hasError = Boolean(entry.error && String(entry.error).trim())
  const level: LogLevel = hasError || statusCode >= 500 ? 'ERROR' : statusCode >= 400 ? 'WARN' : 'INFO'

  const domain = entry.domain || ''
  const path = entry.path || ''
  const target = entry.target ? ` -> ${entry.target}` : ''
  const status = statusCode ? ` ${statusCode}` : ''
  const latency = entry.latency ? ` ${entry.latency}ms` : ''
  const err = hasError ? ` | ${String(entry.error)}` : ''

  const message = `${entry.method || '-'} ${domain}${path}${target}${status}${latency}${err}`.trim()
  return {
    timestamp: entry.timestamp,
    level,
    message,
  }
}

const getLevelClass = (level: LogLevel) => {
  switch (level) {
    case 'ERROR':
      return 'level-error'
    case 'WARN':
      return 'level-warn'
    case 'INFO':
      return 'level-info'
    default:
      return 'level-default'
  }
}

onMounted(() => {
  const saved = localStorage.getItem(AUTO_SCROLL_STORAGE_KEY)
  if (saved === 'false') {
    autoScroll.value = false
  }
  window.addEventListener(AUTO_SCROLL_EVENT, onAutoScrollChanged)
  window.addEventListener(APP_SETTINGS_UPDATED_EVENT, syncLoggingEnabledFromStorage)
  document.addEventListener('visibilitychange', onVisibilityChange)

  refreshNow()
  startPolling()
})

onUnmounted(() => {
  stopPolling()
  window.removeEventListener(AUTO_SCROLL_EVENT, onAutoScrollChanged)
  window.removeEventListener(APP_SETTINGS_UPDATED_EVENT, syncLoggingEnabledFromStorage)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})

watch(refreshIntervalMs, (value) => {
  localStorage.setItem(REFRESH_INTERVAL_STORAGE_KEY, String(value))
  stopPolling()
  startPolling()
})

watch(maxLines, (value) => {
  localStorage.setItem(MAX_LINES_STORAGE_KEY, String(value))
  logs.value = logs.value.slice(-value)
})

watch(
  levelFilter,
  (value) => {
    localStorage.setItem(LEVEL_FILTER_STORAGE_KEY, JSON.stringify(value))
  },
  { deep: true },
)

watch(paused, (value) => {
  if (value) {
    stopPolling()
    return
  }
  startPolling()
  refreshNow()
})

function readNumberFromStorage(key: string, defaultValue: number) {
  const raw = localStorage.getItem(key)
  if (raw === null || raw.trim() === '') return defaultValue
  const value = Number(raw)
  return Number.isFinite(value) ? value : defaultValue
}

function readLevelFilterFromStorage(): Record<LogLevel, boolean> {
  const fallback: Record<LogLevel, boolean> = { INFO: true, WARN: true, ERROR: true }
  try {
    const raw = localStorage.getItem(LEVEL_FILTER_STORAGE_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as Partial<Record<LogLevel, boolean>>
    return {
      INFO: parsed.INFO !== false,
      WARN: parsed.WARN !== false,
      ERROR: parsed.ERROR !== false,
    }
  } catch {
    return fallback
  }
}
</script>

<style scoped>
.terminal-toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid #27272a;
  background: #0f0f12;
}

.terminal-toolbar-left {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: center;
  min-width: 240px;
}

.terminal-toolbar-right {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: center;
}

.terminal-toolbar-search {
  max-width: 420px;
  min-width: 220px;
}

.terminal-toolbar-levels {
  display: flex;
  gap: 0.5rem;
  align-items: center;
}

.terminal-toolbar-label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  color: #a1a1aa;
  font-size: 0.75rem;
}

.terminal-toolbar-select {
  width: auto;
  min-width: 92px;
  height: 32px;
  padding: 0 0.5rem;
  font-size: 0.8125rem;
  border-radius: 0.5rem;
  border: 1px solid #27272a;
  background: #09090b;
  color: #e4e4e7;
}

.terminal-container :deep(.base-input) {
  border-color: #27272a;
  background: #09090b;
  color: #e4e4e7;
}

.terminal-container :deep(.base-input::placeholder) {
  color: #71717a;
}

.terminal-container :deep(.btn-outline) {
  border-color: #27272a;
  background: transparent;
  color: #e4e4e7;
}

.terminal-container :deep(.btn-outline:hover),
.terminal-container :deep(.btn-outline:focus-visible) {
  background: rgba(255, 255, 255, 0.06);
  color: #e4e4e7;
}

.terminal-container :deep(.btn-secondary) {
  background: rgba(255, 255, 255, 0.08);
  color: #e4e4e7;
}

.terminal-container :deep(.btn-secondary:hover),
.terminal-container :deep(.btn-secondary:focus-visible) {
  background: rgba(255, 255, 255, 0.12);
}

.terminal-container {
  background: #09090b;
  border-radius: 0.75rem;
  border: 1px solid #27272a;
  overflow: hidden;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.3);
}

.terminal-header {
  height: 2.25rem;
  background: #18181b;
  border-bottom: 1px solid #27272a;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 1rem;
  flex-shrink: 0;
}

.terminal-buttons {
  display: flex;
  gap: 0.375rem;
}

.terminal-button {
  width: 0.75rem;
  height: 0.75rem;
  border-radius: 50%;
}

.terminal-button-red {
  background: rgba(239, 68, 68, 0.2);
  border: 1px solid rgba(239, 68, 68, 0.5);
}

.terminal-button-yellow {
  background: rgba(234, 179, 8, 0.2);
  border: 1px solid rgba(234, 179, 8, 0.5);
}

.terminal-button-green {
  background: rgba(34, 197, 94, 0.2);
  border: 1px solid rgba(34, 197, 94, 0.5);
}

.terminal-title {
  font-family: 'SF Mono', 'Monaco', 'Consolas', monospace;
  font-size: 0.625rem;
  color: #71717a;
}

.terminal-auto-scroll {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  cursor: pointer;
  transition: opacity 0.2s;
}

.terminal-auto-scroll:hover {
  opacity: 0.8;
}

.terminal-auto-scroll-text {
  font-size: 0.625rem;
  color: #71717a;
}

.terminal-auto-scroll-indicator {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: #3f3f46;
  transition: background 0.2s;
}

.terminal-auto-scroll-indicator.active {
  background: #22c55e;
}

.terminal-content {
  padding: 1rem;
  font-family: 'SF Mono', 'Monaco', 'Consolas', monospace;
  font-size: 0.75rem;
  line-height: 1.5;
  color: #a1a1aa;
}

.terminal-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 5rem 0;
  color: #a1a1aa;
  opacity: 0.9;
}

.log-line {
  display: flex;
  gap: 0.75rem;
  padding: 0.125rem 0.25rem;
  border-radius: 0.25rem;
  transition: background 0.15s;
  word-break: break-all;
}

.log-line:hover {
  background: rgba(255, 255, 255, 0.03);
}

.log-timestamp {
  color: #94a3b8;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
  user-select: none;
  min-width: 130px;
}

.log-level {
  font-weight: 600;
  flex-shrink: 0;
  min-width: 60px;
  text-transform: uppercase;
}

.level-error {
  color: #ef4444;
}

.level-warn {
  color: #eab308;
}

.level-info {
  color: #3b82f6;
}

.level-default {
  color: #71717a;
}

.log-message {
  color: #d4d4d8;
  flex: 1;
  white-space: pre-wrap;
}
</style>
