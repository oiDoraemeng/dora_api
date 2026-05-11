<template>
  <AppLayout>
    <div class="image-page grid grid-cols-1 gap-6 xl:grid-cols-[360px_minmax(0,1fr)]">
      <section class="card image-control p-5">
        <div class="mb-4">
          <h2 class="text-2xl font-bold text-gray-900 dark:text-white">{{ tr('imageGeneration.panelTitle', 'Generation Console') }}</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ tr('imageGeneration.panelSubtitle', 'Choose key, model and prompt to create images') }}</p>
        </div>

        <div class="space-y-4">
          <div>
            <label class="input-label">{{ tr('imageGeneration.apiKeyLabel', 'Current API Key') }}</label>
            <Select
              v-model="selectedApiKeyValue"
              :options="apiKeyOptions"
              :placeholder="tr('imageGeneration.apiKeyPlaceholder', 'Select available API key')"
            />
            <p v-if="!activeApiKey" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
              {{ tr('imageGeneration.noApiKeyHint', 'No active API key found. Create one in API Keys first.') }}
            </p>
          </div>

          <div>
            <label class="input-label">{{ tr('imageGeneration.modelLabel', 'Model') }}</label>
            <Select
              v-model="selectedModel"
              :options="modelOptions"
              :placeholder="tr('imageGeneration.modelPlaceholder', 'Select an image model')"
            />
          </div>

          <div>
            <label class="input-label">{{ tr('imageGeneration.promptLabel', 'Prompt') }}</label>
            <TextArea
              v-model="prompt"
              :rows="5"
              :placeholder="tr('imageGeneration.promptPlaceholder', 'Example: Cinematic glasshouse in mist, ultra realistic.')"
            />
          </div>

          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="input-label">{{ tr('imageGeneration.sizeLabel', 'Size') }}</label>
              <Select
                v-model="selectedSize"
                :options="sizeOptions"
              />
            </div>
            <div>
              <label class="input-label">{{ tr('imageGeneration.qualityLabel', 'Quality') }}</label>
              <Select
                v-model="selectedQuality"
                :options="qualityOptions"
              />
            </div>
          </div>

          <div>
            <label class="input-label">{{ tr('imageGeneration.countLabel', 'Count') }}</label>
            <Select
              v-model="selectedCount"
              :options="countOptions"
            />
          </div>

          <button
            class="btn btn-primary w-full"
            :disabled="submitting || !canSubmit"
            @click="handleGenerate"
          >
            <span v-if="submitting">{{ tr('imageGeneration.generating', 'Generating...') }}</span>
            <span v-else>{{ tr('imageGeneration.generateButton', 'Generate Image') }}</span>
          </button>
        </div>
      </section>

      <section class="card image-result p-5">
        <div class="mb-4 flex items-center justify-between gap-3">
          <div>
            <h2 class="text-2xl font-bold text-gray-900 dark:text-white">{{ tr('imageGeneration.resultTitle', 'Generated Result') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ resultMetaText }}</p>
          </div>
          <button class="btn btn-secondary btn-sm" :disabled="history.length === 0" @click="clearHistory">
            {{ tr('imageGeneration.clearHistory', 'Clear History') }}
          </button>
        </div>

        <div class="result-panel relative overflow-hidden rounded-2xl border border-gray-200 bg-gray-100/80 dark:border-dark-700 dark:bg-dark-900/50">
          <div v-if="latestImageUrl" class="result-image-wrap">
            <img :src="latestImageUrl" :alt="latestPrompt || t('imageGeneration.resultTitle')" class="result-image" />
          </div>
          <div v-else class="result-empty">
            <Icon name="sparkles" size="xl" class="text-primary-500" />
            <p class="mt-4 text-xl font-semibold text-gray-800 dark:text-gray-100">{{ tr('imageGeneration.emptyTitle', 'Your latest image will appear here') }}</p>
            <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ tr('imageGeneration.emptyDescription', 'Enter prompt and start generation.') }}</p>
          </div>
        </div>

        <div v-if="history.length > 0" class="mt-5">
              <h3 class="mb-3 text-sm font-semibold text-gray-700 dark:text-gray-300">{{ tr('imageGeneration.historyTitle', 'History') }}</h3>
          <div class="history-grid">
            <button
              v-for="item in history"
              :key="item.id"
              class="history-item"
              :class="{ 'history-item-active': item.id === activeHistoryId }"
              @click="setActive(item.id)"
            >
              <img :src="item.imageUrl" :alt="item.prompt" class="history-thumb" />
            </button>
          </div>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import Icon from '@/components/icons/Icon.vue'
