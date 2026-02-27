<template>
  <PageLayout :title="t('sidebar.mcp')" :sticky="true">
    <template #actions>
      <button class="ghost-icon" :class="{ rotating: loading }" type="button"
        :data-tooltip="t('components.mcp.controls.refresh')" :aria-label="t('components.mcp.controls.refresh')"
        :disabled="loading" @click="reload">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20.5 8a8.5 8.5 0 10-2.38 7.41" fill="none" stroke="currentColor" stroke-width="1.5"
            stroke-linecap="round" stroke-linejoin="round" />
          <path d="M20.5 4v4h-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"
            stroke-linejoin="round" />
        </svg>
      </button>
      <button class="ghost-icon" type="button" :data-tooltip="t('components.mcp.controls.create')"
        :aria-label="t('components.mcp.controls.create')" @click="openCreateModal">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"
            stroke-linejoin="round" fill="none" />
        </svg>
      </button>
      <button class="ghost-icon" type="button" :data-tooltip="t('components.mcp.import.title')"
        :aria-label="t('components.mcp.import.title')" @click="openBatchImport">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1M12 4v12m0 0l-4-4m4 4l4-4" stroke="currentColor"
            stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" fill="none" />
        </svg>
      </button>
    </template>

    <p class="page-lead">{{ t('components.mcp.hero.lead') }}</p>

    <section class="automation-section">

      <div v-if="errorMessage" class="alert-error">{{ errorMessage }}</div>

      <div v-if="loading" class="empty-state">{{ t('components.mcp.list.loading') }}</div>

      <div v-else-if="!servers.length" class="empty-state">
        <p>{{ t('components.mcp.list.empty') }}</p>
        <BaseButton type="button" @click="openCreateModal">
          {{ t('components.mcp.controls.create') }}
        </BaseButton>
      </div>

      <div v-else class="mcp-table-wrapper">
        <table class="mcp-table">
          <thead>
            <tr>
              <th class="col-name">{{ t('components.mcp.form.name') }}</th>
              <th class="col-type">{{ t('components.mcp.form.type') }}</th>
              <th class="col-endpoint">{{ t('components.mcp.list.endpoint') }}</th>
              <th class="col-platform">{{ t('components.mcp.platforms.claude') }}</th>
              <th class="col-platform">{{ t('components.mcp.platforms.codex') }}</th>
              <th class="col-platform">{{ t('components.mcp.platforms.gemini') }}</th>
              <th class="col-actions"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="server in servers" :key="server.name">
              <td class="cell-name">
                <div class="name-row">
                  <div class="card-icon" :style="iconStyle(server.name)">
                    <span v-if="iconSvg(server.name)" class="icon-svg" v-html="iconSvg(server.name)"
                      aria-hidden="true"></span>
                    <span v-else class="icon-fallback">{{ serverInitials(server.name) }}</span>
                  </div>
                  <div class="name-text">
                    <div class="name-title">
                      <span class="server-name">{{ server.name }}</span>
                      <span
                        v-if="hasMissingPlaceholders(server)"
                        class="placeholder-badge"
                        :data-tooltip="t('components.mcp.list.placeholderWarning', { vars: (server.missing_placeholders ?? []).join(', ') })"
                      >
                        !
                      </span>
                    </div>
                    <a v-if="server.website" class="name-sub" :href="server.website" target="_blank" rel="noreferrer">
                      {{ server.website }}
                    </a>
                    <div v-else-if="server.tips" class="name-sub">
                      {{ server.tips }}
                    </div>
                  </div>
                </div>
              </td>
              <td>
                <span class="chip">{{ typeLabel(server.type) }}</span>
              </td>
              <td class="cell-endpoint">
                <div class="endpoint-main">
                  {{ server.type === 'http' ? server.url : server.command }}
                </div>
                <div class="endpoint-meta">
                  <span v-if="server.type === 'stdio' && server.cwd" class="meta-item">cwd: {{ server.cwd }}</span>
                  <span v-if="server.type === 'http' && Object.keys(server.headers ?? {}).length" class="meta-item">
                    headers: {{ Object.keys(server.headers ?? {}).length }}
                  </span>
                  <span v-if="server.startup_timeout_sec" class="meta-item">
                    startup_timeout_sec: {{ server.startup_timeout_sec }}s
                  </span>
                </div>
              </td>
              <td class="cell-platform">
                <div class="platform-cell" :title="platformActive(server, 'claude-code') ? t('components.mcp.status.active') : t('components.mcp.status.inactive')">
                  <span class="platform-dot" :class="{ active: platformActive(server, 'claude-code') }"></span>
                  <label class="mac-switch sm">
                    <input
                      type="checkbox"
                      :checked="platformEnabled(server, 'claude-code')"
                      :disabled="saveBusy"
                      @change="onPlatformToggle(server, 'claude-code', $event)"
                    />
                    <span></span>
                  </label>
                </div>
              </td>
              <td class="cell-platform">
                <div class="platform-cell" :title="platformActive(server, 'codex') ? t('components.mcp.status.active') : t('components.mcp.status.inactive')">
                  <span class="platform-dot" :class="{ active: platformActive(server, 'codex') }"></span>
                  <label class="mac-switch sm">
                    <input
                      type="checkbox"
                      :checked="platformEnabled(server, 'codex')"
                      :disabled="saveBusy"
                      @change="onPlatformToggle(server, 'codex', $event)"
                    />
                    <span></span>
                  </label>
                </div>
              </td>
              <td class="cell-platform">
                <div class="platform-cell" :title="platformActive(server, 'gemini') ? t('components.mcp.status.active') : t('components.mcp.status.inactive')">
                  <span class="platform-dot" :class="{ active: platformActive(server, 'gemini') }"></span>
                  <label class="mac-switch sm">
                    <input
                      type="checkbox"
                      :checked="platformEnabled(server, 'gemini')"
                      :disabled="saveBusy"
                      @change="onPlatformToggle(server, 'gemini', $event)"
                    />
                    <span></span>
                  </label>
                </div>
              </td>
              <td class="cell-actions">
                <button class="ghost-icon" :aria-label="t('components.mcp.list.edit')" @click="openEditModal(server)">
                  <svg viewBox="0 0 24 24" aria-hidden="true">
                    <path
                      d="M16.474 5.408l2.118 2.117m-.756-3.982L12.109 9.27a2.118 2.118 0 00-.58 1.082L11 13l2.648-.53c.41-.082.786-.283 1.082-.579l5.727-5.727a1.853 1.853 0 10-2.621-2.621z"
                      fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"
                      stroke-linejoin="round" />
                    <path d="M19 15v3a2 2 0 01-2 2H6a2 2 0 01-2-2V7a2 2 0 012-2h3" fill="none" stroke="currentColor"
                      stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                </button>
                <button class="ghost-icon" :aria-label="t('components.mcp.list.delete')" @click="requestDelete(server)">
                  <svg viewBox="0 0 24 24" aria-hidden="true">
                    <path
                      d="M9 3h6m-7 4h8m-6 0v11m4-11v11M5 7h14l-.867 12.138A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.862L5 7z"
                      fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"
                      stroke-linejoin="round" />
                  </svg>
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <FullScreenPanel class="mcp-fullscreen-panel" :open="modalState.open"
      :title="modalState.editingName ? t('components.mcp.form.editTitle') : t('components.mcp.form.createTitle')"
      @close="closeModal">
      <form class="vendor-form" @submit.prevent="submitModal">
        <div class="form-row">
          <label class="form-field">
            <span>{{ t('components.mcp.form.name') }}</span>
            <BaseInput v-model="modalState.form.name" type="text" :disabled="saveBusy" />
          </label>
          <label class="form-field">
            <span>{{ t('components.mcp.form.website') }}</span>
            <BaseInput v-model="modalState.form.website" type="text" :disabled="saveBusy"
              placeholder="https://example.com" />
          </label>
        </div>
        <label class="form-field">
          <span>{{ t('components.mcp.form.type') }}</span>
          <select v-model="modalState.form.type" :disabled="saveBusy" class="base-input">
            <option value="stdio">{{ t('components.mcp.types.stdio') }}</option>
            <option value="http">{{ t('components.mcp.types.http') }}</option>
          </select>
        </label>
        <label v-if="modalState.form.type === 'stdio'" class="form-field">
          <span>{{ t('components.mcp.form.command') }}</span>
          <BaseInput v-model="modalState.form.command" type="text" :disabled="saveBusy" />
        </label>
        <label v-if="modalState.form.type === 'stdio'" class="form-field">
          <span>{{ t('components.mcp.form.args') }}</span>
          <BaseTextarea v-model="modalState.form.argsText" :placeholder="t('components.mcp.form.argsHint')"
            :disabled="saveBusy" rows="5" />
        </label>
        <label v-if="modalState.form.type === 'stdio'" class="form-field">
          <span>{{ t('components.mcp.form.cwd') }}</span>
          <BaseInput v-model="modalState.form.cwd" type="text" :disabled="saveBusy" :placeholder="t('components.mcp.form.cwdHint')" />
        </label>
        <label v-if="modalState.form.type === 'http'" class="form-field">
          <span>{{ t('components.mcp.form.url') }}</span>
          <BaseInput v-model="modalState.form.url" type="text" :disabled="saveBusy" />
        </label>
        <div v-if="modalState.form.type === 'http'" class="form-field">
          <span>{{ t('components.mcp.form.headers') }}</span>
          <div class="env-table">
            <div v-for="entry in modalState.form.headersEntries" :key="entry.id" class="env-row">
              <BaseInput v-model="entry.key" :placeholder="t('components.mcp.form.headerKey')" :disabled="saveBusy" />
              <BaseInput v-model="entry.value" :placeholder="t('components.mcp.form.headerValue')" :disabled="saveBusy" />
              <button class="ghost-icon" type="button" :aria-label="t('components.mcp.form.headerRemove')"
                :disabled="modalState.form.headersEntries.length === 1 || saveBusy" @click="removeHeaderEntry(entry.id)">
                ✕
              </button>
            </div>
          </div>
          <BaseButton variant="outline" type="button" class="env-add" :disabled="saveBusy" @click="addHeaderEntry()">
            {{ t('components.mcp.form.headerAdd') }}
          </BaseButton>
          <p class="field-hint">{{ t('components.mcp.form.headersHint') }}</p>
        </div>
        <label class="form-field">
          <span>{{ t('components.mcp.form.startupTimeout') }}</span>
          <input v-model.number="modalState.form.startupTimeoutSec" type="number" min="0" class="base-input"
            :disabled="saveBusy" />
          <p class="field-hint">{{ t('components.mcp.form.startupTimeoutHint') }}</p>
        </label>
        <label class="form-field">
          <span>{{ t('components.mcp.form.tips') }}</span>
          <BaseTextarea v-model="modalState.form.tips" :placeholder="t('components.mcp.form.tipsHint')"
            :disabled="saveBusy" rows="4" />
        </label>
        <div v-if="modalState.form.type === 'stdio'" class="form-field">
          <span>{{ t('components.mcp.form.env') }}</span>
          <div class="env-table">
            <div v-for="entry in modalState.form.envEntries" :key="entry.id" class="env-row">
              <BaseInput v-model="entry.key" :placeholder="t('components.mcp.form.envKey')" :disabled="saveBusy" />
              <BaseInput v-model="entry.value" :placeholder="t('components.mcp.form.envValue')" :disabled="saveBusy" />
              <button class="ghost-icon" type="button" :aria-label="t('components.mcp.form.envRemove')"
                :disabled="modalState.form.envEntries.length === 1 || saveBusy" @click="removeEnvEntry(entry.id)">
                ✕
              </button>
            </div>
          </div>
          <BaseButton variant="outline" type="button" class="env-add" :disabled="saveBusy" @click="addEnvEntry()">
            {{ t('components.mcp.form.envAdd') }}
          </BaseButton>
        </div>
        <div class="form-field">
          <span>{{ t('components.mcp.form.platforms.title') }}</span>
          <div class="platform-checkboxes">
            <label v-for="option in platformOptions" :key="option.id" class="platform-checkbox">
              <input type="checkbox" :checked="modalState.form.enablePlatform.includes(option.id)" :disabled="saveBusy"
                @change="onModalPlatformToggle(option.id, $event)" />
              <span>{{ option.label }}</span>
            </label>
          </div>
        </div>

        <!-- 表单模式：JSON 配置编辑器 -->
        <div class="form-field mcp-json-field">
          <div class="mcp-json-header" @click="toggleFormJsonExpanded">
            <svg class="mcp-json-expand-icon" :class="{ expanded: formJsonExpanded }" viewBox="0 0 20 20"
              aria-hidden="true">
              <path d="M6 8l4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"
                stroke-linejoin="round" fill="none" />
            </svg>
            <span class="mcp-json-title">{{ t('components.mcp.form.jsonEditor.title') }}</span>
            <span v-if="formJsonDirty" class="mcp-json-dirty">{{ t('components.mcp.form.jsonEditor.dirty') }}</span>

            <div class="mcp-json-actions" @click.stop>
              <button type="button" class="mcp-json-action-btn" :disabled="saveBusy" @click="toggleJsonLock">
                <span v-if="formJsonLocked">{{ t('components.mcp.form.jsonEditor.unlock') }}</span>
                <span v-else>{{ t('components.mcp.form.jsonEditor.lock') }}</span>
              </button>

              <button v-if="!formJsonLocked" type="button" class="mcp-json-action-btn primary"
                :disabled="saveBusy || !formJsonDirty" @click="applyJsonToForm">
                {{ t('components.mcp.form.jsonEditor.apply') }}
              </button>
              <button v-if="!formJsonLocked" type="button" class="mcp-json-action-btn"
                :disabled="saveBusy || !formJsonDirty" @click="resetJsonFromForm">
                {{ t('components.mcp.form.jsonEditor.reset') }}
              </button>
            </div>
          </div>

          <div v-if="formJsonExpanded" class="mcp-json-body">
            <BaseTextarea v-if="!formJsonLocked" ref="formJsonTextareaRef" v-model="formJsonEditingText" rows="10"
              class="mcp-json-textarea" :disabled="saveBusy" />
            <pre v-else class="mcp-json-preview">{{ formJsonSyncedText }}</pre>

            <p v-if="formJsonError" class="alert-error">{{ formJsonError }}</p>
            <p class="mcp-json-hint">{{ t('components.mcp.form.jsonEditor.hint') }}</p>
          </div>
        </div>

        <p v-if="modalError" class="alert-error">{{ modalError }}</p>

        <div class="form-actions">
          <BaseButton variant="outline" type="button" :disabled="saveBusy" @click="closeModal">
            {{ t('components.mcp.form.actions.cancel') }}
          </BaseButton>
          <BaseButton :disabled="saveBusy" type="submit">
            {{ t('components.mcp.form.actions.save') }}
          </BaseButton>
        </div>
      </form>
    </FullScreenPanel>

    <InlineModal :open="confirmState.open" :title="t('components.mcp.form.deleteTitle')" variant="confirm"
      :close-on-backdrop="false" @close="closeConfirm">
      <div class="confirm-body">
        <p>
          {{ t('components.mcp.form.deleteMessage', { name: confirmState.target?.name ?? '' }) }}
        </p>
      </div>
      <footer class="form-actions confirm-actions">
        <BaseButton variant="outline" type="button" :disabled="saveBusy" @click="closeConfirm">
          {{ t('components.mcp.form.actions.cancel') }}
        </BaseButton>
        <BaseButton variant="danger" type="button" :disabled="saveBusy" @click="confirmDelete">
          {{ t('components.mcp.form.actions.delete') }}
        </BaseButton>
      </footer>
    </InlineModal>

    <BatchImportModal :open="showBatchImport" @close="closeBatchImport" @imported="onBatchImported" />
  </PageLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onActivated, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseButton from '../common/BaseButton.vue'
