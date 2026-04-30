package service

const (
	defaultPageSizeDefault = 50
	maxPageSizeDefault     = 200
)

// clampPageSize returns the effective page size, applying per-service defaults
// and ensuring the result never exceeds maxSize.
func clampPageSize(requested, defaultSize, maxSize int) int {
	if requested <= 0 {
		requested = defaultSize
	}
	if requested > maxSize {
		requested = maxSize
	}
	return requested
}

// resolvePageDefaults returns (defaultSize, maxSize) with sensible fallbacks
// applied when the caller passes zero or negative values.
func resolvePageDefaults(def, max int) (int, int) {
	if def <= 0 {
		def = defaultPageSizeDefault
	}
	if max <= 0 {
		max = maxPageSizeDefault
	}
	if def > max {
		def = max
	}
	return def, max
}
