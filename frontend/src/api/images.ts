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
  generationBackend?: 'chatgpt2api' | 'openai_images' | string
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

const imageGatewayClient = axios.create({
  baseURL: '/v1',
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

export async function generateImage(request: ImageGenerationRequest): Promise<ImageGenerationResponse> {
  const payload = {
    model: request.model,
    task_id: request.taskId,
    prompt: request.prompt,
    size: request.size || '1024x1024',
    quality: request.quality || 'auto',
    n: request.n || 1,
    generation_backend: request.generationBackend || 'chatgpt2api',
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
