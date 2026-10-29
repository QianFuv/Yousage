/** Original cn implementation, without unrelated application utilities. */
import { clsx, type ClassValue } from 'clsx';
import { extendTailwindMerge } from 'tailwind-merge';
const TW_MERGE = extendTailwindMerge({
  extend: {
    theme: { text: ['2xs', '3xs', '4xs', '5xs'] },
    classGroups: { 'bg-image': [{ bg: ['composer-seam-below', 'composer-seam-above'] }] },
  },
});
/** Combine classes using the upstream dense text-size and background-image rules. */
export function cn(...inputs: ClassValue[]): string { return TW_MERGE(clsx(inputs)); }