import PageLayout from '../common/PageLayout.vue'
import InlineModal from '../common/InlineModal.vue'
import FullScreenPanel from '../common/FullScreenPanel.vue'
import BaseInput from '../common/BaseInput.vue'
import BaseTextarea from '../common/BaseTextarea.vue'
import BatchImportModal from './BatchImportModal.vue'
import {
  fetchMcpServers,
  saveMcpServers,
  type McpPlatform,
  type McpServer,
  type McpServerType,
} from '../../services/mcp'
import lobeIcons from '../../icons/lobeIconMap'
import { showToast } from '../../utils/toast'

type KeyValueEntry = {
  id: number
  key: string
  value: string
}

type McpForm = {
  name: string
  type: McpServerType
  command: string
  cwd: string
  url: string
  startupTimeoutSec: number
  website: string
  tips: string
  argsText: string
  envEntries: KeyValueEntry[]
  headersEntries: KeyValueEntry[]
  enablePlatform: McpPlatform[]
}

const { t } = useI18n()

const servers = ref<McpServer[]>([])
const loading = ref(false)
const saveBusy = ref(false)
const errorMessage = ref('')
const modalError = ref('')
const placeholderRegex = /\{([a-zA-Z0-9_]+)\}/g

let entryId = 0

const createKeyValueEntry = (key = '', value = ''): KeyValueEntry => ({
  id: ++entryId,
  key,
  value,
})

