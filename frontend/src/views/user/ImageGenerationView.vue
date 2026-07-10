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
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ tr('imageGeneration.apiKeySwitchHint', 'When switching between GPT, Gemini, and Grok image models, choose an API key that supports the matching platform.') }}
            </p>
          </div>

          <div>
            <label class="input-label">{{ tr('imageGeneration.modelLabel', 'Model') }}</label>
            <Select
              v-model="selectedModel"
              :options="modelOptions"
              :placeholder="tr('imageGeneration.modelPlaceholder', 'Select an image model')"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ tr('imageGeneration.modelHint', 'Supports GPT Image, Gemini Image, and Grok Image models.') }}
            </p>
          </div>

          <div>
            <label class="input-label">{{ tr('imageGeneration.backendLabel', 'Generation Method') }}</label>
            <Select
              v-model="selectedGenerationBackend"
              :options="generationBackendOptions"
              :placeholder="tr('imageGeneration.backendPlaceholder', 'Select generation method')"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ backendHintText }}
            </p>
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
          <Transition name="result-panel-fade" mode="out-in">
            <div v-if="submitting" key="generating" class="generation-stage">
              <img
                v-if="latestImageUrl"
                :src="latestImageUrl"
                :alt="latestPrompt || t('imageGeneration.resultTitle')"
                class="generation-backdrop"
              />
              <div class="generation-backdrop-mask"></div>
              <div class="generation-orb generation-orb-a"></div>
              <div class="generation-orb generation-orb-b"></div>
              <div class="generation-scanner"></div>

              <div class="generation-stack" :class="`generation-stack-${previewCardCount}`">
                <div
                  v-for="index in previewCardCount"
                  :key="`preview-card-${index}`"
                  class="generation-card"
                >
                  <div class="generation-card-frame">
                    <div class="generation-card-core"></div>
                    <div class="generation-card-sheen"></div>
                  </div>
                </div>
              </div>

              <div class="generation-copy">
                <div class="generation-copy-row">
                  <span class="generation-status-dot"></span>
                  <span class="generation-status-label">{{ tr('imageGeneration.generatingLive', 'Generating live preview') }}</span>
                </div>

                <div class="generation-chip-row">
                  <span class="generation-chip generation-chip-primary">
                    <ModelIcon :model="selectedModelName" size="14px" />
                    {{ selectedModelProviderLabel }}
                  </span>
                  <span class="generation-chip">{{ selectedModelName }}</span>
                  <span class="generation-chip">{{ selectedSizeLabel }}</span>
                  <span v-if="selectedCountChipText" class="generation-chip">{{ selectedCountChipText }}</span>
                </div>

                <h3 class="generation-title">{{ tr('imageGeneration.generatingTitle', 'Shaping your next image') }}</h3>
                <p class="generation-description">{{ activeGeneratingMessage }}</p>
                <p class="generation-prompt-preview">{{ promptPreview }}</p>

                <div class="generation-activity">
                  <span v-for="bar in 3" :key="bar" class="generation-activity-bar"></span>
                </div>

                <p class="generation-backend-note">{{ selectedBackendLabel }}</p>
              </div>
            </div>

            <div v-else-if="latestImageUrl" key="result" class="result-image-wrap">
              <img :src="latestImageUrl" :alt="latestPrompt || t('imageGeneration.resultTitle')" class="result-image" />
            </div>

            <div v-else key="empty" class="result-empty">
              <Icon name="sparkles" size="xl" class="text-primary-500" />
              <p class="mt-4 text-xl font-semibold text-gray-800 dark:text-gray-100">{{ tr('imageGeneration.emptyTitle', 'Your latest image will appear here') }}</p>
              <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ tr('imageGeneration.emptyDescription', 'Enter prompt and start generation.') }}</p>
            </div>
          </Transition>
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
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { keysAPI } from '@/api/keys'
import { userChannelsAPI, type UserAvailableChannel } from '@/api/channels'
import { imagesAPI } from '@/api/images'
import type { ApiKey, GroupPlatform, SelectOption } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  clearImageGenerationHistory,
  loadImageGenerationDraft,
  loadImageGenerationHistory,
  saveImageGenerationDraftLocal,
  saveImageGenerationDraft,
  saveImageGenerationHistory
} from '@/utils/imageGenerationHistory'
import { maskApiKey } from '@/utils/maskApiKey'

interface HistoryItem {
  id: string
  imageUrl: string
  prompt: string
  model: string
  size: string
  backend: string
  createdAt: number
}

const { t } = useI18n()
const appStore = useAppStore()
const IMAGE_HISTORY_LIMIT = 24

type ImageModelFamily = 'openai' | 'gemini' | 'grok' | 'other'

function tr(key: string, fallback: string): string {
  const text = t(key)
  return text === key ? fallback : text
}

