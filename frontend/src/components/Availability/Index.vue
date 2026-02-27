<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PageLayout from '../common/PageLayout.vue'
import BaseButton from '../common/BaseButton.vue'
import AvailabilityConfigModal from './AvailabilityConfigModal.vue'
import {
  getLatestResults,
  runAllChecks,
  runSingleCheck,
  setAvailabilityMonitorEnabled,
  isPollingRunning,
  saveAvailabilityConfig,
  ProviderTimeline,
  HealthStatus,
  formatStatus,
  getStatusColor,
} from '../../services/healthcheck'

const { t } = useI18n()

// 状态
const loading = ref(true)
const refreshing = ref(false)
const timelines = ref<Record<string, ProviderTimeline[]>>({})
const pollingRunning = ref(false)
const lastUpdated = ref<Date | null>(null)
const nextRefreshIn = ref(0)

// 配置编辑弹窗状态
const showConfigModal = ref(false)
const savingConfig = ref(false)
const activeProvider = ref<(ProviderTimeline & { platform: string }) | null>(null)
const configForm = ref({
  testModel: '',
  testEndpoint: '',
  timeout: 15000,
})

// 刷新定时器
let refreshTimer: ReturnType<typeof setInterval> | null = null
let countdownTimer: ReturnType<typeof setInterval> | null = null

// 计算属性：状态统计
const statusStats = computed(() => {
  const stats = {
    operational: 0,
    degraded: 0,
    failed: 0,
    disabled: 0,
    total: 0,
  }

  for (const platform of Object.keys(timelines.value)) {
    for (const timeline of timelines.value[platform] || []) {
      stats.total++
      if (!timeline.availabilityMonitorEnabled) {
        stats.disabled++
      } else if (timeline.latest) {
        switch (timeline.latest.status) {
          case HealthStatus.OPERATIONAL:
            stats.operational++
            break
          case HealthStatus.DEGRADED:
            stats.degraded++
            break
          case HealthStatus.FAILED:
          case HealthStatus.VALIDATION_ERROR:
            stats.failed++
            break
        }
      } else {
        stats.disabled++
      }
    }
  }

  return stats
})

// 计算属性：所有平台列表（过滤掉空平台）
const platforms = computed(() =>
  Object.keys(timelines.value).filter((platform) => (timelines.value[platform]?.length || 0) > 0)
)

// 加载数据
async function loadData() {
  try {
    timelines.value = await getLatestResults()
    pollingRunning.value = await isPollingRunning()
    lastUpdated.value = new Date()
  } catch (error) {
    console.error('Failed to load availability data:', error)
  } finally {
    loading.value = false
  }
}

// 刷新全部
async function refreshAll() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    await runAllChecks()
    await loadData()
  } catch (error) {
    console.error('Failed to refresh:', error)
  } finally {
    refreshing.value = false
  }
}

// 检测单个 Provider
async function checkSingle(platform: string, providerId: number) {
  try {
    await runSingleCheck(platform, providerId)
    await loadData()
  } catch (error) {
    console.error('Failed to check provider:', error)
  }
}

// 切换监控开关
async function toggleMonitor(platform: string, providerId: number, enabled: boolean) {
  try {
    await setAvailabilityMonitorEnabled(platform, providerId, enabled)
    await loadData() // 刷新当前页面

    // 通知主页面刷新供应商列表
    window.dispatchEvent(new CustomEvent('providers-updated', {
      detail: { platform, providerId, enabled }
    }))
  } catch (error) {
    console.error('Failed to toggle monitor:', error)
  }
}

// 启用监控并打开配置编辑
async function enableMonitoringAndEdit(platform: string, timeline: ProviderTimeline) {
  try {
    await toggleMonitor(platform, timeline.providerId, true)
    // 等待状态更新后打开配置弹窗
    editConfig(platform, { ...timeline, availabilityMonitorEnabled: true })
  } catch (error) {
    console.error('Failed to enable monitoring and edit:', error)
  }
}

