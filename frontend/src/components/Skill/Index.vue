<template>
  <PageLayout
    :title="t('sidebar.skill')"
    :sticky="true"
  >
    <template #actions>
      <button
        class="ghost-icon"
        :class="{ rotating: refreshing }"
        :data-tooltip="t('components.skill.actions.refresh')"
        :disabled="refreshing"
        @click="refresh"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20.5 8a8.5 8.5 0 10-2.38 7.41" fill="none" stroke="currentColor" stroke-width="1.5"
            stroke-linecap="round" stroke-linejoin="round" />
          <path d="M20.5 4v4h-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"
            stroke-linejoin="round" />
        </svg>
      </button>
      <button
        class="ghost-icon"
        :data-tooltip="t('components.skill.actions.openFolder')"
        @click="handleOpenFolder"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" fill="none"
            stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
      <button
        class="ghost-icon"
        :data-tooltip="t('components.skill.repos.open')"
        @click="openRepoModal"
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M5 5h14v6H5zM7 13h10v6H7z" fill="none" stroke="currentColor" stroke-width="1.5"
            stroke-linecap="round" stroke-linejoin="round" />
          <path d="M12 7.5v1M12 15.5v1" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
        </svg>
      </button>
    </template>

    <p class="page-lead">{{ t('components.skill.hero.lead') }}</p>

    <div class="section-header">
      <div class="tab-group" role="tablist" :aria-label="t('components.skill.hero.title')">
        <button
          v-for="platform in platforms"
          :key="platform.value"
          class="tab-pill"
          :class="{ active: activePlatform === platform.value }"
          type="button"
          role="tab"
          :aria-selected="activePlatform === platform.value"
          @click="switchPlatform(platform.value)"
        >
          {{ platform.label }}
        </button>
      </div>
    </div>

      <section class="skill-list-section">
        <div v-if="loading" class="skill-empty">{{ t('components.skill.list.loading') }}</div>

        <template v-else>
          <!-- Project Skills Group -->
          <div v-if="projectSkills.length > 0" class="skill-group">
            <div class="skill-group-header">
              <h2 class="skill-group-title">
                {{ t('components.skill.groups.project') }} ({{ projectSkills.length }})
              </h2>
              <button
                type="button"
                class="ghost-icon sm"
                :title="t('components.skill.actions.openFolder')"
                @click="handleOpenFolderForLocation('project')"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" fill="none"
                    stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
                </svg>
              </button>
            </div>
            <div class="skill-list installed-skills">
              <SkillCard
                v-for="skill in projectSkills"
                :key="skill.key"
                :skill="skill"
                :expanded="expandedSkills.has(skill.key)"
                :toggling="togglingSkill === skill.key"
                :uninstalling="processingSkill === uninstallProcessingKey(skill)"
                @toggle="handleToggle"
                @expand="toggleExpand"
                @uninstall="handleUninstall"
                @view="openGithub"
              />
            </div>
          </div>

          <!-- User Skills Group -->
          <div v-if="userSkills.length > 0" class="skill-group">
            <div class="skill-group-header">
              <h2 class="skill-group-title">
                {{ t('components.skill.groups.user') }} ({{ userSkills.length }})
              </h2>
              <button
                type="button"
                class="ghost-icon sm"
                :title="t('components.skill.actions.openFolder')"
                @click="handleOpenFolderForLocation('user')"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" fill="none"
                    stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
                </svg>
              </button>
            </div>
            <div class="skill-list installed-skills">
              <SkillCard
                v-for="skill in userSkills"
                :key="skill.key"
                :skill="skill"
                :expanded="expandedSkills.has(skill.key)"
                :toggling="togglingSkill === skill.key"
                :uninstalling="processingSkill === uninstallProcessingKey(skill)"
                @toggle="handleToggle"
                @expand="toggleExpand"
                @uninstall="handleUninstall"
                @view="openGithub"
              />
            </div>
          </div>

          <div v-if="projectSkills.length === 0 && userSkills.length === 0" class="skill-empty-installed">
            {{ t('components.skill.list.noInstalled') }}
          </div>

          <div class="skill-divider"></div>

          <div class="skill-group">
            <div class="skill-group-header">
              <h2 class="skill-group-title">
                {{ t('components.skill.groups.available') }} ({{ availableSkills.length }})
              </h2>
            </div>

            <div v-if="availableSkills.length > 0" class="skill-list">
              <article v-for="skill in availableSkills" :key="skill.key || skill.directory" class="skill-card available-card">
                <div class="skill-card-head">
                  <div>
                    <p class="skill-card-eyebrow">{{ skill.directory }}</p>
                    <h3>{{ skill.name }}</h3>
                  </div>
                  <div class="skill-card-actions">
                    <button type="button" class="ghost-icon sm" :title="t('components.skill.actions.view')"
                      :data-tooltip="t('components.skill.actions.view')" @click="openGithub(skill.readme_url)">
                      <svg viewBox="0 0 24 24" aria-hidden="true">
                        <path d="M12 5h7v7M19 5l-9 9" fill="none" stroke="currentColor" stroke-width="1.6"
                          stroke-linecap="round" stroke-linejoin="round" />
                        <path d="M11 6H7a2 2 0 00-2 2v9a2 2 0 002 2h9a2 2 0 002-2v-4" fill="none" stroke="currentColor"
                          stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
                      </svg>
                    </button>
                    <button
                      type="button"
                      class="ghost-icon sm"
                      :title="canInstallSkill(skill) ? t('components.skill.actions.install') : t('components.skill.list.missingRepo')"
                      :data-tooltip="canInstallSkill(skill) ? t('components.skill.actions.install') : t('components.skill.list.missingRepo')"
                      :disabled="!canInstallSkill(skill)"
                      @click="openInstallModal(skill)"
                    >
                      <svg viewBox="0 0 24 24" aria-hidden="true">
                        <path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"
                          stroke-linejoin="round" fill="none" />
                      </svg>
                    </button>
                  </div>
                </div>
                <p class="skill-card-desc">
                  {{ skill.description || t('components.skill.list.noDescription') }}
                </p>
              </article>
            </div>

            <div v-else class="skill-empty">
              {{ t('components.skill.list.empty') }}
            </div>
          </div>
        </template>

        <p v-if="skillsError" class="skill-error">{{ skillsError }}</p>
      </section>

    <SkillInstallModal
      :open="installModalOpen"
      :platform="activePlatform"
      :target="installTarget"
      @close="closeInstallModal"
      @installed="loadSkillsForPlatform"
      @error="(message) => (skillsError = message)"
    />

    <SkillRepoModal
      :open="repoModalOpen"
      @close="closeRepoModal"
      @updated="loadSkillsForPlatform"
    />
  </PageLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Browser } from '@wailsio/runtime'