function normalizeTextOption(value: string | number | boolean | null | undefined): string {
  return String(value ?? '').trim()
}

function normalizeImageModelName(name: string): string {
  return name.toLowerCase().trim().replace(/^models\//, '')
}

function isOpenAIImageModel(name: string): boolean {
  const model = normalizeImageModelName(name)
  return model.startsWith('gpt-image-') || model.startsWith('dall-e-')
}

function isGeminiImageModel(name: string): boolean {
  const model = normalizeImageModelName(name)
  return model.startsWith('imagen-') || (model.startsWith('gemini-') && model.includes('image'))
}

function isGrokImageModel(name: string): boolean {
  const model = normalizeImageModelName(name)
  return model === 'grok-imagine' || model.startsWith('grok-imagine-image')
}

function isImageGenerationModel(name: string): boolean {
  return isOpenAIImageModel(name) || isGeminiImageModel(name) || isGrokImageModel(name)
}

function getImageModelFamily(name: string): ImageModelFamily {
  if (isOpenAIImageModel(name)) return 'openai'
  if (isGeminiImageModel(name)) return 'gemini'
  if (isGrokImageModel(name)) return 'grok'
  return 'other'
}

function getImageModelFamilyLabel(name: string): string {
  switch (getImageModelFamily(name)) {
    case 'openai':
      return tr('imageGeneration.modelFamilyOpenAI', 'OpenAI')
    case 'gemini':
      return tr('imageGeneration.modelFamilyGemini', 'Gemini')
    case 'grok':
      return tr('imageGeneration.modelFamilyGrok', 'Grok')
    default:
      return tr('imageGeneration.modelFamilyOther', 'Image')
  }
}

function getPlatformDisplayLabel(platform?: GroupPlatform): string {
  switch (platform) {
    case 'openai':
      return tr('imageGeneration.platformOpenAI', 'OpenAI')
    case 'gemini':
      return tr('imageGeneration.platformGemini', 'Gemini')
    case 'grok':
      return tr('imageGeneration.platformGrok', 'Grok')
    case 'anthropic':
      return tr('imageGeneration.platformAnthropic', 'Anthropic')
    case 'antigravity':
      return tr('imageGeneration.platformAntigravity', 'Antigravity')
    default:
      return tr('imageGeneration.platformUnknown', 'Unknown')
  }
}

function getImageModelDisplayLabel(name: string): string {
  const family = getImageModelFamilyLabel(name)
  if (getImageModelFamily(name) === 'grok') {
    return `${family} - ${name}`
  }
  return `${family} - ${name}`
}

function getApiKeyGroupDisplayLabel(item: ApiKey): string {
  const platformLabel = getPlatformDisplayLabel(item.group?.platform)
  const groupName = item.group?.name?.trim() || (item.group_id ? `#${item.group_id}` : tr('imageGeneration.apiKeyUngrouped', 'Ungrouped'))
  if (platformLabel === tr('imageGeneration.platformUnknown', 'Unknown')) {
    return groupName
  }
  return `${platformLabel} / ${groupName}`
}

function getApiKeyOptionLabel(item: ApiKey): string {
  return `${item.name} [${getApiKeyGroupDisplayLabel(item)}] (${maskApiKey(item.key)})`
}

function getImageModelSortRank(name: string): number {
  if (isOpenAIImageModel(name)) return 0
  if (isGeminiImageModel(name)) return 1
  if (isGrokImageModel(name)) return 2
  return 9
}

function compareImageModels(a: string, b: string): number {
  const rankDiff = getImageModelSortRank(a) - getImageModelSortRank(b)
  if (rankDiff !== 0) return rankDiff
  return a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' })
}

function getGenerationBackendLabel(value: string, modelFamily: ImageModelFamily = 'other'): string {
  if (value === 'openai_images') {
    if (modelFamily === 'grok') {
      return tr('imageGeneration.backendGrokImages', 'Grok Images API')
    }
    return tr('imageGeneration.backendOpenAIImages', 'OpenAI Images API (recommended)')
  }
  if (value === 'chatgpt2api') {
    return tr('imageGeneration.backendChatGPTWeb', 'ChatGPT Web')
  }
  if (value === 'gemini_native') {
    return tr('imageGeneration.backendGeminiNative', 'Gemini Native API')
  }
  return value
}

function buildPromptPreview(value: string, maxLength = 150): string {
  const trimmed = value.trim().replace(/\s+/g, ' ')
  if (!trimmed) {
    return tr('imageGeneration.promptPlaceholderFallback', 'Preparing prompt...')
  }
  if (trimmed.length <= maxLength) return trimmed
  return `${trimmed.slice(0, maxLength - 1)}…`
}

const apiKeys = ref<ApiKey[]>([])
const channels = ref<UserAvailableChannel[]>([])
const selectedApiKeyValue = ref<string | number | boolean | null>(null)
const selectedModel = ref<string | number | boolean | null>('gpt-image-2')
const selectedGenerationBackend = ref<string | number | boolean | null>('openai_images')
const selectedSize = ref<string | number | boolean | null>('1024x1024')
const selectedQuality = ref<string | number | boolean | null>('auto')
const selectedCount = ref<string | number | boolean | null>(1)
const prompt = ref('')
const submitting = ref(false)
const history = ref<HistoryItem[]>([])
const activeHistoryId = ref<string | null>(null)
let historyPersistQueue = Promise.resolve()
let draftPersistTimer: number | null = null
let currentGenerateController: AbortController | null = null
let currentGenerateTaskId: string | null = null
let restoringDraft = false
let modelFamilyToastReady = false
const generationPhase = ref(0)
let generationPhaseTimer: number | null = null

const selectedModelName = computed(() => normalizeTextOption(selectedModel.value))
const selectedModelFamily = computed(() => getImageModelFamily(selectedModelName.value))
const isGeminiSelectedModel = computed(() => selectedModelFamily.value === 'gemini')
const isGrokSelectedModel = computed(() => selectedModelFamily.value === 'grok')
const defaultGenerationBackend = computed(() => isGeminiSelectedModel.value ? 'gemini_native' : 'openai_images')
const defaultQualityByFamily: Record<ImageModelFamily, string> = {
  openai: 'auto',
  gemini: 'auto',
  grok: 'medium',
  other: 'auto'
}

const sizeOptions: SelectOption[] = [
  { value: '1024x1024', label: '1K · 1024×1024' },
  { value: '1536x1024', label: '2K · 1536×1024' },
  { value: '1024x1536', label: '2K · 1024×1536' },
  { value: '2048x2048', label: '2K · 2048×2048' },
  { value: '3840x2160', label: '4K · 3840×2160' },
]

const qualityOptions = computed<SelectOption[]>(() => {
  if (isGrokSelectedModel.value) {
    return [
      { value: 'low', label: 'Low' },
      { value: 'medium', label: 'Medium' },
      { value: 'high', label: 'High' },
    ]
  }
  return [
    { value: 'auto', label: 'Auto' },
    { value: 'low', label: 'Low' },
    { value: 'medium', label: 'Medium' },
    { value: 'high', label: 'High' },
  ]
})

const countOptions: SelectOption[] = [
  { value: 1, label: `1 ${tr('imageGeneration.countUnit', 'images')}` },
  { value: 2, label: `2 ${tr('imageGeneration.countUnit', 'images')}` },
  { value: 3, label: `3 ${tr('imageGeneration.countUnit', 'images')}` },
]

const generationBackendOptions = computed<SelectOption[]>(() => {
  if (isGeminiSelectedModel.value) {
    return [
      { value: 'gemini_native', label: tr('imageGeneration.backendGeminiNative', 'Gemini Native API') },
    ]
  }
  if (isGrokSelectedModel.value) {
    return [
      { value: 'openai_images', label: tr('imageGeneration.backendGrokImages', 'Grok Images API') },
    ]
  }
  return [
    { value: 'openai_images', label: tr('imageGeneration.backendOpenAIImages', 'OpenAI Images API (recommended)') },
    { value: 'chatgpt2api', label: tr('imageGeneration.backendChatGPTWeb', 'ChatGPT Web') },
  ]
})

const apiKeyOptions = computed<SelectOption[]>(() => {
  return apiKeys.value.map((item) => ({
    value: item.key,
    label: getApiKeyOptionLabel(item)
  }))
})

const discoveredImageModels = computed(() => {
  const modelSet = new Set<string>()
  for (const channel of channels.value) {
    for (const platform of channel.platforms || []) {
      for (const model of platform.supported_models || []) {
        const name = String(model.name || '').trim()
        if (!name) continue
        if (isImageGenerationModel(name)) {
          modelSet.add(name)
        }
      }
    }
  }
  return Array.from(modelSet).sort(compareImageModels)
})

const modelOptions = computed<SelectOption[]>(() => {
  const fallbackModels = [
    'gpt-image-2',
    'gpt-image-1',
    'gemini-3.1-flash-image',
    'gemini-2.5-flash-image',
    'grok-imagine',
    'grok-imagine-image',
    'grok-imagine-image-quality'
  ]
  const models = discoveredImageModels.value.length > 0 ? discoveredImageModels.value : fallbackModels.sort(compareImageModels)
  return models.map((name) => ({
    value: name,
    label: getImageModelDisplayLabel(name)
  }))
})

const activeApiKey = computed(() => {
  const value = String(selectedApiKeyValue.value || '')
  return value.trim()
})

const selectedModelProviderLabel = computed(() => getImageModelFamilyLabel(selectedModelName.value))
const selectedBackendValue = computed(() => normalizeTextOption(selectedGenerationBackend.value || defaultGenerationBackend.value))
const selectedBackendLabel = computed(() => getGenerationBackendLabel(selectedBackendValue.value, selectedModelFamily.value))
const backendHintText = computed(() => {
  if (isGeminiSelectedModel.value) {
    return tr('imageGeneration.backendGeminiHint', 'Gemini image models automatically use Gemini Native API. Please use an API key from a Gemini-capable group.')
  }
  if (isGrokSelectedModel.value) {
    return tr('imageGeneration.backendGrokHint', 'Grok image models automatically use the Grok images endpoint. Please use an API key from a Grok-capable group.')
  }
  return tr('imageGeneration.backendHint', 'Supports GPT, Gemini, and Grok image models. OpenAI Images API is recommended for GPT, while ChatGPT Web remains available for OAuth/free account pools.')
})
const selectedSizeLabel = computed(() => normalizeTextOption(selectedSize.value || '1024x1024'))
const selectedCountNumber = computed(() => {
  const value = Number(selectedCount.value || 1)
  return Number.isFinite(value) ? Math.min(Math.max(Math.round(value), 1), 3) : 1
})
const selectedCountChipText = computed(() => selectedCountNumber.value > 1 ? `${selectedCountNumber.value}x` : '')
const previewCardCount = computed(() => selectedCountNumber.value)

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
  if (submitting.value) {
    return [
      selectedModelName.value,
      selectedSizeLabel.value,
      selectedBackendLabel.value
    ].filter(Boolean).join(' / ')
  }
  if (!activeHistory.value) {
    return tr('imageGeneration.resultMetaEmpty', 'No image generated yet')
  }
  return `${activeHistory.value.model} / ${activeHistory.value.size} / ${getGenerationBackendLabel(activeHistory.value.backend, getImageModelFamily(activeHistory.value.model))}`
})