const createEmptyForm = (): McpForm => ({
  name: '',
  type: 'stdio',
  command: '',
  cwd: '',
  url: '',
  startupTimeoutSec: 0,
  website: '',
  tips: '',
  argsText: '',
  envEntries: [createKeyValueEntry()],
  headersEntries: [createKeyValueEntry()],
  enablePlatform: [],
})

const modalState = reactive({
  open: false,
  editingName: '',
  form: createEmptyForm(),
})

// Session ID：防止异步回调竞态条件（快速切换 modal 时旧回调覆盖新状态）
let modalSessionId = 0

// 表单模式：JSON 配置编辑器状态（单服务器对象，不含 name 与平台）
const formJsonExpanded = ref(true)
const formJsonLocked = ref(true)
const formJsonSyncedText = ref('')
const formJsonEditingText = ref('')
const formJsonError = ref('')
const formJsonTextareaRef = ref<InstanceType<typeof BaseTextarea> | null>(null)

const formJsonDirty = computed(() => !formJsonLocked.value && formJsonEditingText.value !== formJsonSyncedText.value)

const confirmState = reactive<{ open: boolean; target: McpServer | null }>({
  open: false,
  target: null,
})

const showBatchImport = ref(false)

const openBatchImport = () => {
  showBatchImport.value = true
}

