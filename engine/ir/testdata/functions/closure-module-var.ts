let hits = 0;
export function hit() {
  hits++;
  return () => hits;
}