const generationMessages = computed(() => {
  const messages = [
    tr('imageGeneration.generatingStagePrompt', 'Reading your prompt and building the scene.'),
    tr('imageGeneration.generatingStageCompose', 'Balancing light, color and composition.'),
  ]
  if (selectedCountNumber.value > 1) {
    messages.push(tr('imageGeneration.generatingStageVariants', 'Creating multiple variants so the best composition lands first.'))
  } else {
    messages.push(tr('imageGeneration.generatingStageRefine', 'Refining texture and details before the final reveal.'))
  }
  return messages
})

const activeGeneratingMessage = computed(() => {
  const messages = generationMessages.value
  return messages[generationPhase.value % messages.length] || tr('imageGeneration.generating', 'Generating...')
})

const promptPreview = computed(() => buildPromptPreview(prompt.value))

function normalizeDataImage(value: string | undefined): string {
  if (!value) return ''
  const trimmed = value.trim()
  if (!trimmed) return ''
  if (trimmed.startsWith('data:image/')) return trimmed
  return `data:image/png;base64,${trimmed}`
}

function queueHistoryPersistence(operation: () => Promise<void>, action: string): Promise<void> {
  historyPersistQueue = historyPersistQueue
    .catch(() => undefined)
    .then(operation)
    .catch((error) => {
      console.warn(`[ImageGeneration] failed to ${action} history`, error)
    })
  return historyPersistQueue
}

