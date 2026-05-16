export interface StoredImageGenerationHistoryItem {
  id: string
  imageUrl: string
  prompt: string
  model: string
  size: string
  backend: string
  createdAt: number
}

export interface StoredImageGenerationHistorySnapshot {
  items: StoredImageGenerationHistoryItem[]
  activeId: string | null
}

export interface StoredImageGenerationDraft {
  prompt: string
  model: string
  size: string
  quality: string
  count: number
  generationBackend: string
}

interface StoredImageGenerationHistoryMetaItem {
  id: string
  prompt: string
  model: string
  size: string
  backend: string
  createdAt: number
  contentType: string
}

interface StoredImageGenerationHistoryMeta {
  items: StoredImageGenerationHistoryMetaItem[]
  activeId: string | null
}

const DB_NAME = 'sub2api-image-generation'
const DB_VERSION = 2
const META_STORE_NAME = 'history_meta'
const BLOB_STORE_NAME = 'history_blobs'
const LEGACY_STORE_NAME = 'history'
const SNAPSHOT_KEY = 'latest'
const DRAFT_KEY = 'draft'
const DRAFT_LOCAL_STORAGE_KEY = 'sub2api:image-generation:draft'

function indexedDBAvailable(): boolean {
  return typeof window !== 'undefined' && Boolean(window.indexedDB)
}

function openImageHistoryDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (!indexedDBAvailable()) {
      reject(new Error('IndexedDB is not available'))
      return
    }

    const request = window.indexedDB.open(DB_NAME, DB_VERSION)

    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains(META_STORE_NAME)) {
        db.createObjectStore(META_STORE_NAME)
      }
      if (!db.objectStoreNames.contains(BLOB_STORE_NAME)) {
        db.createObjectStore(BLOB_STORE_NAME)
      }
      if (!db.objectStoreNames.contains(LEGACY_STORE_NAME)) {
        db.createObjectStore(LEGACY_STORE_NAME)
      }
    }

    request.onerror = () => {
      reject(request.error || new Error('Failed to open image generation history database'))
    }

    request.onsuccess = () => {
      resolve(request.result)
    }
  })
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function normalizeItem(value: unknown): StoredImageGenerationHistoryItem | null {
  if (!isRecord(value)) return null

  const id = typeof value.id === 'string' ? value.id : ''
  const imageUrl = typeof value.imageUrl === 'string' ? value.imageUrl : ''
  const prompt = typeof value.prompt === 'string' ? value.prompt : ''
  const model = typeof value.model === 'string' ? value.model : ''
  const size = typeof value.size === 'string' ? value.size : ''
  const backend = typeof value.backend === 'string' ? value.backend : ''
  const createdAt = typeof value.createdAt === 'number' ? value.createdAt : 0

  if (!id || !imageUrl) return null

  return {
    id,
    imageUrl,
    prompt,
    model,
    size,
    backend,
    createdAt
  }
}

function normalizeMetaItem(value: unknown): StoredImageGenerationHistoryMetaItem | null {
  if (!isRecord(value)) return null

  const id = typeof value.id === 'string' ? value.id : ''
  const prompt = typeof value.prompt === 'string' ? value.prompt : ''
  const model = typeof value.model === 'string' ? value.model : ''
  const size = typeof value.size === 'string' ? value.size : ''
  const backend = typeof value.backend === 'string' ? value.backend : ''
  const contentType = typeof value.contentType === 'string' ? value.contentType : 'image/png'
  const createdAt = typeof value.createdAt === 'number' ? value.createdAt : 0

  if (!id) return null

  return {
    id,
    prompt,
    model,
    size,
    backend,
    createdAt,
    contentType
  }
}

function normalizeSnapshot(value: unknown): StoredImageGenerationHistorySnapshot {
  if (!isRecord(value)) {
    return { items: [], activeId: null }
  }

  const items = Array.isArray(value.items)
    ? value.items.map(normalizeItem).filter((item): item is StoredImageGenerationHistoryItem => Boolean(item))
    : []
  const activeId = typeof value.activeId === 'string' ? value.activeId : null

  return { items, activeId }
}

function normalizeMeta(value: unknown): StoredImageGenerationHistoryMeta {
  if (!isRecord(value)) {
    return { items: [], activeId: null }
  }

  const items = Array.isArray(value.items)
    ? value.items.map(normalizeMetaItem).filter((item): item is StoredImageGenerationHistoryMetaItem => Boolean(item))
    : []
  const activeId = typeof value.activeId === 'string' ? value.activeId : null

  return { items, activeId }
}

