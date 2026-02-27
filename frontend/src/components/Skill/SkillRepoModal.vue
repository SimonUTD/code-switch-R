<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Browser } from '@wailsio/runtime'
import BaseModal from '../common/BaseModal.vue'
import { addSkillRepo, fetchSkillRepos, removeSkillRepo, type SkillRepoConfig } from '../../services/skill'

const props = defineProps<{
  open: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'updated'): void
}>()

const { t } = useI18n()

const repoList = ref<SkillRepoConfig[]>([])
const repoLoading = ref(false)
const repoError = ref('')
const repoBusy = ref(false)
const repoForm = reactive({ url: '', branch: 'main' })

const refreshing = computed(() => repoLoading.value || repoBusy.value)

const close = () => {
  if (repoBusy.value) return
  emit('close')
}

const repoKey = (repo: SkillRepoConfig) => `${repo.owner}/${repo.name}`

const openExternal = (target: string) => {
  if (!target) return
  Browser.OpenURL(target).catch(() => console.error('failed to open link', target))
}

const openRepoGithub = (repo: SkillRepoConfig) => {
  if (!repo?.owner || !repo?.name) return
  openExternal(`https://github.com/${repo.owner}/${repo.name}`)
}

const parseRepoInput = (value: string) => {
  let input = value.trim()
  if (!input) return null
  input = input.replace(/^https?:\/\/(www\.)?github\.com\//i, '')
  input = input.replace(/\.git$/i, '')
  const parts = input.split('/')
  if (parts.length < 2) return null
  const owner = parts[0]
  const name = parts[1]
  if (!owner || !name) return null
  return { owner, name }
}

const loadRepos = async () => {
  repoLoading.value = true
  repoError.value = ''
  try {
    repoList.value = await fetchSkillRepos()
  } catch (error) {
    console.error('failed to load skill repos', error)
    repoError.value = t('components.skill.repos.loadError')
  } finally {
    repoLoading.value = false
  }
}

const submitRepo = async () => {
  const parsed = parseRepoInput(repoForm.url)
  if (!parsed) {
    repoError.value = t('components.skill.repos.formError')
    return
  }

  repoBusy.value = true
  repoError.value = ''
  try {
    repoList.value = await addSkillRepo({
      owner: parsed.owner,
      name: parsed.name,
      branch: repoForm.branch || 'main',
      enabled: true,
    })
    repoForm.url = ''
    repoForm.branch = 'main'
    emit('updated')
  } catch (error) {
    console.error('failed to add skill repo', error)
    repoError.value = t('components.skill.repos.addError')
  } finally {
    repoBusy.value = false
  }
}

const removeRepo = async (repo: SkillRepoConfig) => {
  repoBusy.value = true
  repoError.value = ''
  try {
    repoList.value = await removeSkillRepo(repo.owner, repo.name)
    emit('updated')
  } catch (error) {
    console.error('failed to remove skill repo', error)
    repoError.value = t('components.skill.repos.removeError')
  } finally {
    repoBusy.value = false
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    if (repoList.value.length > 0 || repoLoading.value) return
    void loadRepos()
  },
)
</script>

<template>
  <BaseModal :open="open" :title="t('components.skill.repos.title')" @close="close">
    <div class="repo-modal-content">
      <div class="skill-repo-section">
        <p class="skill-repo-subtitle">{{ t('components.skill.repos.subtitle') }}</p>
        <form class="skill-repo-form" @submit.prevent="submitRepo">
          <div class="repo-input-field">
            <input
              v-model="repoForm.url"
              type="text"
              :placeholder="t('components.skill.repos.urlPlaceholder')"
              :disabled="refreshing"
            />
          </div>
          <div class="repo-form-actions">
            <input
              v-model="repoForm.branch"
              type="text"
              :placeholder="t('components.skill.repos.branchPlaceholder')"
              :disabled="refreshing"
            />
            <button
              class="ghost-icon"
              :class="{ rotating: refreshing }"
              type="submit"
              :disabled="refreshing"
              :title="t('components.skill.repos.addLabel')"
              :data-tooltip="t('components.skill.repos.addLabel')"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path
                  d="M12 5v14M5 12h14"
                  stroke="currentColor"
                  stroke-width="1.6"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  fill="none"
                />
              </svg>
            </button>
          </div>
        </form>

        <p v-if="repoError" class="skill-error">{{ repoError }}</p>

        <div class="skill-repo-list" :class="{ loading: repoLoading }">
          <p v-if="repoLoading" class="skill-empty">{{ t('components.skill.repos.loading') }}</p>
          <p v-else-if="!repoList.length" class="skill-empty">{{ t('components.skill.repos.empty') }}</p>
          <div v-else>
            <article v-for="repo in repoList" :key="repoKey(repo)" class="skill-repo-item">
              <div class="skill-repo-meta">
                <p class="repo-name">{{ repo.owner }}/{{ repo.name }}</p>
                <span class="repo-branch">{{ t('components.skill.repos.branchLabel', { branch: repo.branch }) }}</span>
              </div>
              <div class="skill-repo-actions">
                <button
                  class="ghost-icon sm"
                  type="button"
                  :title="t('components.skill.repos.viewLabel')"
                  :data-tooltip="t('components.skill.repos.viewLabel')"
                  @click="openRepoGithub(repo)"
                >
                  <svg viewBox="0 0 24 24" aria-hidden="true">
                    <path
                      d="M12 5h7v7M19 5l-9 9"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="1.6"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    />
                    <path
                      d="M11 6H7a2 2 0 00-2 2v9a2 2 0 002 2h9a2 2 0 002-2v-4"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="1.6"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    />
                  </svg>
                </button>
                <button
                  class="ghost-icon sm danger"
                  type="button"
                  :title="t('components.skill.repos.removeLabel')"
                  :data-tooltip="t('components.skill.repos.removeLabel')"
                  :disabled="refreshing"
                  @click="removeRepo(repo)"
                >
                  <svg viewBox="0 0 24 24" aria-hidden="true">
                    <path
                      d="M5 7h14M10 11v6M14 11v6M9 7V5h6v2"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="1.6"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    />
                    <path
                      d="M6.5 7l-.5 12a2 2 0 002 2h8a2 2 0 002-2L17.5 7"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="1.6"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    />
                  </svg>
                </button>
              </div>
            </article>
          </div>
        </div>
      </div>
    </div>
  </BaseModal>
</template>

<style scoped>
.repo-modal-content {
  min-width: min(600px, 80vw);
}

.skill-repo-section {
  border: 1px solid var(--mac-border);
  border-radius: 20px;
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  background: color-mix(in srgb, var(--mac-surface) 90%, transparent);
}

.skill-repo-subtitle {
  margin: 0 0 12px;
  color: var(--mac-text-secondary);
  font-size: 0.95rem;
}

.skill-repo-form {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  width: 100%;
}

.repo-input-field {
  flex: 1;
  min-width: 220px;
}

.skill-repo-form input {
  border: 1px solid var(--mac-border);
  border-radius: 10px;
  padding: 8px 12px;
  background: var(--mac-surface);
  color: var(--mac-text);
  font-size: 0.9rem;
}

.repo-input-field input {
  width: 100%;
}

.repo-form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  align-items: center;
}

.repo-form-actions input {
  width: 160px;
}

.skill-repo-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.skill-repo-list.loading {
  opacity: 0.7;
}

.skill-repo-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 18px;
  border: 1px solid var(--mac-border);
  border-radius: 12px;
  background: color-mix(in srgb, var(--mac-surface) 80%, transparent);
  gap: 16px;
  margin: 0 0 8px;
}

.skill-repo-item:last-child {
  margin-bottom: 0;
}

.skill-repo-meta {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.skill-repo-meta .repo-name {
  margin: 0;
  font-weight: 600;
}

.skill-repo-meta .repo-branch {
  font-size: 0.85rem;
  color: var(--mac-text-secondary);
}

.skill-repo-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

.skill-empty {
  margin-top: 0;
  color: var(--mac-text-secondary);
  text-align: center;
}

.skill-error {
  color: #f87171;
  margin-top: 0;
}
</style>

