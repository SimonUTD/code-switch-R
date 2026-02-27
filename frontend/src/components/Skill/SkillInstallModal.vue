<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseButton from '../common/BaseButton.vue'
import BaseModal from '../common/BaseModal.vue'
import { installSkill, type SkillSummary } from '../../services/skill'

type Platform = 'claude' | 'codex'
type InstallLocation = 'user' | 'project'

const props = defineProps<{
  open: boolean
  platform: Platform
  target: SkillSummary | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'installed'): void
  (e: 'error', message: string): void
}>()

const { t } = useI18n()

const installLocation = ref<InstallLocation>('user')
const installing = ref(false)

const canInstall = computed(() => Boolean(props.target?.repo_owner && props.target?.repo_name))

watch(
  () => props.open,
  (open) => {
    if (!open) return
    installLocation.value = 'user'
    installing.value = false
  },
)

const close = () => {
  if (installing.value) return
  emit('close')
}

const confirmInstall = async () => {
  if (!props.target || !canInstall.value) return

  installing.value = true
  try {
    await installSkill({
      directory: props.target.directory,
      repo_owner: props.target.repo_owner,
      repo_name: props.target.repo_name,
      repo_branch: props.target.repo_branch,
      platform: props.platform,
      location: installLocation.value,
    })
    emit('installed')
    emit('close')
  } catch (error) {
    console.error('failed to install skill', error)
    emit('error', t('components.skill.actions.installError', { name: props.target.name }))
  } finally {
    installing.value = false
  }
}
</script>

<template>
  <BaseModal :open="open" :title="t('components.skill.install.title')" @close="close">
    <div class="install-modal-content">
      <p class="install-modal-desc">
        {{ t('components.skill.install.desc', { name: target?.name }) }}
      </p>

      <div class="install-location-options">
        <label class="install-option" :class="{ selected: installLocation === 'user' }">
          <input type="radio" v-model="installLocation" value="user" class="sr-only" :disabled="installing" />
          <div class="install-option-content">
            <svg viewBox="0 0 24 24" class="install-option-icon" aria-hidden="true">
              <path
                d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2M12 11a4 4 0 100-8 4 4 0 000 8z"
                fill="none"
                stroke="currentColor"
                stroke-width="1.5"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
            <div>
              <p class="install-option-title">{{ t('components.skill.install.userLevel') }}</p>
              <p class="install-option-desc">~/.{{ platform }}/skills/</p>
            </div>
          </div>
        </label>

        <label class="install-option" :class="{ selected: installLocation === 'project' }">
          <input type="radio" v-model="installLocation" value="project" class="sr-only" :disabled="installing" />
          <div class="install-option-content">
            <svg viewBox="0 0 24 24" class="install-option-icon" aria-hidden="true">
              <path
                d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"
                fill="none"
                stroke="currentColor"
                stroke-width="1.5"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
            <div>
              <p class="install-option-title">{{ t('components.skill.install.projectLevel') }}</p>
              <p class="install-option-desc">./.{{ platform }}/skills/</p>
              <p class="install-option-warning">{{ t('components.skill.install.gitWarning') }}</p>
            </div>
          </div>
        </label>
      </div>

      <div class="install-modal-actions">
        <BaseButton variant="outline" size="sm" type="button" :disabled="installing" @click="close">
          {{ t('common.cancel') }}
        </BaseButton>
        <BaseButton size="sm" type="button" :disabled="installing || !canInstall" @click="confirmInstall">
          {{ installing ? t('components.skill.install.installing') : t('components.skill.install.confirm') }}
        </BaseButton>
      </div>
    </div>
  </BaseModal>
</template>

<style scoped>
.install-modal-content {
  min-width: min(400px, 80vw);
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.install-modal-desc {
  color: var(--mac-text-secondary);
  font-size: 0.95rem;
}

.install-location-options {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.install-option {
  display: block;
  cursor: pointer;
}

.install-option-content {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  padding: 16px;
  border: 2px solid var(--mac-border);
  border-radius: 12px;
  transition: all 0.2s ease;
}

.install-option:hover .install-option-content {
  border-color: var(--mac-accent);
}

.install-option.selected .install-option-content {
  border-color: var(--mac-accent);
  background: color-mix(in srgb, var(--mac-accent) 10%, transparent);
}

.install-option-icon {
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  color: var(--mac-text-secondary);
}

.install-option.selected .install-option-icon {
  color: var(--mac-accent);
}

.install-option-title {
  font-weight: 600;
  margin: 0 0 4px;
}

.install-option-desc {
  font-size: 0.85rem;
  color: var(--mac-text-secondary);
  margin: 0;
  font-family: monospace;
}

.install-option-warning {
  font-size: 0.8rem;
  color: #f59e0b;
  margin: 8px 0 0;
}

.install-modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  padding-top: 8px;
  border-top: 1px solid var(--mac-border);
}
</style>