function persistHistory(): Promise<void> {
  const items = history.value.slice(0, IMAGE_HISTORY_LIMIT)
  const activeId = activeHistoryId.value
  return queueHistoryPersistence(() => saveImageGenerationHistory(items, activeId), 'persist')
}

function persistDraft() {
  const draft = {
    prompt: prompt.value,
    model: String(selectedModel.value || ''),
    size: String(selectedSize.value || ''),
    quality: String(selectedQuality.value || ''),
    count: Number(selectedCount.value || 1),
    generationBackend: selectedBackendValue.value
  }
  saveImageGenerationDraftLocal(draft)
  void saveImageGenerationDraft(draft).catch((error) => {
    console.warn('[ImageGeneration] failed to persist draft', error)
  })
}

function scheduleDraftPersistence() {
  if (restoringDraft) return
  if (draftPersistTimer !== null) {
    window.clearTimeout(draftPersistTimer)
  }
  draftPersistTimer = window.setTimeout(() => {
    draftPersistTimer = null
    persistDraft()
  }, 300)
}

function clearGenerationPhaseTimer() {
  if (generationPhaseTimer !== null) {
    window.clearInterval(generationPhaseTimer)
    generationPhaseTimer = null
  }
}

async function restoreHistory() {
  try {
    const snapshot = await loadImageGenerationHistory()
    history.value = snapshot.items.slice(0, IMAGE_HISTORY_LIMIT)
    activeHistoryId.value = snapshot.activeId && history.value.some((item) => item.id === snapshot.activeId)
      ? snapshot.activeId
      : history.value[0]?.id || null
  } catch (error) {
    console.warn('[ImageGeneration] failed to restore history', error)
  }
}