const closeBatchImport = () => {
  showBatchImport.value = false
}

const onBatchImported = async () => {
  await loadServers()
}

const platformOptions = computed(() => [
  { id: 'claude-code' as McpPlatform, label: t('components.mcp.platforms.claude') },
  { id: 'codex' as McpPlatform, label: t('components.mcp.platforms.codex') },
  { id: 'gemini' as McpPlatform, label: t('components.mcp.platforms.gemini') },
])

const formMissingPlaceholders = computed(() =>
  detectPlaceholders(
    modalState.form.url,
    modalState.form.command,
    modalState.form.argsText,
    modalState.form.cwd,
    modalState.form.envEntries,
    modalState.form.headersEntries,
  )
)

const loadServers = async () => {
  loading.value = true
  errorMessage.value = ''
  try {
    const data = await fetchMcpServers()
    servers.value = (data ?? []).map((item) => ({
      ...item,
      args: item.args ?? [],
      cwd: item.cwd ?? '',
      env: item.env ?? {},
      headers: item.headers ?? {},
      startup_timeout_sec: item.startup_timeout_sec ?? 0,
      enable_platform: item.enable_platform ?? [],
      website: item.website ?? '',
      tips: item.tips ?? '',
      missing_placeholders: item.missing_placeholders ?? [],
    }))
  } catch (error) {
    console.error('failed to load mcp servers', error)
    errorMessage.value = t('components.mcp.list.loadError')
  } finally {
    loading.value = false
  }
}

const persistServers = async () => {
  saveBusy.value = true
  try {
    await saveMcpServers(servers.value)
    await loadServers()
  } catch (error) {
    console.error('failed to save mcp servers', error)
    errorMessage.value = t('components.mcp.list.saveError')
  } finally {
    saveBusy.value = false
  }
}

const iconSvg = (name: string) => {
  if (!name) return lobeIcons['mcp'] ?? ''
  const key = name.toLowerCase()
  return lobeIcons[key] ?? lobeIcons['mcp'] ?? ''
}

const iconStyle = (name: string) => ({
  backgroundColor: 'rgba(255,255,255,0.08)',
  color: 'var(--text-primary)',
})

const serverInitials = (name: string) => {
  if (!name) return 'MC'
  return name
    .split(/\s+/)
    .filter(Boolean)
    .map((word) => word[0])
    .join('')
    .slice(0, 2)
    .toUpperCase()
}

const serverSummary = (server: McpServer) => {
  if (server.type === 'http' && server.url) {
    return `${t('components.mcp.types.httpShort')} · ${server.url}`
  }
  if (server.command) {
    return `${t('components.mcp.types.stdioShort')} · ${server.command}`
  }
  return server.type === 'http' ? t('components.mcp.types.httpShort') : t('components.mcp.types.stdioShort')
}

const typeLabel = (type: McpServerType) =>
  type === 'http' ? t('components.mcp.types.http') : t('components.mcp.types.stdio')

const platformEnabled = (server: McpServer, platform: McpPlatform) =>
  server.enable_platform?.includes(platform) ?? false

const platformActive = (server: McpServer, platform: McpPlatform) => {
  switch (platform) {
    case 'claude-code':
      return server.enabled_in_claude
    case 'codex':
      return server.enabled_in_codex
    case 'gemini':
      return server.enabled_in_gemini
    default:
      return false
  }
}

const hasMissingPlaceholders = (server: McpServer) => (server.missing_placeholders?.length ?? 0) > 0

const showPlaceholderWarning = (variables: string[]) => {
  const list = (variables ?? []).filter(Boolean)
  showToast(t('components.mcp.toast.placeholder', { vars: list.join(', ') || 'variables' }), 'error')
}

const onModalPlatformToggle = (platform: McpPlatform, event: Event) => {
  const targetInput = event.target as HTMLInputElement | null
  if (!targetInput) return

  if (formMissingPlaceholders.value.length > 0) {
    targetInput.checked = modalState.form.enablePlatform.includes(platform)
    showPlaceholderWarning(formMissingPlaceholders.value)
    return
  }

  const next = new Set<McpPlatform>(modalState.form.enablePlatform)
  if (targetInput.checked) {
    next.add(platform)
  } else {
    next.delete(platform)
  }
  modalState.form.enablePlatform = Array.from(next)
}

