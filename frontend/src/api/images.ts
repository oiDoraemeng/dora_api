import axios from 'axios'
import { getLocale } from '@/i18n'

export interface ImageGenerationRequest {
  apiKey: string
  taskId?: string
  model: string
  prompt: string
  size?: string
  quality?: string
  n?: number
  generationBackend?: 'chatgpt2api' | 'openai_images' | 'gemini_native' | string
  signal?: AbortSignal
}

export interface ImageGenerationResult {
  b64_json?: string
  url?: string
  revised_prompt?: string
}

export interface ImageGenerationResponse {
  created?: number
  data?: ImageGenerationResult[]
}

interface GeminiInlineData {
  mimeType?: string
  mime_type?: string
  data?: string
}

interface GeminiPart {
  text?: string
  inlineData?: GeminiInlineData
  inline_data?: GeminiInlineData
}

interface GeminiCandidate {
  content?: {
    parts?: GeminiPart[]
  }
}

interface GeminiGenerateContentResponse {
  candidates?: GeminiCandidate[]
  response?: {
    candidates?: GeminiCandidate[]
  }
}

const imageGatewayClient = axios.create({
  baseURL: '/v1',
  timeout: 120000,
  headers: {
    'Content-Type': 'application/json'
  }
})

const geminiGatewayClient = axios.create({
  baseURL: '/v1beta',
  timeout: 120000,
  headers: {
    'Content-Type': 'application/json'
  }
})

function isCanceledRequest(error: unknown): boolean {
  return axios.isAxiosError(error) && (error.code === 'ERR_CANCELED' || axios.isCancel(error))
}

function extractImageApiError(error: unknown): string {
  if (isCanceledRequest(error)) {
    return 'Image generation cancelled'
  }
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as
      | { error?: { message?: string }; message?: string }
      | undefined
    if (typeof data?.error?.message === 'string' && data.error.message.trim() !== '') {
      return data.error.message
    }
    if (typeof data?.message === 'string' && data.message.trim() !== '') {
      return data.message
    }
    if (typeof error.message === 'string' && error.message.trim() !== '') {
      return error.message
    }
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return 'Image generation failed'
}

function normalizeImageCount(value: number | undefined): number {
  const count = Number(value || 1)
  return Number.isFinite(count) ? Math.min(Math.max(Math.round(count), 1), 3) : 1
}

function normalizeGeminiModelName(value: string): string {
  return value.trim().replace(/^models\//i, '')
}

function parseSizeDimensions(value: string | undefined): { width: number; height: number } | null {
  const match = String(value || '').match(/(\d+)\s*x\s*(\d+)/i)
  if (!match) return null
  const width = Number(match[1])
  const height = Number(match[2])
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return null
  }
  return { width, height }
}

function mapGeminiAspectRatio(size: string | undefined): string | undefined {
  const dimensions = parseSizeDimensions(size)
  if (!dimensions) return undefined
  const { width, height } = dimensions
  if (width === height) return '1:1'
  if (width > height) {
    return width / height >= 1.6 ? '16:9' : '4:3'
  }
  return height / width >= 1.6 ? '9:16' : '3:4'
}

function mapGeminiImageSize(size: string | undefined): string | undefined {
  const dimensions = parseSizeDimensions(size)
  if (!dimensions) return undefined
  const longestSide = Math.max(dimensions.width, dimensions.height)
  if (longestSide >= 3000) return '4K'
  if (longestSide >= 1500) return '2K'
  return '1K'
}

function buildGeminiImagePayload(request: ImageGenerationRequest) {
  const imageConfig: Record<string, string> = {}
  const aspectRatio = mapGeminiAspectRatio(request.size)
  const imageSize = mapGeminiImageSize(request.size)
  if (aspectRatio) imageConfig.aspectRatio = aspectRatio
  if (imageSize) imageConfig.imageSize = imageSize

  return {
    contents: [
      {
        role: 'user',
        parts: [
          {
            text: request.prompt
          }
        ]
      }
    ],
    generationConfig: {
      responseModalities: ['TEXT', 'IMAGE'],
      ...(Object.keys(imageConfig).length > 0 ? { imageConfig } : {})
    }
  }
}

function collectGeminiImages(response: GeminiGenerateContentResponse, fallbackPrompt: string): ImageGenerationResult[] {
  const candidates = response.response?.candidates || response.candidates || []
  const images: ImageGenerationResult[] = []

  for (const candidate of candidates) {
    const parts = candidate.content?.parts || []
    const text = parts
      .map((part) => part.text?.trim() || '')
      .filter(Boolean)
      .join('\n')

    for (const part of parts) {
      const inlineData = part.inlineData || part.inline_data
      const mimeType = inlineData?.mimeType || inlineData?.mime_type || 'image/png'
      const data = inlineData?.data?.trim()
      if (!data || !mimeType.toLowerCase().startsWith('image/')) continue
      images.push({
        url: `data:${mimeType};base64,${data}`,
        revised_prompt: text || fallbackPrompt
      })
    }
  }

  return images
}

async function generateGeminiNativeImage(request: ImageGenerationRequest): Promise<ImageGenerationResponse> {
  const model = normalizeGeminiModelName(request.model)
  const count = normalizeImageCount(request.n)
  const data: ImageGenerationResult[] = []

  try {
    for (let index = 0; index < count; index += 1) {
      const response = await geminiGatewayClient.post<GeminiGenerateContentResponse>(
        `/models/${encodeURIComponent(model)}:generateContent`,
        buildGeminiImagePayload(request),
        {
          signal: request.signal,
          headers: {
            Authorization: `Bearer ${request.apiKey}`,
            'Accept-Language': getLocale()
          }
        }
      )
      data.push(...collectGeminiImages(response.data, request.prompt))
    }

    return {
      created: Math.floor(Date.now() / 1000),
      data
    }
  } catch (error) {
    if (isCanceledRequest(error)) {
      throw error
    }
    throw new Error(extractImageApiError(error))
  }
}

export async function generateImage(request: ImageGenerationRequest): Promise<ImageGenerationResponse> {
  const generationBackend = request.generationBackend || 'chatgpt2api'
  if (generationBackend === 'gemini_native') {
    return generateGeminiNativeImage(request)
  }

  const payload = {
    model: request.model,
    task_id: request.taskId,
    prompt: request.prompt,
    size: request.size || '1024x1024',
    quality: request.quality || 'auto',
    n: request.n || 1,
    generation_backend: generationBackend,
    response_format: 'b64_json',
    stream: false
  }

  try {
    const { data } = await imageGatewayClient.post<ImageGenerationResponse>(
      '/images/generations',
      payload,
      {
        signal: request.signal,
        headers: {
          Authorization: `Bearer ${request.apiKey}`,
          'Accept-Language': getLocale()
        }
      }
    )
    return data
  } catch (error) {
    if (isCanceledRequest(error)) {
      throw error
    }
    throw new Error(extractImageApiError(error))
  }
}

export function cancelGeneration(apiKey: string, taskId: string): void {
  const payload = JSON.stringify({ task_id: taskId })

  void fetch('/v1/images/cancel', {
    method: 'POST',
    body: payload,
    keepalive: true,
    headers: {
      Authorization: `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
      'Accept-Language': getLocale()
    }
  }).catch(() => undefined)
}

export const imagesAPI = {
  generateImage,
  cancelGeneration
}

export default imagesAPI