function normalizeDraft(value: unknown): StoredImageGenerationDraft | null {
  if (!isRecord(value)) return null

  const prompt = typeof value.prompt === 'string' ? value.prompt : ''
  const model = typeof value.model === 'string' ? value.model : ''
  const size = typeof value.size === 'string' ? value.size : ''
  const quality = typeof value.quality === 'string' ? value.quality : ''
  const generationBackend = typeof value.generationBackend === 'string' ? value.generationBackend : ''
  const count = typeof value.count === 'number' && Number.isFinite(value.count) ? value.count : 1

  return {
    prompt,
    model,
    size,
    quality,
    count,
    generationBackend
  }
}

function dataURLToBlob(dataURL: string): { blob: Blob; contentType: string } | null {
  const match = /^data:([^;,]+)?(;base64)?,(.*)$/i.exec(dataURL)
  if (!match) return null

  const contentType = match[1] || 'image/png'
  const isBase64 = Boolean(match[2])
  const payload = match[3] || ''
  const binary = isBase64 ? window.atob(payload) : decodeURIComponent(payload)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index)
  }

  return { blob: new Blob([bytes], { type: contentType }), contentType }
}

function imageURLToBlob(imageUrl: string): { blob: Blob; contentType: string } | null {
  if (imageUrl.startsWith('data:image/')) {
    return dataURLToBlob(imageUrl)
  }
  if (imageUrl.startsWith('blob:')) return null

  const contentType = imageUrl.startsWith('data:') ? 'application/octet-stream' : 'text/uri-list'
  return { blob: new Blob([imageUrl], { type: contentType }), contentType }
}

function blobToImageURL(blob: Blob, contentType: string): string {
  if (contentType === 'text/uri-list') return ''
  return URL.createObjectURL(blob)
}

function loadImageGenerationDraftFromLocalStorage(): StoredImageGenerationDraft | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.localStorage.getItem(DRAFT_LOCAL_STORAGE_KEY)
    if (!raw) return null
    return normalizeDraft(JSON.parse(raw))
  } catch {
    return null
  }
}

export function saveImageGenerationDraftLocal(draft: StoredImageGenerationDraft): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(DRAFT_LOCAL_STORAGE_KEY, JSON.stringify(draft))
  } catch {
    // Ignore quota/private-mode failures; IndexedDB persistence remains best-effort.
  }
}

export async function loadImageGenerationHistory(): Promise<StoredImageGenerationHistorySnapshot> {
  if (!indexedDBAvailable()) {
    return { items: [], activeId: null }
  }

  const db = await openImageHistoryDB()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction([META_STORE_NAME, BLOB_STORE_NAME, LEGACY_STORE_NAME], 'readonly')
    const metaStore = transaction.objectStore(META_STORE_NAME)
    const blobStore = transaction.objectStore(BLOB_STORE_NAME)
    const legacyStore = transaction.objectStore(LEGACY_STORE_NAME)
    const metaRequest = metaStore.get(SNAPSHOT_KEY)
    const legacyRequest = legacyStore.get(SNAPSHOT_KEY)
    let settled = false

    function rejectOnce(error: unknown) {
      if (settled) return
      settled = true
      reject(error)
    }

    metaRequest.onsuccess = () => {
      const meta = normalizeMeta(metaRequest.result)
      if (meta.items.length === 0) return

      Promise.all(meta.items.map((item) => readHistoryBlob(blobStore, item)))
        .then((items) => {
          if (settled) return
          const restoredItems = items.filter((item): item is StoredImageGenerationHistoryItem => Boolean(item))
          if (restoredItems.length > 0) {
            settled = true
            resolve({ items: restoredItems, activeId: meta.activeId })
          }
        })
        .catch(rejectOnce)
    }

    legacyRequest.onsuccess = () => {
      if (settled) return
      const legacy = normalizeSnapshot(legacyRequest.result)
      if (legacy.items.length > 0) {
        settled = true
        resolve(legacy)
      }
    }

    metaRequest.onerror = () => {
      rejectOnce(metaRequest.error || new Error('Failed to read image generation history metadata'))
    }

    legacyRequest.onerror = () => {
      rejectOnce(legacyRequest.error || new Error('Failed to read legacy image generation history'))
    }

    transaction.oncomplete = () => {
      db.close()
      if (!settled) {
        settled = true
        resolve({ items: [], activeId: null })
      }
    }

    transaction.onerror = () => {
      db.close()
      rejectOnce(transaction.error || new Error('Failed to read image generation history'))
    }
  })
}