const onPlatformToggle = async (server: McpServer, platform: McpPlatform, event: Event) => {
  const targetInput = event.target as HTMLInputElement | null
  if (!targetInput) return

  if (hasMissingPlaceholders(server)) {
    targetInput.checked = platformEnabled(server, platform)
    showPlaceholderWarning(server.missing_placeholders ?? [])
    return
  }

  const target = servers.value.find((item) => item.name === server.name)
  if (!target) return

  const next = new Set<McpPlatform>(target.enable_platform ?? [])
  if (targetInput.checked) {
    next.add(platform)
  } else {
    next.delete(platform)
  }
  target.enable_platform = Array.from(next)
  await persistServers()
}

const openCreateModal = () => {
  modalSessionId++  // 递增 session ID，使旧异步回调失效
  modalState.open = true
  modalState.editingName = ''
  modalState.form = createEmptyForm()
  modalError.value = ''
  // 初始化表单 JSON 编辑器状态
  formJsonExpanded.value = true
  formJsonLocked.value = true
  formJsonSyncedText.value = ''
  formJsonEditingText.value = ''
  formJsonError.value = ''
  syncJsonFromForm()
}

const openEditModal = (server: McpServer) => {
  modalSessionId++  // 递增 session ID，使旧异步回调失效
  modalState.open = true
  modalState.editingName = server.name
  modalError.value = ''
  modalState.form = {
    name: server.name,
    type: server.type,
    command: server.command ?? '',
    cwd: server.cwd ?? '',
    url: server.url ?? '',
    startupTimeoutSec: server.startup_timeout_sec ?? 0,
    website: server.website ?? '',
    tips: server.tips ?? '',
    argsText: (server.args ?? []).join('\n'),
    envEntries: buildEnvEntries(server.env),
    headersEntries: buildHeaderEntries(server.headers),
    enablePlatform: [...(server.enable_platform ?? [])],
  }
  // 初始化表单 JSON 编辑器状态
  formJsonExpanded.value = true
  formJsonLocked.value = true
  formJsonSyncedText.value = ''
  formJsonEditingText.value = ''
  formJsonError.value = ''
  syncJsonFromForm()
}

const closeModal = () => {
  modalState.open = false
  modalState.editingName = ''
  modalState.form = createEmptyForm()
  modalError.value = ''
  // 重置表单 JSON 编辑器状态
  formJsonExpanded.value = true
  formJsonLocked.value = true
  formJsonSyncedText.value = ''
  formJsonEditingText.value = ''
  formJsonError.value = ''
}

// ========== 表单模式：JSON 配置编辑器（单服务器对象） ==========
const toggleFormJsonExpanded = () => {
  formJsonExpanded.value = !formJsonExpanded.value
}

const focusFormJsonTextarea = () => {
  nextTick(() => {
    requestAnimationFrame(() => {
      formJsonTextareaRef.value?.focus()
    })
  })
}

const toggleJsonLock = () => {
  formJsonError.value = ''
  formJsonLocked.value = !formJsonLocked.value

  if (formJsonLocked.value) {
    // 回到锁定状态：丢弃未应用的编辑
    formJsonEditingText.value = formJsonSyncedText.value
    return
  }

  // 解锁：展开并聚焦输入
  formJsonExpanded.value = true
  formJsonEditingText.value = formJsonSyncedText.value
  focusFormJsonTextarea()
}

const buildJsonFromForm = () => {
  const form = modalState.form
  const startupTimeoutSec = Number(form.startupTimeoutSec) || 0
  if (form.type === 'http') {
    const headers = parseKeyValueEntries(form.headersEntries)
    return {
      type: 'http',
      url: form.url.trim(),
      ...(Object.keys(headers).length ? { headers } : {}),
      ...(startupTimeoutSec > 0 ? { startup_timeout_sec: startupTimeoutSec } : {}),
    }
  }
  const env = parseKeyValueEntries(form.envEntries)
  return {
    type: 'stdio',
    command: form.command.trim(),
    args: parseArgs(form.argsText),
    ...(form.cwd.trim() ? { cwd: form.cwd.trim() } : {}),
    ...(Object.keys(env).length ? { env } : {}),
    ...(startupTimeoutSec > 0 ? { startup_timeout_sec: startupTimeoutSec } : {}),
  }
}

type FormatJsonResult =
  | { ok: true; text: string; value: Record<string, unknown> }
  | { ok: false; error: string }

const formatJson = (input: string): FormatJsonResult => {
  const trimmed = input.trim()
  if (!trimmed) {
    return { ok: false, error: t('components.mcp.form.jsonEditor.errors.empty') }
  }
  try {
    const parsed = JSON.parse(trimmed) as unknown
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return { ok: false, error: t('components.mcp.form.jsonEditor.errors.mustBeObject') }
    }
    return { ok: true, text: JSON.stringify(parsed, null, 2), value: parsed as Record<string, unknown> }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    return { ok: false, error: t('components.mcp.form.jsonEditor.errors.invalidJson', { message }) }
  }
}

const syncJsonFromForm = () => {
  const prevSynced = formJsonSyncedText.value
  const nextSynced = JSON.stringify(buildJsonFromForm(), null, 2)
  formJsonSyncedText.value = nextSynced

  const editingWasSynced = formJsonEditingText.value === prevSynced
  if (formJsonLocked.value || editingWasSynced) {
    formJsonEditingText.value = nextSynced
  }
}

const resetJsonFromForm = () => {
  formJsonError.value = ''
  formJsonEditingText.value = formJsonSyncedText.value
}

const parseJsonArgs = (value: unknown): string[] => {
  if (value === undefined) return []
  if (!Array.isArray(value)) {
    throw new Error(t('components.mcp.form.jsonEditor.errors.argsInvalid'))
  }
  return value
    .map((item) => {
      if (typeof item !== 'string') {
        throw new Error(t('components.mcp.form.jsonEditor.errors.argsInvalid'))
      }
      return item.trim()
    })
    .filter(Boolean)
}

