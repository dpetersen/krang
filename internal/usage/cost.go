package usage

import "strings"

// Per-million-token pricing (USD). Approximate as of early 2025.
type modelPricing struct {
	Input       float64
	Output      float64
	CacheRead   float64
	CacheCreate float64
}

var pricing = map[string]modelPricing{
	"opus": {
		Input:       15.0,
		Output:      75.0,
		CacheRead:   3.75,
		CacheCreate: 18.75,
	},
	"sonnet": {
		Input:       3.0,
		Output:      15.0,
		CacheRead:   0.75,
		CacheCreate: 3.75,
	},
	"haiku": {
		Input:       0.80,
		Output:      4.0,
		CacheRead:   0.08,
		CacheCreate: 1.0,
	},
}

func pricingForModel(model string) modelPricing {
	lower := strings.ToLower(model)
	for key, p := range pricing {
		if strings.Contains(lower, key) {
			return p
		}
	}
	// Unknown model: use sonnet pricing as a middle-ground fallback.
	return pricing["sonnet"]
}

func tokenCost(tokens int, perMillion float64) float64 {
	return float64(tokens) * perMillion / 1_000_000
}

func (mu ModelUsage) computeCost(model string) float64 {
	p := pricingForModel(model)
	return tokenCost(mu.Input, p.Input) +
		tokenCost(mu.Output, p.Output) +
		tokenCost(mu.CacheRead, p.CacheRead) +
		tokenCost(mu.CacheCreate, p.CacheCreate)
}
