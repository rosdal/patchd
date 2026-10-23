async function* gen(urls: string[]) {
  for (const u of urls) {
    yield await fetch(u);
  }
}