// 格式化时间
function formatTime(dateStr: string): string {
  if (!dateStr) return '-'
  const date = new Date(dateStr)
  return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

// 格式化上次更新时间
function formatLastUpdated(): string {
  if (!lastUpdated.value) return '-'
  return lastUpdated.value.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

// 启动刷新定时器
function startRefreshTimer() {
  // 每 60 秒刷新一次
  const refreshInterval = 60000
  nextRefreshIn.value = 60

  refreshTimer = setInterval(() => {
    loadData()
    nextRefreshIn.value = 60
  }, refreshInterval)

  countdownTimer = setInterval(() => {
    if (nextRefreshIn.value > 0) {
      nextRefreshIn.value--
    }
  }, 1000)
}

// 停止定时器
function stopTimers() {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
}

// 打开配置编辑弹窗
function editConfig(platform: string, timeline: ProviderTimeline) {
  activeProvider.value = { ...timeline, platform }
  const cfg = timeline.availabilityConfig || {}
  configForm.value = {
    testModel: cfg.testModel || '',
    testEndpoint: cfg.testEndpoint || '',
    timeout: cfg.timeout || 15000,
  }
  showConfigModal.value = true
}

// 关闭配置编辑弹窗
function closeConfigModal() {
  showConfigModal.value = false
  activeProvider.value = null
}

// 保存配置
async function saveConfig() {
  if (!activeProvider.value) return
  savingConfig.value = true
  try {
    await saveAvailabilityConfig(activeProvider.value.platform, activeProvider.value.providerId, {
      testModel: configForm.value.testModel,
      testEndpoint: configForm.value.testEndpoint,
      timeout: Number(configForm.value.timeout) || 15000,
    })
    showConfigModal.value = false
    await loadData()
  } catch (error) {
    console.error('Failed to save availability config:', error)
  } finally {
    savingConfig.value = false
  }
}

// 显示配置值（为空时标注默认）
function displayConfigValue(value: string | number | undefined, label: string) {
  if (value === undefined || value === null || value === '' || value === 0) {
    return `${label}（${t('availability.default')}）`
  }
  return String(value)
}

onMounted(async () => {
  await loadData()
  startRefreshTimer()

  // 监听主页面的 Provider 更新事件
  const handleProvidersUpdated = () => {
    void loadData()
  }
  window.addEventListener('providers-updated', handleProvidersUpdated)

  // 清理监听器
  onUnmounted(() => {
    window.removeEventListener('providers-updated', handleProvidersUpdated)
    stopTimers()
  })
})

onUnmounted(() => {
  stopTimers()
})
</script>

<template>
  <PageLayout :title="t('sidebar.availability')" :sticky="true">
    <template #actions>
      <button
        type="button"
        class="ghost-icon"
        :class="{ rotating: refreshing }"
        :data-tooltip="refreshing ? t('availability.refreshing') : t('availability.refreshAll')"
        :aria-label="refreshing ? t('availability.refreshing') : t('availability.refreshAll')"
        :disabled="refreshing"
        @click="refreshAll"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path
            d="M20.5 8a8.5 8.5 0 10-2.38 7.41"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
          <path
            d="M20.5 4v4h-4"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
      </button>
    </template>

    <p class="page-lead">{{ t('availability.subtitle') }}</p>

    <div class="availability-stats">
      <div class="stat-card stat-ok">
        <div class="stat-value">{{ statusStats.operational }}</div>
        <div class="stat-label">{{ t('availability.stats.operational') }}</div>
      </div>
      <div class="stat-card stat-warn">
        <div class="stat-value">{{ statusStats.degraded }}</div>
        <div class="stat-label">{{ t('availability.stats.degraded') }}</div>
      </div>
      <div class="stat-card stat-bad">
        <div class="stat-value">{{ statusStats.failed }}</div>
        <div class="stat-label">{{ t('availability.stats.failed') }}</div>
      </div>
      <div class="stat-card stat-off">
        <div class="stat-value">{{ statusStats.disabled }}</div>
        <div class="stat-label">{{ t('availability.stats.disabled') }}</div>
      </div>
    </div>

    <div class="availability-meta">
      <span>{{ t('availability.lastUpdate') }}: {{ formatLastUpdated() }}</span>
      <span>{{ t('availability.nextRefresh') }}: {{ nextRefreshIn }}s</span>
    </div>

    <div v-if="loading" class="availability-loading">
      <div class="availability-spinner" aria-hidden="true"></div>
    </div>

    <!-- Provider 列表 -->
    <div v-else class="availability-platforms">
      <section v-for="platform in platforms" :key="platform" class="platform-section">
        <template v-if="timelines[platform]?.length">
          <h2 class="platform-title">
            {{ platform }} {{ t('availability.providers') }}
          </h2>

          <div class="availability-table-wrapper">
            <table class="availability-table">
              <thead>
                <tr>
                  <th class="col-provider">{{ t('availability.table.provider') }}</th>
                  <th class="col-config">{{ t('availability.table.config') }}</th>
                  <th class="col-timeline">{{ t('availability.table.timeline') }}</th>
                  <th class="col-actions"></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="timeline in timelines[platform]" :key="timeline.providerId">
                  <td class="cell-provider">
                    <div class="provider-row">
                      <label class="mac-switch sm">
                        <input
                          type="checkbox"
                          :checked="timeline.availabilityMonitorEnabled"
                          @change="toggleMonitor(platform, timeline.providerId, !timeline.availabilityMonitorEnabled)"
                        />
                        <span></span>
                      </label>

                      <div class="provider-main">
                        <div class="provider-title">
                          <span class="provider-name">{{ timeline.providerName }}</span>
                          <span
                            v-if="timeline.availabilityMonitorEnabled && timeline.latest"
                            class="provider-status"
                            :class="getStatusColor(timeline.latest.status)"
                          >
                            {{ formatStatus(timeline.latest.status) }}
                          </span>
                          <span v-else class="provider-status provider-status--disabled">
                            {{ t('availability.notMonitored') }}
                          </span>
                        </div>
                        <div class="provider-meta">
                          <span v-if="timeline.latest?.latencyMs" class="provider-meta-item">
                            {{ timeline.latest.latencyMs }}ms
                          </span>
                          <span v-if="timeline.uptime > 0" class="provider-meta-item">
                            {{ timeline.uptime.toFixed(1) }}%
                          </span>
                        </div>
                      </div>
                    </div>
                  </td>

                  <td class="cell-config">
                    <div v-if="timeline.availabilityMonitorEnabled" class="config-lines">
                      <div class="config-line">
                        <span class="config-label">{{ t('availability.currentModel') }}</span>
                        <span class="config-value">{{ displayConfigValue(timeline.availabilityConfig?.testModel, t('availability.defaultModel')) }}</span>
                      </div>
                      <div class="config-line">
                        <span class="config-label">{{ t('availability.currentEndpoint') }}</span>
                        <span class="config-value">{{ displayConfigValue(timeline.availabilityConfig?.testEndpoint, t('availability.defaultEndpoint')) }}</span>
                      </div>
                      <div class="config-line">
                        <span class="config-label">{{ t('availability.currentTimeout') }}</span>
                        <span class="config-value">{{ displayConfigValue(timeline.availabilityConfig?.timeout, '15000ms') }}</span>
                      </div>
                    </div>
                    <span v-else class="placeholder-muted">-</span>
                  </td>

                  <td class="cell-timeline">
                    <div v-if="timeline.items?.length > 0" class="timeline-inline">
                      <div
                        v-for="(item, idx) in timeline.items.slice(0, 20)"
                        :key="idx"
                        :title="`${formatTime(item.checkedAt)} - ${formatStatus(item.status)} (${item.latencyMs}ms)`"
                        class="timeline-dot"
                        :class="{
                          'dot-ok': item.status === HealthStatus.OPERATIONAL,
                          'dot-warn': item.status === HealthStatus.DEGRADED,
                          'dot-bad': item.status === HealthStatus.FAILED || item.status === HealthStatus.VALIDATION_ERROR,
                        }"
                      ></div>
                    </div>
                    <span v-else class="placeholder-muted">-</span>
                  </td>

                  <td class="cell-actions">
                    <BaseButton
                      v-if="!timeline.availabilityMonitorEnabled"
                      size="sm"
                      type="button"
                      @click="enableMonitoringAndEdit(platform, timeline)"
                    >
                      {{ t('availability.enableMonitoring') }}
                    </BaseButton>
                    <template v-else>
                      <button
                        class="ghost-icon sm"
                        type="button"
                        :title="t('availability.check')"
                        @click="checkSingle(platform, timeline.providerId)"
                      >
                        <svg viewBox="0 0 24 24" aria-hidden="true">
                          <path
                            d="M20.5 8a8.5 8.5 0 10-2.38 7.41"
                            fill="none"
                            stroke="currentColor"
                            stroke-width="1.5"
                            stroke-linecap="round"
                            stroke-linejoin="round"
                          />
                          <path
                            d="M20.5 4v4h-4"
                            fill="none"
                            stroke="currentColor"
                            stroke-width="1.5"
                            stroke-linecap="round"
                            stroke-linejoin="round"
                          />
                        </svg>
                      </button>
                      <BaseButton variant="outline" size="sm" type="button" @click="editConfig(platform, timeline)">
                        {{ t('availability.editConfig') }}
                      </BaseButton>
                    </template>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </section>

      <div v-if="platforms.length === 0" class="availability-empty">
        {{ t('availability.noProviders') }}
      </div>
    </div>

    <!-- 配置编辑弹窗 -->
    <AvailabilityConfigModal
      :open="showConfigModal"
      :title="t('availability.configTitle')"
      :provider-name="activeProvider?.providerName"
      :platform="activeProvider?.platform"
      :saving="savingConfig"
      v-model="configForm"
      @close="closeConfigModal"
      @save="saveConfig"
    />
  </PageLayout>
</template>

<style scoped src="./availability.css"></style>