const parseJsonStringMap = (value: unknown, errorMessage: string): Record<string, string> => {
  if (value === undefined) return {}
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new Error(errorMessage)
  }

  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    const key = k.trim()
    if (!key) continue
    if (v === null || v === undefined) continue
    if (typeof v === 'string') {
      out[key] = v
      continue
    }
    if (typeof v === 'number' || typeof v === 'boolean') {
      out[key] = String(v)
      continue
    }
    throw new Error(errorMessage)
  }
  return out
}

const parseJsonEnv = (value: unknown): Record<string, string> =>
  parseJsonStringMap(value, t('components.mcp.form.jsonEditor.errors.envInvalid'))

const parseJsonHeaders = (value: unknown): Record<string, string> =>
  parseJsonStringMap(value, t('components.mcp.form.jsonEditor.errors.headersInvalid'))

const parseJsonCwd = (value: unknown): string => {
  if (value === undefined) return ''
  if (typeof value !== 'string') {
    throw new Error(t('components.mcp.form.jsonEditor.errors.cwdInvalid'))
  }
  return value.trim()
}

const parseJsonStartupTimeoutSec = (value: unknown): number => {
  if (value === undefined) return 0
  if (typeof value === 'number') {
    return Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0
  }
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return 0
    const parsed = Number(trimmed)
    if (!Number.isFinite(parsed)) {
      throw new Error(t('components.mcp.form.jsonEditor.errors.startupTimeoutInvalid'))
    }
    return Math.max(0, Math.floor(parsed))
  }
  throw new Error(t('components.mcp.form.jsonEditor.errors.startupTimeoutInvalid'))
}

const applyJsonToForm = () => {
  formJsonError.value = ''
  const formatted = formatJson(formJsonEditingText.value)
  if (!formatted.ok) {
    formJsonError.value = formatted.error
    return
  }

  const data = formatted.value
  const typeValue = typeof data.type === 'string' ? data.type.trim() : ''
  if (!typeValue) {
    formJsonError.value = t('components.mcp.form.jsonEditor.errors.typeRequired')
    return
  }
  const normalizedType = typeValue === 'sse' || typeValue === 'streamable_http' || typeValue === 'streamable-http'
    ? 'http'
    : typeValue
  if (normalizedType !== 'stdio' && normalizedType !== 'http') {
    formJsonError.value = t('components.mcp.form.jsonEditor.errors.typeInvalid')
    return
  }

  if (normalizedType === 'stdio') {
    const command = typeof data.command === 'string' ? data.command.trim() : ''
    if (!command) {
      formJsonError.value = t('components.mcp.form.jsonEditor.errors.commandRequired')
      return
    }
    try {
      const args = parseJsonArgs(data.args)
      const env = parseJsonEnv(data.env)
      const cwd = parseJsonCwd(data.cwd)
      const startupTimeoutSec = parseJsonStartupTimeoutSec((data as any).startup_timeout_sec)
      modalState.form.type = 'stdio'
      modalState.form.command = command
      modalState.form.argsText = args.join('\n')
      modalState.form.envEntries = buildEnvEntries(env)
      modalState.form.cwd = cwd
      modalState.form.startupTimeoutSec = startupTimeoutSec
      modalState.form.headersEntries = [createKeyValueEntry()]
    } catch (error) {
      formJsonError.value = error instanceof Error ? error.message : t('components.mcp.form.jsonEditor.errors.applyFailed')
      return
    }
  } else {
    const url = typeof data.url === 'string' ? data.url.trim() : ''
    if (!url) {
      formJsonError.value = t('components.mcp.form.jsonEditor.errors.urlRequired')
      return
    }
    let headers: Record<string, string> = {}
    let startupTimeoutSec = 0
    try {
      headers = parseJsonHeaders((data as any).headers)
      startupTimeoutSec = parseJsonStartupTimeoutSec((data as any).startup_timeout_sec)
    } catch (error) {
      formJsonError.value = error instanceof Error ? error.message : t('components.mcp.form.jsonEditor.errors.applyFailed')
      return
    }
    modalState.form.type = 'http'
    modalState.form.url = url
    modalState.form.headersEntries = buildHeaderEntries(headers)
    modalState.form.startupTimeoutSec = startupTimeoutSec
    modalState.form.command = ''
    modalState.form.argsText = ''
    modalState.form.cwd = ''
    modalState.form.envEntries = [createKeyValueEntry()]
  }

  // 先统一格式，避免缩进差异导致 dirty 误判
  formJsonEditingText.value = formatted.text
  nextTick(() => {
    syncJsonFromForm()
    formJsonEditingText.value = formJsonSyncedText.value
  })
}

watch(
  () => modalState.form,
  () => {
    if (!modalState.open) return
    syncJsonFromForm()
  },
  { deep: true }
)

const buildEnvEntries = (env: Record<string, string> | undefined) => {
  const entries = Object.entries(env ?? {})
  if (!entries.length) {
    return [createKeyValueEntry()]
  }
  return entries.map(([key, value]) => createKeyValueEntry(key, value))
}

const addEnvEntry = () => {
  modalState.form.envEntries.push(createKeyValueEntry())
}

const removeEnvEntry = (id: number) => {
  if (modalState.form.envEntries.length === 1) return
  const index = modalState.form.envEntries.findIndex((entry) => entry.id === id)
  if (index !== -1) {
    modalState.form.envEntries.splice(index, 1)
  }
}

const buildHeaderEntries = (headers: Record<string, string> | undefined) => {
  const entries = Object.entries(headers ?? {})
  if (!entries.length) {
    return [createKeyValueEntry()]
  }
  return entries.map(([key, value]) => createKeyValueEntry(key, value))
}

const addHeaderEntry = () => {
  modalState.form.headersEntries.push(createKeyValueEntry())
}

const removeHeaderEntry = (id: number) => {
  if (modalState.form.headersEntries.length === 1) return
  const index = modalState.form.headersEntries.findIndex((entry) => entry.id === id)
  if (index !== -1) {
    modalState.form.headersEntries.splice(index, 1)
  }
}

