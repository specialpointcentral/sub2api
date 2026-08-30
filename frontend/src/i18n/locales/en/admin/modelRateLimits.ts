export default {
  modelRateLimits: {
    globalTitle: 'Per-model rate limits (global defaults)',
    globalDescription: 'Default per-person limits for each client-facing model. Empty means this layer is off. New rules do not retroactively count requests already in flight.',
    userTitle: 'Per-model rate limits',
    userDescription: 'Rows replace matching global rules for this user.',
    menuItem: 'Per-model rate limits',
    model: 'Model or pattern',
    concurrency: 'Concurrency',
    rpm: 'RPM',
    patternPlaceholder: 'gpt-5.6-* or a concrete model',
    empty: 'No rules. Per-model limiting is off for this scope.',
    addRule: 'Add rule',
    saved: 'Per-model limits saved',
    loadFailed: 'Failed to load per-model limits',
    saveFailed: 'Failed to save per-model limits',
    errors: {
      required: 'Model pattern is required',
      glob: 'Only * is supported as a wildcard',
      duplicate: 'Duplicate model pattern',
      nonNegativeInteger: 'Enter a non-negative integer',
    },
  },
}
