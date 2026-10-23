async function f(p: Promise<number>) {
  try {
    await p;
  } catch {
    return -1;
  }
  return 0;
}