const closeConfirm = () => {
  confirmState.open = false
  confirmState.target = null
}

const requestDelete = (server: McpServer) => {
  confirmState.target = server
  confirmState.open = true
}

const confirmDelete = async () => {
  if (!confirmState.target) return
  servers.value = servers.value.filter((server) => server.name !== confirmState.target?.name)
  closeConfirm()
  await persistServers()
}

const submitModal = async () => {
  modalError.value = ''
  if (formJsonDirty.value) {
    showToast(t('components.mcp.form.jsonEditor.toast.dirtyNotApplied'), 'warning')
  }
  const form = modalState.form
  const trimmedName = form.name.trim()
  if (!trimmedName) {
    modalError.value = t('components.mcp.form.errors.name')
    return
  }
  if (form.type === 'stdio' && !form.command.trim()) {
    modalError.value = t('components.mcp.form.errors.command')
    return
  }
  if (form.type === 'http' && !form.url.trim()) {
    modalError.value = t('components.mcp.form.errors.url')
    return
  }

  // 平台校验：至少勾选一个平台
  if (form.enablePlatform.length === 0) {
    modalError.value = t('components.mcp.form.errors.noPlatformSelected')
    return
  }

  const existing = servers.value.find((server) => server.name === trimmedName)
  if (!modalState.editingName && existing) {
    modalError.value = t('components.mcp.form.errors.duplicate')
    return
  }
  if (modalState.editingName && modalState.editingName !== trimmedName && existing) {
    modalError.value = t('components.mcp.form.errors.duplicate')
    return
  }

  const startupTimeoutSec = Number(form.startupTimeoutSec) || 0
  const env = form.type === 'stdio' ? parseKeyValueEntries(form.envEntries) : {}
  const headers = form.type === 'http' ? parseKeyValueEntries(form.headersEntries) : {}
  const payload: McpServer = {
    name: trimmedName,
    type: form.type,
    command: form.type === 'stdio' ? form.command.trim() : '',
    args: form.type === 'stdio' ? parseArgs(form.argsText) : [],
    cwd: form.type === 'stdio' ? form.cwd.trim() : '',
    env,
    url: form.type === 'http' ? form.url.trim() : '',
    headers,
    ...(startupTimeoutSec > 0 ? { startup_timeout_sec: startupTimeoutSec } : {}),
    website: form.website.trim(),
    tips: form.tips.trim(),
    enable_platform: [...form.enablePlatform],
    enabled_in_claude:
      modalState.editingName === trimmedName
        ? existing?.enabled_in_claude ?? false
        : servers.value.find((server) => server.name === modalState.editingName)?.enabled_in_claude ?? false,
    enabled_in_codex:
      modalState.editingName === trimmedName
        ? existing?.enabled_in_codex ?? false
        : servers.value.find((server) => server.name === modalState.editingName)?.enabled_in_codex ?? false,
    enabled_in_gemini:
      modalState.editingName === trimmedName
        ? existing?.enabled_in_gemini ?? false
        : servers.value.find((server) => server.name === modalState.editingName)?.enabled_in_gemini ?? false,
    missing_placeholders: [],
  }

  if (modalState.editingName) {
    const index = servers.value.findIndex((server) => server.name === modalState.editingName)
    if (index !== -1) {
      servers.value.splice(index, 1, payload)
    } else {
      servers.value.push(payload)
    }
  } else {
    servers.value.push(payload)
  }

  // 检查占位符并提示
  const placeholders = formMissingPlaceholders.value
  if (placeholders.length > 0 && form.enablePlatform.length > 0) {
    // 显示警告（允许保存，但提示未同步）
    showToast(
      t('components.mcp.form.warnings.savedWithPlaceholders', {
        vars: placeholders.join(', ')
      }),
      'warning'
    )
  }

  closeModal()
  await persistServers()
}

const parseArgs = (value: string) =>
  value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)

const parseKeyValueEntries = (entries: KeyValueEntry[]) => {
  return entries.reduce<Record<string, string>>((acc, entry) => {
    const key = entry.key.trim()
    if (!key) return acc
    acc[key] = entry.value
    return acc
  }, {})
}

const reload = async () => {
  await loadServers()
}

const detectPlaceholders = (
  url: string,
  command: string,
  argsText: string,
  cwd: string,
  envEntries: KeyValueEntry[],
  headerEntries: KeyValueEntry[],
) => {
  const set = new Set<string>()
  collectPlaceholders(url, set)
  collectPlaceholders(command, set)
  collectPlaceholders(cwd, set)
  argsText
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .forEach((line) => collectPlaceholders(line, set))
  envEntries.forEach((entry) => {
    collectPlaceholders(entry.value, set)
  })
  headerEntries.forEach((entry) => {
    collectPlaceholders(entry.value, set)
  })
  return Array.from(set)
}

const collectPlaceholders = (value: string, set: Set<string>) => {
  if (!value) return
  const matches = value.matchAll(placeholderRegex)
  for (const match of matches) {
    const key = match[1]
    if (key) {
      set.add(key)
    }
  }
}

onMounted(() => {
  void loadServers()
})

onActivated(() => {
  void loadServers()
})
</script>

<style scoped>
/* 修复：提升 MCP 全屏面板层级，避免被全局 modal 遮罩层覆盖 */
:global(body .mcp-fullscreen-panel.panel-container) {
  z-index: 2100;
}

.chip {
  padding: 2px 8px;
  border-radius: 999px;
  border: 1px solid color-mix(in srgb, var(--mac-border) 75%, transparent);
  background: color-mix(in srgb, var(--mac-surface) 80%, transparent);
  font-size: 12px;
  text-transform: uppercase;
  color: var(--mac-text-secondary);
}

