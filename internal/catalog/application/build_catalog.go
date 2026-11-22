// Package application holds the use case of the catalogue context and the ports it reads through.
package application

import (
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// The catalogue's read models, as callers of BuildCatalog see them.
type (
	Entry = domain.Entry
	Item  = domain.Item
)

// Installed is what one harness has installed, and what could not be read.
type Installed struct {
	Items []domain.Item
	// Skipped describes each configuration that was left out and why (one line each). It never holds a configuration value.
	Skipped []string
}

// Inventory lists what a harness has installed.
type Inventory interface {
	Installed(kernel.Harness) (Installed, error)
}

// ActivityReader answers what the logs say was used.
type ActivityReader interface {
	Activity(domain.Filter) (domain.Activity, error)
}

// Query asks for the catalogue of activity since a time (zero: all) and optionally one harness (empty: all).
type Query struct {
	Since   time.Time
	Harness kernel.Harness
}

// Catalog is the installed items linked to their usage.
type Catalog struct {
	Entries []Entry
	Skipped []string
}

// BuildCatalog links what harnesses have installed to what their sessions used.
type BuildCatalog struct {
	Inventory Inventory
	Activity  ActivityReader
}

// Run builds the catalogue for q.
func (uc BuildCatalog) Run(q Query) (Catalog, error) {
	harnesses := kernel.Harnesses
	if !q.Harness.IsZero() {
		harnesses = []kernel.Harness{q.Harness}
	}
	var c Catalog
	var items []domain.Item
	for _, h := range harnesses {
		in, err := uc.Inventory.Installed(h)
		if err != nil {
			return Catalog{}, err
		}
		items = append(items, in.Items...)
		c.Skipped = append(c.Skipped, in.Skipped...)
	}
	act, err := uc.Activity.Activity(domain.Filter{Since: q.Since, Harness: q.Harness})
	if err != nil {
		return Catalog{}, err
	}
	c.Entries = domain.Link(items, act)
	return c, nil
}
