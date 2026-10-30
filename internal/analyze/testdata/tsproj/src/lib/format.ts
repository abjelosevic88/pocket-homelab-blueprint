/**
 * Formats a name for display. Trims it first.
 */
export function formatName(n: string): string {
  return n.trim();
}

export interface Greeting {
  text: string;
}

export const VERSION = '1.0';

export class Formatter {
  constructor(private prefix: string) {}

  format(n: string): string {
    return this.prefix + formatName(n);
  }
}