function readHistoryBlob(
  store: IDBObjectStore,
  item: StoredImageGenerationHistoryMetaItem
): Promise<StoredImageGenerationHistoryItem | null> {
  return new Promise((resolve, reject) => {
    const request = store.get(item.id)

    request.onsuccess = () => {
      const value = request.result
      if (!(value instanceof Blob)) {
        resolve(null)
        return
      }
      if (item.contentType === 'text/uri-list') {
        void value.text().then((text) => {
          const imageUrl = text.trim()
          if (!imageUrl) {
            resolve(null)
            return
          }
          resolve({
            id: item.id,
            imageUrl,
            prompt: item.prompt,
            model: item.model,
            size: item.size,
            backend: item.backend,
            createdAt: item.createdAt
          })
        }).catch(reject)
        return
      }
      const imageUrl = blobToImageURL(value, item.contentType)
      if (!imageUrl) {
        resolve(null)
        return
      }
      resolve({
        id: item.id,
        imageUrl,
        prompt: item.prompt,
        model: item.model,
        size: item.size,
        backend: item.backend,
        createdAt: item.createdAt
      })
    }

    request.onerror = () => {
      reject(request.error || new Error('Failed to read image generation history image'))
    }
  })
}

export async function loadImageGenerationDraft(): Promise<StoredImageGenerationDraft | null> {
  if (!indexedDBAvailable()) {
    return loadImageGenerationDraftFromLocalStorage()
  }

  const db = await openImageHistoryDB()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction(LEGACY_STORE_NAME, 'readonly')
    const store = transaction.objectStore(LEGACY_STORE_NAME)
    const request = store.get(DRAFT_KEY)

    request.onsuccess = () => {
      resolve(normalizeDraft(request.result) || loadImageGenerationDraftFromLocalStorage())
    }

    request.onerror = () => {
      reject(request.error || new Error('Failed to read image generation draft'))
    }

    transaction.oncomplete = () => {
      db.close()
    }

    transaction.onerror = () => {
      db.close()
      reject(transaction.error || new Error('Failed to read image generation draft'))
    }
  })
}

export async function saveImageGenerationHistory(
  items: StoredImageGenerationHistoryItem[],
  activeId: string | null
): Promise<void> {
  if (!indexedDBAvailable()) return

  const db = await openImageHistoryDB()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction([META_STORE_NAME, BLOB_STORE_NAME, LEGACY_STORE_NAME], 'readwrite')
    const metaStore = transaction.objectStore(META_STORE_NAME)
    const blobStore = transaction.objectStore(BLOB_STORE_NAME)
    const legacyStore = transaction.objectStore(LEGACY_STORE_NAME)
    const metaItems: StoredImageGenerationHistoryMetaItem[] = []
    const retainedIDs = new Set<string>()

    for (const item of items) {
      const blobResult = imageURLToBlob(item.imageUrl)
      retainedIDs.add(item.id)
      metaItems.push({
        id: item.id,
        prompt: item.prompt,
        model: item.model,
        size: item.size,
        backend: item.backend,
        createdAt: item.createdAt,
        contentType: blobResult?.contentType || 'image/png'
      })
      if (blobResult) {
        blobStore.put(blobResult.blob, item.id)
      }
    }

    metaStore.put({ items: metaItems, activeId }, SNAPSHOT_KEY)
    legacyStore.delete(SNAPSHOT_KEY)
    pruneUnretainedBlobs(blobStore, retainedIDs)

    transaction.oncomplete = () => {
      db.close()
      resolve()
    }

    transaction.onerror = () => {
      db.close()
      reject(transaction.error || new Error('Failed to save image generation history'))
    }
  })
}

function pruneUnretainedBlobs(store: IDBObjectStore, retainedIDs: Set<string>) {
  const request = store.getAllKeys()

  request.onsuccess = () => {
    for (const key of request.result) {
      if (typeof key === 'string' && !retainedIDs.has(key)) {
        store.delete(key)
      }
    }
  }
}

export async function saveImageGenerationDraft(draft: StoredImageGenerationDraft): Promise<void> {
  saveImageGenerationDraftLocal(draft)

  if (!indexedDBAvailable()) return

  const db = await openImageHistoryDB()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction(LEGACY_STORE_NAME, 'readwrite')
    const store = transaction.objectStore(LEGACY_STORE_NAME)
    store.put(draft, DRAFT_KEY)

    transaction.oncomplete = () => {
      db.close()
      resolve()
    }

    transaction.onerror = () => {
      db.close()
      reject(transaction.error || new Error('Failed to save image generation draft'))
    }
  })
}

export async function clearImageGenerationHistory(): Promise<void> {
  if (!indexedDBAvailable()) return

  const db = await openImageHistoryDB()

  return new Promise((resolve, reject) => {
    const transaction = db.transaction([META_STORE_NAME, BLOB_STORE_NAME, LEGACY_STORE_NAME], 'readwrite')
    transaction.objectStore(META_STORE_NAME).delete(SNAPSHOT_KEY)
    transaction.objectStore(BLOB_STORE_NAME).clear()
    transaction.objectStore(LEGACY_STORE_NAME).delete(SNAPSHOT_KEY)

    transaction.oncomplete = () => {
      db.close()
      resolve()
    }

    transaction.onerror = () => {
      db.close()
      reject(transaction.error || new Error('Failed to clear image generation history'))
    }
  })
}