async function restoreDraft() {
  restoringDraft = true
  try {
    const draft = await loadImageGenerationDraft()
    if (!draft) return
    prompt.value = draft.prompt
    selectedModel.value = draft.model || selectedModel.value
    selectedSize.value = draft.size || selectedSize.value
    selectedQuality.value = draft.quality || selectedQuality.value
    selectedCount.value = draft.count || selectedCount.value
    selectedGenerationBackend.value = draft.generationBackend || selectedGenerationBackend.value
  } catch (error) {
    console.warn('[ImageGeneration] failed to restore draft', error)
  } finally {
    restoringDraft = false
  }
}

function setActive(id: string) {
  activeHistoryId.value = id
  void persistHistory()
}

function clearHistory() {
  revokeHistoryObjectURLs(history.value)
  history.value = []
  activeHistoryId.value = null
  queueHistoryPersistence(clearImageGenerationHistory, 'clear')
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
  await Promise.all([restoreHistory(), restoreDraft()])

  try {
    await Promise.all([loadApiKeys(), loadChannels()])
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, tr('imageGeneration.loadFailed', 'Failed to load image generation settings')))
  }
  window.setTimeout(() => {
    modelFamilyToastReady = true
  }, 0)
}

function isAbortError(error: unknown): boolean {
  const maybeError = error as { name?: unknown; code?: unknown } | null
  return maybeError?.name === 'AbortError' || maybeError?.name === 'CanceledError' || maybeError?.code === 'ERR_CANCELED'
}

function cancelCurrentGeneration() {
  const taskId = currentGenerateTaskId
  currentGenerateController?.abort()
  if (taskId && activeApiKey.value) {
    imagesAPI.cancelGeneration(activeApiKey.value, taskId)
  }
}

function createGenerationTaskId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function flushDraftAndCancelCurrentGeneration() {
  if (draftPersistTimer !== null) {
    window.clearTimeout(draftPersistTimer)
    draftPersistTimer = null
  }
  persistDraft()
  cancelCurrentGeneration()
}

function revokeObjectURL(value: string) {
  if (value.startsWith('blob:')) {
    URL.revokeObjectURL(value)
  }
}

function revokeHistoryObjectURLs(items: HistoryItem[]) {
  for (const item of items) {
    revokeObjectURL(item.imageUrl)
  }
}

async function handleGenerate() {
  if (!canSubmit.value || submitting.value) return

  cancelCurrentGeneration()
  currentGenerateController = new AbortController()
  currentGenerateTaskId = createGenerationTaskId()
  submitting.value = true
  try {
    persistDraft()
    const response = await imagesAPI.generateImage({
      apiKey: activeApiKey.value,
      taskId: currentGenerateTaskId,
      model: String(selectedModel.value),
      prompt: prompt.value.trim(),
      size: String(selectedSize.value || '1024x1024'),
      quality: String(selectedQuality.value || 'auto'),
      n: Number(selectedCount.value || 1),
      generationBackend: selectedBackendValue.value,
      signal: currentGenerateController.signal
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
        backend: selectedBackendValue.value,
        createdAt: now
      })
    }

    if (nextItems.length === 0) {
      appStore.showWarning(tr('imageGeneration.emptyResult', 'No image returned from the API'))
      return
    }

    const previousHistory = history.value
    history.value = [...nextItems, ...history.value].slice(0, IMAGE_HISTORY_LIMIT)
    const retainedIDs = new Set(history.value.map((item) => item.id))
    revokeHistoryObjectURLs(previousHistory.filter((item) => !retainedIDs.has(item.id)))
    activeHistoryId.value = nextItems[0].id
    await persistHistory()
    appStore.showSuccess(tr('imageGeneration.generateSuccess', 'Image generated successfully'))
  } catch (error) {
    if (isAbortError(error)) {
      return
    }
    appStore.showError(extractApiErrorMessage(error, tr('imageGeneration.generateFailed', 'Image generation failed')))
  } finally {
    submitting.value = false
    currentGenerateController = null
    currentGenerateTaskId = null
  }
}

watch(
  modelOptions,
  (options) => {
    if (options.length === 0) return
    const current = normalizeTextOption(selectedModel.value)
    const values = new Set(options.map((item) => normalizeTextOption(item.value)))
    if (!current || !values.has(current)) {
      selectedModel.value = options[0].value
    }
  },
  { immediate: true }
)

