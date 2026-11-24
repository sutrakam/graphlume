// Confidence tiers, as GraphLume reports them. Strongest first.
//
// 'exact' means a compiler proved the binding. The other three are
// inferences of decreasing strength. The extension never invents a tier: an
// item the tool sent without one is shown as having none.

export const CONFIDENCE_TIERS = ['exact', 'high', 'medium', 'low'] as const;

export type Confidence = (typeof CONFIDENCE_TIERS)[number];

export const DEFAULT_MIN_CONFIDENCE: Confidence = 'low';

export function isConfidence(value: unknown): value is Confidence {
  return typeof value === 'string' && (CONFIDENCE_TIERS as readonly string[]).includes(value);
}

/** Reads the minimum-confidence setting, falling back to 'low' (show everything). */
export function normalizeMinConfidence(value: unknown): Confidence {
  if (typeof value === 'string') {
    const lowered = value.trim().toLowerCase();
    if (isConfidence(lowered)) {
      return lowered;
    }
  }
  return DEFAULT_MIN_CONFIDENCE;
}

/** 0 for exact up to 3 for low; 4 for anything that is not a tier. */
export function confidenceRank(value: string | undefined): number {
  const index = (CONFIDENCE_TIERS as readonly string[]).indexOf(value ?? '');
  return index < 0 ? CONFIDENCE_TIERS.length : index;
}

/** One sentence on what a tier licenses, for tooltips. */
export function describeConfidence(value: string | undefined): string {
  switch (value) {
    case 'exact':
      return 'exact: a compiler proved this binding when the index was built.';
    case 'high':
      return 'high: inferred by GraphLume from strong evidence. It may still be wrong.';
    case 'medium':
      return 'medium: inferred by GraphLume from partial evidence. Treat it as a lead.';
    case 'low':
      return 'low: a weak inference. Confirm it before acting on it.';
    default:
      return 'GraphLume reported no confidence tier for this row.';
  }
}
