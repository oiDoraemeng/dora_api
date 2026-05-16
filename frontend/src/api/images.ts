import axios from 'axios'
import { getLocale } from '@/i18n'

export interface ImageGenerationRequest {
  apiKey: string
  model: string
  prompt: string
  size?: string
  quality?: string
  n?: number
  generationBackend?: 'chatgpt2api' | 'openai_images' | string
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

function extractImageApiError(error: unknown): string {
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
        headers: {
          Authorization: `Bearer ${request.apiKey}`,
          'Accept-Language': getLocale()
        }
      }
    )
    return data
  } catch (error) {
    throw new Error(extractImageApiError(error))
  }
}

export const imagesAPI = {
  generateImage
}

export default imagesAPI