watch(
  generationBackendOptions,
  (options) => {
    const current = normalizeTextOption(selectedGenerationBackend.value)
    const selectedOption = options.find((item) => normalizeTextOption(item.value) === current)
    if (!selectedOption) {
      selectedGenerationBackend.value = options[0]?.value || defaultGenerationBackend.value
    }
  },
  { immediate: true }
)

watch(
  qualityOptions,
  (options) => {
    const current = normalizeTextOption(selectedQuality.value).toLowerCase()
    const values = new Set(options.map((item) => normalizeTextOption(item.value).toLowerCase()))
    if (!current || !values.has(current)) {
      selectedQuality.value = defaultQualityByFamily[selectedModelFamily.value] || 'auto'
    }
  },
  { immediate: true }
)

watch(selectedModelFamily, (family, previousFamily) => {
  if (!modelFamilyToastReady || family === previousFamily) return
  if (family === 'gemini') {
    appStore.showInfo(tr('imageGeneration.switchToGeminiKeyToast', 'Switched to a Gemini image model. Please switch to a Gemini group API key.'), 5000)
  }
  if (family === 'grok') {
    appStore.showInfo(tr('imageGeneration.switchToGrokKeyToast', 'Switched to a Grok image model. Please switch to a Grok group API key.'), 5000)
  }
  if (family === 'openai') {
    appStore.showInfo(tr('imageGeneration.switchToGPTKeyToast', 'Switched to a GPT image model. Please switch to a GPT/OpenAI group API key.'), 5000)
  }
})

watch(submitting, (value) => {
  clearGenerationPhaseTimer()
  generationPhase.value = 0
  if (!value) return
  generationPhaseTimer = window.setInterval(() => {
    generationPhase.value = (generationPhase.value + 1) % generationMessages.value.length
  }, 2100)
})

watch(
  [prompt, selectedModel, selectedSize, selectedQuality, selectedCount, selectedGenerationBackend],
  scheduleDraftPersistence
)

onMounted(() => {
  init()
  window.addEventListener('beforeunload', flushDraftAndCancelCurrentGeneration)
})

onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', flushDraftAndCancelCurrentGeneration)
  clearGenerationPhaseTimer()
  flushDraftAndCancelCurrentGeneration()
  revokeHistoryObjectURLs(history.value)
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
  isolation: isolate;
}

.result-panel-fade-enter-active,
.result-panel-fade-leave-active {
  transition:
    opacity 0.35s ease,
    transform 0.35s ease;
}

.result-panel-fade-enter-from,
.result-panel-fade-leave-to {
  opacity: 0;
  transform: translateY(10px) scale(0.985);
}

.generation-stage {
  position: relative;
  min-height: 560px;
  display: grid;
  align-items: center;
  padding: 28px;
  overflow: hidden;
  background:
    radial-gradient(circle at 16% 18%, rgba(20, 184, 166, 0.22), transparent 30%),
    radial-gradient(circle at 84% 14%, rgba(59, 130, 246, 0.18), transparent 34%),
    linear-gradient(160deg, rgba(255, 255, 255, 0.92), rgba(241, 245, 249, 0.76));
}

.dark .generation-stage {
  background:
    radial-gradient(circle at 16% 18%, rgba(20, 184, 166, 0.22), transparent 30%),
    radial-gradient(circle at 84% 14%, rgba(59, 130, 246, 0.24), transparent 34%),
    linear-gradient(160deg, rgba(15, 23, 42, 0.92), rgba(15, 23, 42, 0.76));
}

.generation-backdrop {
  position: absolute;
  inset: -6%;
  width: 112%;
  height: 112%;
  object-fit: cover;
  opacity: 0.24;
  filter: blur(26px) saturate(1.08);
  transform: scale(1.08);
}

.generation-backdrop-mask {
  position: absolute;
  inset: 0;
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.2), rgba(255, 255, 255, 0.72)),
    radial-gradient(circle at 50% 50%, rgba(255, 255, 255, 0.04), rgba(255, 255, 255, 0.66));
  backdrop-filter: blur(10px);
}

.dark .generation-backdrop-mask {
  background:
    linear-gradient(180deg, rgba(15, 23, 42, 0.18), rgba(15, 23, 42, 0.74)),
    radial-gradient(circle at 50% 50%, rgba(15, 23, 42, 0.12), rgba(15, 23, 42, 0.72));
}

.generation-orb {
  position: absolute;
  border-radius: 999px;
  filter: blur(2px);
  opacity: 0.68;
  animation: generation-float 9s ease-in-out infinite;
}

.generation-orb-a {
  top: 10%;
  left: 12%;
  width: 160px;
  height: 160px;
  background: rgba(20, 184, 166, 0.24);
}

.generation-orb-b {
  top: 12%;
  right: 10%;
  width: 220px;
  height: 220px;
  background: rgba(59, 130, 246, 0.18);
  animation-delay: -4s;
}