import { keysAPI } from '@/api/keys'
import { userChannelsAPI, type UserAvailableChannel } from '@/api/channels'
import { imagesAPI } from '@/api/images'
import type { ApiKey, SelectOption } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { maskApiKey } from '@/utils/maskApiKey'

interface HistoryItem {
  id: string
  imageUrl: string
  prompt: string
  model: string
  size: string
  createdAt: number
}

const { t } = useI18n()
const appStore = useAppStore()

function tr(key: string, fallback: string): string {
  const text = t(key)
  return text === key ? fallback : text
}

const apiKeys = ref<ApiKey[]>([])
const channels = ref<UserAvailableChannel[]>([])
const selectedApiKeyValue = ref<string | number | boolean | null>(null)
const selectedModel = ref<string | number | boolean | null>('gpt-image-2')
const selectedSize = ref<string | number | boolean | null>('1024x1024')
const selectedQuality = ref<string | number | boolean | null>('auto')
const selectedCount = ref<string | number | boolean | null>(1)
const prompt = ref('')
const submitting = ref(false)
const history = ref<HistoryItem[]>([])
const activeHistoryId = ref<string | null>(null)

const sizeOptions: SelectOption[] = [
  { value: '1024x1024', label: '1K · 1024×1024' },
  { value: '1536x1024', label: '2K · 1536×1024' },
  { value: '1024x1536', label: '2K · 1024×1536' },
  { value: '2048x2048', label: '2K · 2048×2048' },
  { value: '3840x2160', label: '4K · 3840×2160' },
]

const qualityOptions: SelectOption[] = [
  { value: 'auto', label: 'Auto' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
]

const countOptions: SelectOption[] = [
  { value: 1, label: `1 ${tr('imageGeneration.countUnit', 'images')}` },
  { value: 2, label: `2 ${tr('imageGeneration.countUnit', 'images')}` },
  { value: 3, label: `3 ${tr('imageGeneration.countUnit', 'images')}` },
]

const apiKeyOptions = computed<SelectOption[]>(() => {
  return apiKeys.value.map((item) => ({
    value: item.key,
    label: `${item.name} (${maskApiKey(item.key)})`
  }))
})

const discoveredImageModels = computed(() => {
  const modelSet = new Set<string>()
  for (const channel of channels.value) {
    for (const platform of channel.platforms || []) {
      for (const model of platform.supported_models || []) {
        const name = String(model.name || '').trim()
        if (!name) continue
        if (name.startsWith('gpt-image-')) {
          modelSet.add(name)
        }
      }
    }
  }
  return Array.from(modelSet)
})

const modelOptions = computed<SelectOption[]>(() => {
  const fallbackModels = ['gpt-image-2', 'gpt-image-1']
  const models = discoveredImageModels.value.length > 0 ? discoveredImageModels.value : fallbackModels
  return models.map((name) => ({ value: name, label: name }))
})

const activeApiKey = computed(() => {
  const value = String(selectedApiKeyValue.value || '')
  return value.trim()
})

const canSubmit = computed(() => {
  return activeApiKey.value.length > 0 && String(selectedModel.value || '').trim().length > 0 && prompt.value.trim().length > 0
})

const activeHistory = computed(() => {
  if (!activeHistoryId.value) return null
  return history.value.find((item) => item.id === activeHistoryId.value) || null
})

const latestImageUrl = computed(() => activeHistory.value?.imageUrl || '')
const latestPrompt = computed(() => activeHistory.value?.prompt || '')

const resultMetaText = computed(() => {
  if (!activeHistory.value) {
    return tr('imageGeneration.resultMetaEmpty', 'No image generated yet')
  }
  return `${activeHistory.value.model} · ${activeHistory.value.size}`
})

function normalizeDataImage(value: string | undefined): string {
  if (!value) return ''
  const trimmed = value.trim()
  if (!trimmed) return ''
  if (trimmed.startsWith('data:image/')) return trimmed
  return `data:image/png;base64,${trimmed}`
}

function setActive(id: string) {
  activeHistoryId.value = id
}

function clearHistory() {
  history.value = []
  activeHistoryId.value = null
}

async function loadApiKeys() {
  const response = await keysAPI.list(1, 100, { status: 'active' })
  apiKeys.value = response.items || []
  if (!selectedApiKeyValue.value && apiKeys.value.length > 0) {
    selectedApiKeyValue.value = apiKeys.value[0].key
  }
}

async function loadChannels() {
  channels.value = await userChannelsAPI.getAvailable()
}

async function init() {
  try {
    await Promise.all([loadApiKeys(), loadChannels()])
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, tr('imageGeneration.loadFailed', 'Failed to load image generation settings')))
  }
}

