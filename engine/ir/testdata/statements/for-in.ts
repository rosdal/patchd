function keys(o: object) {
  const out: string[] = [];
  for (const k in o) {
    out.push(k);
  }
  return out;
}
