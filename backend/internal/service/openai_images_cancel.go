package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const openAIImagesCancelTombstoneTTL = 5 * time.Minute

type openAIImagesCancelRegistry struct {
	mu         sync.Mutex
	entries    map[string]openAIImagesCancelEntry
	tombstones map[string]time.Time
}

type openAIImagesCancelEntry struct {
	id     string
	cancel context.CancelFunc
}

func openAIImagesCancelKey(ownerID int64, taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if ownerID <= 0 || taskID == "" {
		return ""
	}
	return fmt.Sprintf("%d:%s", ownerID, taskID)
}

func (r *openAIImagesCancelRegistry) register(ownerID int64, taskID string, cancel context.CancelFunc) func() {
	key := openAIImagesCancelKey(ownerID, taskID)
	if key == "" || cancel == nil {
		return func() {}
	}

	r.mu.Lock()
	now := time.Now()
	if r.entries == nil {
		r.entries = make(map[string]openAIImagesCancelEntry)
	}
	r.sweepTombstonesLocked(now)
	if expiresAt, ok := r.tombstones[key]; ok && now.Before(expiresAt) {
		delete(r.tombstones, key)
		r.mu.Unlock()
		cancel()
		return func() {}
	}
	if previous := r.entries[key]; previous.cancel != nil {
		previous.cancel()
	}
	entryID := uuid.NewString()
	r.entries[key] = openAIImagesCancelEntry{id: entryID, cancel: cancel}
	r.mu.Unlock()

	return func() {
		r.mu.Lock()
		if current := r.entries[key]; current.id == entryID {
			delete(r.entries, key)
		}
		r.mu.Unlock()
	}
}

func (r *openAIImagesCancelRegistry) cancel(ownerID int64, taskID string) bool {
	key := openAIImagesCancelKey(ownerID, taskID)
	if key == "" {
		return false
	}

	r.mu.Lock()
	entry := r.entries[key]
	if entry.cancel != nil {
		delete(r.entries, key)
	} else {
		if r.tombstones == nil {
			r.tombstones = make(map[string]time.Time)
		}
		now := time.Now()
		r.sweepTombstonesLocked(now)
		r.tombstones[key] = now.Add(openAIImagesCancelTombstoneTTL)
	}
	r.mu.Unlock()

	if entry.cancel == nil {
		return false
	}
	entry.cancel()
	return true
}

func (r *openAIImagesCancelRegistry) sweepTombstonesLocked(now time.Time) {
	if len(r.tombstones) == 0 {
		return
	}
	for key, expiresAt := range r.tombstones {
		if !now.Before(expiresAt) {
			delete(r.tombstones, key)
		}
	}
}

var openAIImagesCancels openAIImagesCancelRegistry

func WithOpenAIImagesCancelTask(ctx context.Context, ownerID int64, taskID string) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	taskID = strings.TrimSpace(taskID)
	if ownerID <= 0 || taskID == "" {
		return ctx, func() {}
	}

	taskCtx, cancel := context.WithCancel(ctx)
	release := openAIImagesCancels.register(ownerID, taskID, cancel)
	return taskCtx, func() {
		release()
		cancel()
	}
}

func CancelOpenAIImagesTask(ownerID int64, taskID string) bool {
	return openAIImagesCancels.cancel(ownerID, taskID)
}