async function handleGenerate() {
  if (!canSubmit.value || submitting.value) return

  submitting.value = true
  try {
    const response = await imagesAPI.generateImage({
      apiKey: activeApiKey.value,
      model: String(selectedModel.value),
      prompt: prompt.value.trim(),
      size: String(selectedSize.value || '1024x1024'),
      quality: String(selectedQuality.value || 'auto'),
      n: Number(selectedCount.value || 1)
    })

    const items = response.data || []
    if (items.length === 0) {
      appStore.showWarning(tr('imageGeneration.emptyResult', 'No image returned from the API'))
      return
    }

    const nextItems: HistoryItem[] = []
    const now = Date.now()
    for (let index = 0; index < items.length; index += 1) {
      const item = items[index]
      const imageUrl = item.url || normalizeDataImage(item.b64_json)
      if (!imageUrl) continue
      nextItems.push({
        id: `${now}-${index}`,
        imageUrl,
        prompt: item.revised_prompt || prompt.value.trim(),
        model: String(selectedModel.value),
        size: String(selectedSize.value || '1024x1024'),
        createdAt: now
      })
    }

    if (nextItems.length === 0) {
      appStore.showWarning(tr('imageGeneration.emptyResult', 'No image returned from the API'))
      return
    }

    history.value = [...nextItems, ...history.value].slice(0, 24)
    activeHistoryId.value = nextItems[0].id
    appStore.showSuccess(tr('imageGeneration.generateSuccess', 'Image generated successfully'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, tr('imageGeneration.generateFailed', 'Image generation failed')))
  } finally {
    submitting.value = false
  }
}

onMounted(() => {
  init()
})
</script>

<style scoped>
.image-page {
  min-height: calc(100vh - 170px);
}

.image-control {
  height: fit-content;
  position: sticky;
  top: 88px;
}

.image-result {
  min-height: 680px;
}

.result-panel {
  min-height: 560px;
}

.result-image-wrap {
  height: 100%;
  min-height: 560px;
  display: flex;
  align-items: center;
  justify-content: center;
  background:
    radial-gradient(circle at 10% 12%, rgba(59, 130, 246, 0.08), transparent 35%),
    radial-gradient(circle at 90% 20%, rgba(16, 185, 129, 0.09), transparent 38%),
    linear-gradient(180deg, rgba(255, 255, 255, 0.25), rgba(248, 250, 252, 0.6));
}

.dark .result-image-wrap {
  background:
    radial-gradient(circle at 10% 12%, rgba(59, 130, 246, 0.15), transparent 35%),
    radial-gradient(circle at 90% 20%, rgba(16, 185, 129, 0.13), transparent 38%),
    linear-gradient(180deg, rgba(30, 41, 59, 0.35), rgba(15, 23, 42, 0.75));
}

.result-image {
  max-width: 100%;
  max-height: 560px;
  object-fit: contain;
  border-radius: 14px;
  box-shadow: 0 14px 42px rgba(15, 23, 42, 0.18);
}

.result-empty {
  min-height: 560px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 24px;
  text-align: center;
}

.history-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(112px, 1fr));
  gap: 10px;
}

.history-item {
  border: 1px solid rgba(148, 163, 184, 0.35);
  border-radius: 10px;
  padding: 4px;
  background: rgba(248, 250, 252, 0.8);
  transition: all 0.2s ease;
}

.history-item:hover {
  transform: translateY(-1px);
  border-color: rgba(59, 130, 246, 0.5);
}

.history-item-active {
  border-color: rgba(59, 130, 246, 0.9);
  box-shadow: 0 0 0 2px rgba(59, 130, 246, 0.12);
}

.history-thumb {
  width: 100%;
  aspect-ratio: 1 / 1;
  object-fit: cover;
  border-radius: 7px;
  display: block;
}

@media (max-width: 1279px) {
  .image-control {
    position: static;
  }
}
</style>