.generation-scanner {
  position: absolute;
  left: -20%;
  right: -20%;
  top: 14%;
  height: 200px;
  background:
    linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.6), transparent),
    linear-gradient(180deg, rgba(20, 184, 166, 0), rgba(20, 184, 166, 0.14), rgba(20, 184, 166, 0));
  opacity: 0.75;
  filter: blur(16px);
  animation: generation-scan 6.2s linear infinite;
}

.generation-stack {
  position: relative;
  z-index: 2;
  width: min(90%, 520px);
  height: min(56vw, 340px);
  min-height: 240px;
  margin: 0 auto;
}

.generation-card {
  position: absolute;
  top: 50%;
  left: 50%;
  width: clamp(180px, 28vw, 250px);
  aspect-ratio: 1 / 1.18;
  transform-origin: center;
  animation: generation-card-float 5.4s ease-in-out infinite;
}

.generation-stack-1 .generation-card {
  transform: translate(-50%, -50%) rotate(-3deg);
}

.generation-stack-2 .generation-card:nth-child(1) {
  transform: translate(-68%, -50%) rotate(-11deg);
}

.generation-stack-2 .generation-card:nth-child(2) {
  transform: translate(-32%, -50%) rotate(9deg);
  animation-delay: -1.2s;
}

.generation-stack-3 .generation-card:nth-child(1) {
  transform: translate(-82%, -48%) rotate(-13deg);
}

.generation-stack-3 .generation-card:nth-child(2) {
  transform: translate(-50%, -52%) rotate(-1deg);
  animation-delay: -1.5s;
}

.generation-stack-3 .generation-card:nth-child(3) {
  transform: translate(-18%, -48%) rotate(11deg);
  animation-delay: -3s;
}

.generation-card-frame {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
  border-radius: 30px;
  border: 1px solid rgba(255, 255, 255, 0.65);
  background: rgba(255, 255, 255, 0.26);
  box-shadow:
    0 18px 50px rgba(15, 23, 42, 0.15),
    inset 0 1px 0 rgba(255, 255, 255, 0.45);
  backdrop-filter: blur(10px);
}

.dark .generation-card-frame {
  border-color: rgba(148, 163, 184, 0.22);
  background: rgba(15, 23, 42, 0.25);
  box-shadow:
    0 18px 50px rgba(2, 6, 23, 0.34),
    inset 0 1px 0 rgba(255, 255, 255, 0.06);
}

.generation-card-core {
  position: absolute;
  inset: 14px;
  border-radius: 22px;
  background:
    radial-gradient(circle at 22% 20%, rgba(255, 255, 255, 0.6), transparent 34%),
    radial-gradient(circle at 82% 12%, rgba(96, 165, 250, 0.34), transparent 32%),
    radial-gradient(circle at 48% 82%, rgba(45, 212, 191, 0.28), transparent 30%),
    repeating-linear-gradient(
      135deg,
      rgba(255, 255, 255, 0.16) 0 14px,
      rgba(255, 255, 255, 0.04) 14px 28px
    ),
    linear-gradient(160deg, rgba(255, 255, 255, 0.7), rgba(226, 232, 240, 0.18));
}

.dark .generation-card-core {
  background:
    radial-gradient(circle at 22% 20%, rgba(255, 255, 255, 0.12), transparent 34%),
    radial-gradient(circle at 82% 12%, rgba(96, 165, 250, 0.24), transparent 32%),
    radial-gradient(circle at 48% 82%, rgba(45, 212, 191, 0.18), transparent 30%),
    repeating-linear-gradient(
      135deg,
      rgba(255, 255, 255, 0.07) 0 14px,
      rgba(255, 255, 255, 0.02) 14px 28px
    ),
    linear-gradient(160deg, rgba(15, 23, 42, 0.62), rgba(30, 41, 59, 0.28));
}

.generation-card-sheen {
  position: absolute;
  inset: -45%;
  background: linear-gradient(110deg, transparent 22%, rgba(255, 255, 255, 0.5) 48%, transparent 72%);
  animation: generation-shimmer 2.4s linear infinite;
}

.generation-copy {
  position: absolute;
  left: 24px;
  right: 24px;
  bottom: 24px;
  z-index: 3;
  padding: 20px 20px 18px;
  border-radius: 24px;
  border: 1px solid rgba(255, 255, 255, 0.72);
  background: rgba(255, 255, 255, 0.7);
  box-shadow: 0 20px 45px rgba(15, 23, 42, 0.14);
  backdrop-filter: blur(16px);
}

.dark .generation-copy {
  border-color: rgba(148, 163, 184, 0.18);
  background: rgba(15, 23, 42, 0.68);
  box-shadow: 0 20px 45px rgba(2, 6, 23, 0.26);
}

