// Package models is the public SDK surface for resolving a model's
// context-window limit and per-million-token USD prices from a model id (the
// value emitted on session.started.model). It is what drives the TUI's ctx%
// gauge and cost readout, and it is available to external SDK consumers who
// want to build the same indicators over a session's token usage.
//
// Resolution consults a cached copy of the models.dev table — fetched over
// HTTP on first use and cached on disk with a TTL — and falls back to a static
// offline table (200k context plus known Claude prices) when the network is
// unreachable, the fetch fails, or the id is unknown. Limit therefore never
// blocks indefinitely and always returns a usable Info.
//
// The exported surface is intentionally minimal: the Info struct (ContextLimit
// plus per-Mtok InputPrice/OutputPrice) and the Limit function that resolves
// it. The models.dev fetch, on-disk cache, and static fallback are unexported
// implementation detail. See Limit and Info.
package models
