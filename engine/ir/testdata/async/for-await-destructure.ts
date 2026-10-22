async function f(pages: AsyncIterable<{ items: string[] }>) {
  const all = [];
  for await (const { items } of pages) all.push(...items);
  return all;
}