.mcp-table-wrapper {
  overflow-x: auto;
  border: 1px solid var(--mac-border);
  border-radius: 16px;
  background: var(--mac-surface);
}

.mcp-table {
  width: 100%;
  border-collapse: collapse;
  min-width: 980px;
}

.mcp-table th,
.mcp-table td {
  padding: 10px 12px;
  border-bottom: 1px solid color-mix(in srgb, var(--mac-border) 70%, transparent);
  vertical-align: top;
}

.mcp-table th {
  font-size: 12px;
  color: var(--mac-text-secondary);
  text-align: left;
  font-weight: 600;
  background: color-mix(in srgb, var(--mac-surface) 75%, transparent);
}

.mcp-table tbody tr:hover td {
  background: color-mix(in srgb, var(--mac-surface) 70%, rgba(255, 255, 255, 0.04));
}

.col-name {
  width: 340px;
}

.col-type {
  width: 120px;
}

.col-endpoint {
  width: 320px;
}

.col-platform {
  width: 120px;
  text-align: center;
}

.cell-platform {
  text-align: center;
  vertical-align: middle;
}

.cell-actions {
  white-space: nowrap;
  text-align: right;
}

.name-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}

.name-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.name-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.server-name {
  font-weight: 700;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.name-sub {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.6);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.placeholder-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 700;
  background: rgba(244, 67, 54, 0.18);
  color: #ff9b9b;
}

.endpoint-main {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  color: rgba(255, 255, 255, 0.85);
  word-break: break-all;
}

.endpoint-meta {
  margin-top: 4px;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.meta-item {
  font-size: 11px;
  color: rgba(255, 255, 255, 0.55);
}

.platform-cell {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.platform-dot {
  width: 8px;
  height: 8px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.2);
}

.platform-dot.active {
  background: #4ade80;
}

.automation-card {
  flex-direction: column;
  align-items: stretch;
  gap: 14px;
}

.card-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.card-platforms {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.75rem;
  width: 100%;
}

.platform-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  padding: 10px 12px;
  border-radius: 16px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.06);
}

.platform-info {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.75rem;
  width: 100%;
}

.platform-label {
  font-weight: 600;
}

.platform-controls {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.platform-status {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.6);
}

.platform-status.active {
  color: #4ade80;
}

.card-actions {
  display: inline-flex;
  flex-direction: row;
  gap: 0.5rem;
  align-items: center;
  justify-content: flex-end;
  flex-shrink: 0;
}

.empty-state {
  text-align: center;
  padding: 2rem;
  border: 1px dashed rgba(255, 255, 255, 0.2);
  border-radius: 16px;
}

.alert-error {
  margin-bottom: 1rem;
  padding: 0.75rem 1rem;
  border-radius: 12px;
  background: rgba(244, 67, 54, 0.15);
  color: #ff9b9b;
}

.vendor-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.modal-scroll {
  max-height: 65vh;
  overflow-y: auto;
  padding-right: 0.25rem;
  margin-right: -0.25rem;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.field-hint {
  margin-top: 0.25rem;
  font-size: 12px;
  line-height: 1.4;
  color: rgba(255, 255, 255, 0.65);
}

.form-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 1rem;
  width: 100%;
}

.env-table {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.env-row {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: 0.5rem;
  align-items: center;
}

.env-add {
  align-self: flex-start;
}

.platform-checkboxes {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
}

.platform-checkbox {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.75rem;
}

.card-leading {
  display: flex;
  gap: 1rem;
  min-width: 0;
}

.card-icon {
  display: inline-flex;
  justify-content: center;
  align-items: center;
  width: 48px;
  height: 48px;
  border-radius: 14px;
  overflow: hidden;
}

.card-text {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  min-width: 0;
}

.card-link {
  margin-top: 0.25rem;
}

.card-link a {
  color: var(--link-color, #9acaff);
  text-decoration: none;
}

.card-link a:hover {
  text-decoration: underline;
}

.card-tip {
  margin-top: 0.25rem;
  font-size: 13px;
  color: rgba(255, 255, 255, 0.7);
}

.icon-svg :deep(svg) {
  width: 32px;
  height: 32px;
}

.confirm-body {
  margin-bottom: 1rem;
}

@media (max-width: 900px) {
  .card-platforms {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .card-header {
    flex-direction: column;
  }

  .card-actions {
    align-self: flex-end;
  }

  .card-platforms {
    grid-template-columns: 1fr;
  }
}

/* 表单模式 JSON 配置编辑器 */
.mcp-json-field {
  margin-top: 0.5rem;
}

.mcp-json-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 10px 12px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.06);
  cursor: pointer;
  user-select: none;
}

.mcp-json-expand-icon {
  width: 18px;
  height: 18px;
  flex: 0 0 auto;
  transition: transform 0.15s ease;
  opacity: 0.9;
}

.mcp-json-expand-icon.expanded {
  transform: rotate(180deg);
}

.mcp-json-title {
  flex: 1;
  font-weight: 600;
}

.mcp-json-dirty {
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(251, 191, 36, 0.15);
  border: 1px solid rgba(251, 191, 36, 0.28);
  color: #fbbf24;
}

.mcp-json-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.mcp-json-action-btn {
  padding: 6px 10px;
  border-radius: 10px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.06);
  color: rgba(255, 255, 255, 0.9);
  font-size: 12px;
  cursor: pointer;
}

.mcp-json-action-btn.primary {
  border-color: rgba(74, 222, 128, 0.35);
  background: rgba(74, 222, 128, 0.12);
}

.mcp-json-action-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.mcp-json-body {
  margin-top: 0.75rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.mcp-json-preview {
  padding: 12px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(0, 0, 0, 0.2);
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow: auto;
  max-height: 280px;
}

.mcp-json-textarea {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
}

.mcp-json-hint {
  margin: 0;
  font-size: 12px;
  color: rgba(255, 255, 255, 0.6);
}
</style>
