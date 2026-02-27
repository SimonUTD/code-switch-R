<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseButton from '../common/BaseButton.vue'
import BaseInput from '../common/BaseInput.vue'
import BaseModal from '../common/BaseModal.vue'

type ConfigForm = Readonly<{
  testModel: string
  testEndpoint: string
  timeout: number
}>

defineProps<{
  open: boolean
  title: string
  providerName?: string
  platform?: string
  saving: boolean
  modelValue: ConfigForm
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'save'): void
  (e: 'update:modelValue', value: ConfigForm): void
}>()

const { t } = useI18n()
</script>

<template>
  <BaseModal :open="open" :title="title" @close="emit('close')">
    <div class="availability-config-modal">
      <p class="availability-config-subtitle">
        {{ providerName }} ({{ platform }})
      </p>

      <div class="availability-config-fields">
        <label class="form-field">
          <span>{{ t('availability.field.testModel') }}</span>
          <BaseInput
            :model-value="modelValue.testModel"
            type="text"
            :disabled="saving"
            :placeholder="t('availability.placeholder.testModel')"
            @update:modelValue="(value) => emit('update:modelValue', { ...modelValue, testModel: value })"
          />
        </label>

        <label class="form-field">
          <span>{{ t('availability.field.testEndpoint') }}</span>
          <BaseInput
            :model-value="modelValue.testEndpoint"
            type="text"
            :disabled="saving"
            :placeholder="t('availability.placeholder.testEndpoint')"
            @update:modelValue="(value) => emit('update:modelValue', { ...modelValue, testEndpoint: value })"
          />
        </label>

        <label class="form-field">
          <span>{{ t('availability.field.timeout') }}</span>
          <input
            :value="modelValue.timeout"
            type="number"
            min="1000"
            class="base-input"
            :disabled="saving"
            :placeholder="t('availability.placeholder.timeout')"
            @input="(event) => emit('update:modelValue', { ...modelValue, timeout: Number((event.target as HTMLInputElement).value) })"
          />
          <p class="availability-config-hint">{{ t('availability.hint.timeout') }}</p>
        </label>
      </div>

      <footer class="form-actions">
        <BaseButton variant="outline" size="sm" type="button" :disabled="saving" @click="emit('close')">
          {{ t('common.cancel') }}
        </BaseButton>
        <BaseButton size="sm" type="button" :disabled="saving" @click="emit('save')">
          {{ saving ? t('common.saving') : t('common.save') }}
        </BaseButton>
      </footer>
    </div>
  </BaseModal>
</template>

<style scoped>
.availability-config-modal {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.availability-config-subtitle {
  margin: 0;
  font-size: 0.85rem;
  color: var(--mac-text-secondary);
}

.availability-config-fields {
  display: grid;
  gap: 14px;
}

.availability-config-hint {
  margin: 8px 0 0;
  font-size: 0.8rem;
  color: var(--mac-text-secondary);
}
</style>
