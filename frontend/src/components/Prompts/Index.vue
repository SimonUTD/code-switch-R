<script setup lang="ts">
import { ref, computed, onMounted, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import MarkdownEditor from '../common/MarkdownEditor.vue'
import PageLayout from '../common/PageLayout.vue'
import BaseButton from '../common/BaseButton.vue'
import {
  GetPrompts,
  UpsertPrompt,
  DeletePrompt,
  EnablePrompt,
  ImportFromFile,
  GetCurrentFileContent
} from '../../../bindings/codeswitch/services/promptservice'
import type { Prompt } from '../../../bindings/codeswitch/services/models'

const { t } = useI18n()

type Platform = 'claude' | 'codex' | 'gemini'

const platforms = computed(() => [
  { id: 'claude' as const, name: t('components.mcp.platforms.claude') },
  { id: 'codex' as const, name: t('components.mcp.platforms.codex') },
  { id: 'gemini' as const, name: t('components.mcp.platforms.gemini') },
])

const activePlatform = ref<Platform>('claude')
const prompts = ref<Record<string, Prompt | undefined>>({})
const loading = ref(false)
const showModal = ref(false)
const editingPrompt = ref<Prompt | null>(null)
const currentFileContent = ref<string | null>(null)
const nameInputRef = ref<HTMLInputElement | null>(null)

// 表单
const formData = ref({
  id: '',
  name: '',
  content: '',
  description: '',
  enabled: false
})

const promptList = computed(() => Object.values(prompts.value).filter((item): item is Prompt => !!item))
const enabledPrompt = computed(() => promptList.value.find(p => p.enabled))
const promptCount = computed(() => promptList.value.length)

async function loadPrompts() {
  loading.value = true
  try {
    prompts.value = await GetPrompts(activePlatform.value)
    currentFileContent.value = await GetCurrentFileContent(activePlatform.value)
  } catch (e) {
    console.error('Failed to load prompts:', e)
  } finally {
    loading.value = false
  }
}

async function handleToggleEnabled(prompt: Prompt) {
  try {
    if (!prompt.enabled) {
      await EnablePrompt(activePlatform.value, prompt.id)
    } else {
      // 禁用：将 enabled 设为 false
      await UpsertPrompt(activePlatform.value, prompt.id, { ...prompt, enabled: false })
    }
    await loadPrompts()
  } catch (e) {
    console.error('Failed to toggle prompt:', e)
  }
}

function openCreateModal() {
  editingPrompt.value = null
  formData.value = {
    id: crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`,
    name: '',
    content: '',
    description: '',
    enabled: false
  }
  showModal.value = true
  // 等待 DOM 更新后聚焦输入框（修复 macOS WebView 键盘输入问题）
  nextTick(() => {
    nameInputRef.value?.focus()
  })
}

async function openEditModal(prompt: Prompt) {
  editingPrompt.value = prompt

  // 如果是已启用的提示词，从文件读取最新内容
  let content = prompt.content
  if (prompt.enabled) {
    try {
      const fileContent = await GetCurrentFileContent(activePlatform.value)
      if (fileContent !== null) {
        content = fileContent
      }
    } catch (e) {
      console.error('Failed to get current file content:', e)
    }
  }

  formData.value = {
    id: prompt.id,
    name: prompt.name,
    content: content,
    description: prompt.description || '',
    enabled: prompt.enabled
  }
  showModal.value = true
  // 等待 DOM 更新后聚焦输入框（修复 macOS WebView 键盘输入问题）
  nextTick(() => {
    nameInputRef.value?.focus()
  })
}

async function savePrompt() {
  try {
    const prompt: Prompt = {
      id: formData.value.id,
      name: formData.value.name,
      content: formData.value.content,
      description: formData.value.description || undefined,
      enabled: formData.value.enabled
    }
    await UpsertPrompt(activePlatform.value, prompt.id, prompt)
    showModal.value = false
    await loadPrompts()
  } catch (e) {
    console.error('Failed to save prompt:', e)
  }
}

async function deletePrompt(id: string) {
  if (!confirm(t('prompts.confirmDelete'))) return
  try {
    await DeletePrompt(activePlatform.value, id)
    await loadPrompts()
  } catch (e) {
    console.error('Failed to delete prompt:', e)
  }
}

async function handleImport() {
  try {
    loading.value = true
    await ImportFromFile(activePlatform.value)
    await loadPrompts()
  } catch (e) {
    console.error('Failed to import:', e)
  } finally {
    loading.value = false
  }
}

watch(activePlatform, () => {
  loadPrompts()
})

onMounted(() => {
  loadPrompts()
})
</script>

<template>
  <PageLayout
    :title="t('sidebar.prompts')"
    :sticky="true"
  >
    <template #actions>
      <button
        type="button"
        class="ghost-icon"
        :data-tooltip="t('prompts.actions.import')"
        :aria-label="t('prompts.actions.import')"
        :disabled="loading"
        @click="handleImport"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path
            d="M12 4v9"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            fill="none"
          />
          <path
            d="M8.5 10.5l3.5 3.5 3.5-3.5"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            fill="none"
          />
          <path
            d="M5 19h14"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            fill="none"
          />
        </svg>
      </button>
      <button
        type="button"
        class="ghost-icon"
        :data-tooltip="t('prompts.actions.create')"
        :aria-label="t('prompts.actions.create')"
        @click="openCreateModal"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path
            d="M12 5v14M5 12h14"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
            fill="none"
          />
        </svg>
      </button>
    </template>

    <p class="page-lead">{{ t('prompts.hero.lead') }}</p>

    <div class="section-header">
      <div class="tab-group" role="tablist" :aria-label="t('prompts.hero.title')">
        <button
          v-for="platform in platforms"
          :key="platform.id"
          class="tab-pill"
          :class="{ active: activePlatform === platform.id }"
          type="button"
          role="tab"
          :aria-selected="activePlatform === platform.id"
          @click="activePlatform = platform.id"
        >
          {{ platform.name }}
        </button>
      </div>
    </div>

    <!-- Stats Bar -->
    <div class="stats-bar">
      <span class="stat-text">
        {{ t('prompts.stats.total', { count: promptCount }) }}
      </span>
      <span v-if="enabledPrompt" class="stat-enabled">
        {{ t('prompts.stats.enabled') }}: {{ enabledPrompt.name }}
      </span>
    </div>

    <!-- Prompt List -->
    <div class="prompt-list" v-if="!loading">
      <div v-if="promptList.length === 0" class="empty-state">
        <p>{{ t('prompts.empty') }}</p>
      </div>

      <div
        v-for="prompt in promptList"
        :key="prompt.id"
        class="prompt-card"
        :class="{ enabled: prompt.enabled }"
      >
        <div class="prompt-main">
          <button
            class="toggle-switch"
            :class="{ on: prompt.enabled }"
            @click="handleToggleEnabled(prompt)"
          >
            <span class="toggle-slider"></span>
          </button>
          <div class="prompt-info">
            <h3 class="prompt-name">{{ prompt.name }}</h3>
            <p v-if="prompt.description" class="prompt-description">
              {{ prompt.description }}
            </p>
          </div>
        </div>
        <div class="prompt-actions">
          <button class="ghost-icon sm" type="button" @click="openEditModal(prompt)">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path>
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path>
            </svg>
          </button>
          <button class="ghost-icon sm danger" type="button" @click="deletePrompt(prompt.id)">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="3 6 5 6 21 6"></polyline>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
            </svg>
          </button>
        </div>
      </div>
    </div>

    <div v-else class="loading-state">
      <span>{{ t('prompts.loading') }}</span>
    </div>

    <!-- Edit Modal (不使用 Teleport 以修复 macOS WebView 键盘输入问题) -->
    <div v-if="showModal" class="modal-overlay" @click.self="showModal = false">
      <div class="modal-content" tabindex="-1">
        <h2 class="modal-title">
          {{ editingPrompt ? t('prompts.form.editTitle') : t('prompts.form.createTitle') }}
        </h2>

        <div class="form-group">
          <label>{{ t('prompts.form.name') }}</label>
          <input
            ref="nameInputRef"
            v-model="formData.name"
            type="text"
            class="form-input"
            :placeholder="t('prompts.form.namePlaceholder')"
          />
        </div>

        <div class="form-group">
          <label>{{ t('prompts.form.description') }}</label>
          <input
            v-model="formData.description"
            type="text"
            class="form-input"
            :placeholder="t('prompts.form.descriptionPlaceholder')"
          />
        </div>

        <div class="form-group">
          <label>{{ t('prompts.form.content') }}</label>
          <MarkdownEditor v-model="formData.content" />
        </div>

        <div class="modal-actions">
          <BaseButton variant="outline" size="sm" type="button" @click="showModal = false">
            {{ t('prompts.form.cancel') }}
          </BaseButton>
          <BaseButton size="sm" type="button" @click="savePrompt" :disabled="!formData.name">
            {{ t('prompts.form.save') }}
          </BaseButton>
        </div>
      </div>
    </div>
  </PageLayout>
</template>

<style scoped src="./prompts.css"></style>