.generation-copy-row {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: rgb(13 148 136);
}

.dark .generation-copy-row {
  color: rgb(94 234 212);
}

.generation-status-dot {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  background: rgb(20 184 166);
  box-shadow: 0 0 0 0 rgba(20, 184, 166, 0.5);
  animation: generation-pulse 1.8s ease-out infinite;
}

.generation-chip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
}

.generation-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  padding: 6px 10px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.7);
  color: rgb(51 65 85);
  font-size: 12px;
  font-weight: 600;
  border: 1px solid rgba(148, 163, 184, 0.18);
}

.dark .generation-chip {
  background: rgba(30, 41, 59, 0.7);
  color: rgb(226 232 240);
  border-color: rgba(148, 163, 184, 0.16);
}

.generation-chip-primary {
  background: linear-gradient(135deg, rgba(20, 184, 166, 0.16), rgba(59, 130, 246, 0.16));
  color: rgb(15 118 110);
}

.dark .generation-chip-primary {
  color: rgb(153 246 228);
}

.generation-title {
  margin-top: 16px;
  font-size: clamp(1.25rem, 2.2vw, 1.55rem);
  line-height: 1.2;
  font-weight: 700;
  color: rgb(15 23 42);
}

.dark .generation-title {
  color: rgb(248 250 252);
}

.generation-description {
  margin-top: 8px;
  font-size: 14px;
  line-height: 1.55;
  color: rgb(71 85 105);
}

.dark .generation-description {
  color: rgb(203 213 225);
}

.generation-prompt-preview {
  margin-top: 16px;
  padding: 12px 14px;
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.56);
  border: 1px solid rgba(148, 163, 184, 0.18);
  color: rgb(30 41 59);
  font-size: 13px;
  line-height: 1.6;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.dark .generation-prompt-preview {
  background: rgba(15, 23, 42, 0.48);
  border-color: rgba(148, 163, 184, 0.12);
  color: rgb(226 232 240);
}

.generation-activity {
  display: flex;
  gap: 10px;
  margin-top: 16px;
}

.generation-activity-bar {
  flex: 1;
  height: 4px;
  border-radius: 999px;
  background:
    linear-gradient(90deg, rgba(20, 184, 166, 0.18), rgba(59, 130, 246, 0.68), rgba(20, 184, 166, 0.18));
  background-size: 200% 100%;
  animation: generation-shimmer 1.6s linear infinite;
}

.generation-activity-bar:nth-child(2) {
  animation-delay: -0.2s;
}

.generation-activity-bar:nth-child(3) {
  animation-delay: -0.4s;
}

.generation-backend-note {
  margin-top: 12px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: rgb(100 116 139);
}

.dark .generation-backend-note {
  color: rgb(148 163 184);
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
  animation: result-reveal 0.42s ease-out;
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

@keyframes generation-float {
  0%,
  100% {
    transform: translateY(0px);
  }
  50% {
    transform: translateY(18px);
  }
}

@keyframes generation-scan {
  0% {
    transform: translateY(-48px);
    opacity: 0;
  }
  12% {
    opacity: 0.72;
  }
  50% {
    transform: translateY(168px);
    opacity: 0.78;
  }
  88% {
    opacity: 0.32;
  }
  100% {
    transform: translateY(360px);
    opacity: 0;
  }
}

@keyframes generation-card-float {
  0%,
  100% {
    margin-top: 0;
  }
  50% {
    margin-top: -10px;
  }
}

@keyframes generation-shimmer {
  0% {
    background-position: 200% 0;
    transform: translateX(-8%);
  }
  100% {
    background-position: -200% 0;
    transform: translateX(8%);
  }
}

@keyframes generation-pulse {
  0% {
    box-shadow: 0 0 0 0 rgba(20, 184, 166, 0.42);
  }
  70% {
    box-shadow: 0 0 0 12px rgba(20, 184, 166, 0);
  }
  100% {
    box-shadow: 0 0 0 0 rgba(20, 184, 166, 0);
  }
}

@keyframes result-reveal {
  0% {
    opacity: 0;
    transform: translateY(8px) scale(0.985);
  }
  100% {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

@media (max-width: 1279px) {
  .image-control {
    position: static;
  }
}

@media (max-width: 768px) {
  .generation-stage {
    padding: 20px;
  }

  .generation-stack {
    width: 100%;
    height: 300px;
  }

  .generation-card {
    width: min(54vw, 210px);
  }

  .generation-copy {
    left: 16px;
    right: 16px;
    bottom: 16px;
    padding: 16px;
  }

  .generation-title {
    font-size: 1.15rem;
  }

  .result-empty,
  .result-image-wrap,
  .generation-stage {
    min-height: 500px;
  }
}
</style>
