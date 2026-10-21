package main

// fallbackPricing is hand-maintained from https://opencode.ai/docs/go/ for
// ids the local OMP catalog does not (yet) list. OMP enrichment wins when
// present; this table only fills the gap. Check the docs page when a new id
// appears with zero pricing.
var fallbackPricing = map[string]enrich{}

func init() {
	for id, e := range map[string]enrich{
		"omen-alpha": {Name: "omen-alpha", Ctx: 0, Out: 0},
	} {
		fallbackPricing[id] = e
	}
}
