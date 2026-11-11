// Reasoning effort per model chain link, passed as the AI SDK's `reasoning`
// call option. A link without one leaves the provider's default. No imports,
// so the dashboard can bundle it.
export const REASONING_EFFORTS = ["none", "minimal", "low", "medium", "high", "xhigh", "max"] as const
export type ReasoningEffort = (typeof REASONING_EFFORTS)[number]