import {
  fetchSkills,
  fetchSkillsForPlatform,
  uninstallSkillEx,
  toggleSkill,
  openSkillFolder,
  type SkillSummary,
} from '../../services/skill'
import BaseButton from '../common/BaseButton.vue'
import PageLayout from '../common/PageLayout.vue'
import SkillCard from './SkillCard.vue'
import SkillInstallModal from './SkillInstallModal.vue'
import SkillRepoModal from './SkillRepoModal.vue'

const { t } = useI18n()

// Platform definitions (use computed for i18n reactivity)
const platforms = computed(() => [
  { value: 'claude' as const, label: t('components.skill.platform.claude') },
  { value: 'codex' as const, label: t('components.skill.platform.codex') }
])

// State
const activePlatform = ref<'claude' | 'codex'>('claude')
const installedSkills = ref<SkillSummary[]>([])
const catalogSkills = ref<SkillSummary[]>([])
const loading = ref(false)
const skillsError = ref('')
const processingSkill = ref('')
const togglingSkill = ref('')
const repoModalOpen = ref(false)

// Install modal state
const installModalOpen = ref(false)
const installTarget = ref<SkillSummary | null>(null)

// Expanded skills
const expandedSkills = ref<Set<string>>(new Set())

const refreshing = computed(() => loading.value)

const projectSkills = computed(() =>
  installedSkills.value.filter((s) => s.install_location === 'project' && s.installed)
)

const userSkills = computed(() =>
  installedSkills.value.filter((s) => s.install_location === 'user' && s.installed)
)

