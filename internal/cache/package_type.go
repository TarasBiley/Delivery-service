package cache

import (
	"sync"

	"delivery-service/internal/model"
)

type PackageTypeCache struct {
	mu    sync.RWMutex
	items []model.PackageType
}

func NewPackageTypeCache() *PackageTypeCache {
	return &PackageTypeCache{}
}

func (c *PackageTypeCache) GetAll() ([]model.PackageType, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.items) == 0 {
		return nil, false
	}

	items := make([]model.PackageType, len(c.items))
	copy(items, c.items)

	return items, true
}

func (c *PackageTypeCache) SetAll(items []model.PackageType) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make([]model.PackageType, len(items))
	copy(c.items, items)
}
