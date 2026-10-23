function parse(s: string) {
  try {
    return JSON.parse(s);
  } catch (e) {
    return null;
  }
}