const availableSkills = computed(() =>
  buildAvailableSkills(catalogSkills.value, installedSkills.value)
)

// Skill identity helpers
const skillIdentity = (skill: SkillSummary) =>
  skill.key || `${(skill.repo_owner ?? 'local').toLowerCase()}:${skill.directory.toLowerCase()}`

const uninstallProcessingKey = (skill: SkillSummary) => `uninstall:${skillIdentity(skill)}`

const canInstallSkill = (skill: SkillSummary) => Boolean(skill.repo_owner && skill.repo_name)

// Platform switching
const switchPlatform = async (platform: 'claude' | 'codex') => {
  activePlatform.value = platform
  await loadSkillsForPlatform()
}

const directoryKey = (value: string) => value.trim().toLowerCase()

const buildAvailableSkills = (catalog: SkillSummary[], installed: SkillSummary[]) => {
  const installedDirs = new Set(installed.map((s) => directoryKey(s.directory)))
  return catalog
    .filter((s) => canInstallSkill(s))
    .filter((s) => !installedDirs.has(directoryKey(s.directory)))
}

// Load skills for current platform
const loadSkillsForPlatform = async () => {
  loading.value = true
  skillsError.value = ''
  try {
    installedSkills.value = await fetchSkillsForPlatform(activePlatform.value)
    catalogSkills.value = await fetchSkills()
  } catch (error) {
    console.error('failed to load skills', error)
    skillsError.value = t('components.skill.list.error')
  } finally {
    loading.value = false
    processingSkill.value = ''
  }
}

const refresh = () => {
  void loadSkillsForPlatform()
}

// Toggle skill enabled status
const handleToggle = async (skill: SkillSummary, enabled: boolean) => {
  togglingSkill.value = skill.key
  try {
    await toggleSkill(
      skill.directory,
      skill.platform || activePlatform.value,
      skill.install_location || 'user',
      enabled
    )
    // Update local state
    const target = installedSkills.value.find((s) => s.key === skill.key)
    if (target) {
      target.enabled = enabled
    }
  } catch (error) {
    console.error('failed to toggle skill', error)
    skillsError.value = t('components.skill.actions.toggleError')
  } finally {
    togglingSkill.value = ''
  }
}

// Toggle content expansion
const toggleExpand = async (skill: SkillSummary) => {
  const key = skill.key
  if (expandedSkills.value.has(key)) {
    expandedSkills.value.delete(key)
  } else {
    expandedSkills.value.add(key)
  }
}

// Open skill folder (default: user location)
const handleOpenFolder = async () => {
  try {
    await openSkillFolder(activePlatform.value, 'user')
  } catch (error) {
    console.error('failed to open folder', error)
  }
}

// Open skill folder for specific location
const handleOpenFolderForLocation = async (location: 'user' | 'project') => {
  try {
    await openSkillFolder(activePlatform.value, location)
  } catch (error) {
    console.error('failed to open folder', error)
  }
}

// Install modal
const openInstallModal = (skill: SkillSummary) => {
  installTarget.value = skill
  installModalOpen.value = true
}

const closeInstallModal = () => {
  installModalOpen.value = false
  installTarget.value = null
}

// Uninstall
const handleUninstall = async (skill: SkillSummary) => {
  processingSkill.value = uninstallProcessingKey(skill)
  try {
    await uninstallSkillEx(
      skill.directory,
      skill.platform || activePlatform.value,
      skill.install_location || 'user'
    )
    skillsError.value = ''
    await loadSkillsForPlatform()
  } catch (error) {
    console.error('failed to uninstall skill', error)
    skillsError.value = t('components.skill.actions.uninstallError', { name: skill.name })
  } finally {
    processingSkill.value = ''
  }
}

const openGithub = (url: string) => {
  if (!url) return
  Browser.OpenURL(url).catch(() => console.error('failed to open link', url))
}

// Repository modal
const openRepoModal = () => {
  repoModalOpen.value = true
}

const closeRepoModal = () => {
  repoModalOpen.value = false
}

onMounted(() => {
  void loadSkillsForPlatform()
})
</script>

<style scoped src="./skill.css"></style>
